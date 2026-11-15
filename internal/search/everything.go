package search

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	defaultTimeout = 5 * time.Second
	maxTimeout     = 30 * time.Second
	// maxBodyBytes bounds what one engine answer may cost in memory.
	maxBodyBytes = 8 << 20
	// minFileTime is the smallest value read as a Windows FILETIME; anything
	// below it is not a plausible modification time.
	minFileTime = 1e15
	// unixEpochFileTime is the FILETIME of 1970-01-01T00:00:00Z.
	unixEpochFileTime = 116444736000000000
)

// EverythingOptions configures the Everything HTTP adapter.
type EverythingOptions struct {
	// BaseURL is the loopback address of the Everything HTTP server, for
	// example http://127.0.0.1:38480. Credentials belong in Username/Password.
	BaseURL string
	// Username and Password enable HTTP Basic authentication when set.
	Username string
	Password string
	// Timeout bounds every request. Zero means 5s; the maximum is 30s.
	Timeout time.Duration
	// HTTPClient is optional. It is copied and never follows redirects.
	HTTPClient *http.Client
}

// String omits BaseURL and the credentials so a logged option struct never
// leaks them.
func (o EverythingOptions) String() string {
	return "EverythingOptions{Timeout: " + o.Timeout.String() + ", Auth: " + strconv.FormatBool(o.Username != "" || o.Password != "") + "}"
}

// GoString implements fmt.GoStringer for the same reason as String.
func (o EverythingOptions) GoString() string { return o.String() }

// Everything searches the files Everything has indexed through its HTTP JSON
// server (Everything 1.4, voidtools).
type Everything struct {
	base     url.URL
	username string
	password string
	useAuth  bool
	timeout  time.Duration
	client   *http.Client
}

var _ Provider = (*Everything)(nil)

// NewEverything validates the options and returns the adapter. It never
// contacts the engine.
func NewEverything(opts EverythingOptions) (*Everything, error) {
	base, err := parseBaseURL(opts.BaseURL)
	if err != nil {
		return nil, err
	}
	timeout := opts.Timeout
	switch {
	case timeout == 0:
		timeout = defaultTimeout
	case timeout < 0 || timeout > maxTimeout:
		return nil, fmt.Errorf("search: Everything timeout must be positive and at most %s", maxTimeout)
	}
	return &Everything{
		base:     base,
		username: opts.Username,
		password: opts.Password,
		useAuth:  opts.Username != "" || opts.Password != "",
		timeout:  timeout,
		client:   newHTTPClient(opts.HTTPClient),
	}, nil
}

// String names the target without credentials.
func (e *Everything) String() string { return "everything(" + e.base.Host + ")" }

// GoString implements fmt.GoStringer for the same reason as String.
func (e *Everything) GoString() string { return e.String() }

// Name implements Provider.
func (e *Everything) Name() string { return "everything" }

// Search implements Provider. Terms are sanitized and quoted, the engine
// query is confined to q.Root, and every result is checked against q.Root again.
func (e *Everything) Search(ctx context.Context, q Query) (Page, error) {
	req, err := newRequest(q)
	if err != nil {
		return Page{}, err
	}
	body, err := e.get(ctx, url.Values{
		"search":               {buildSearch(req.root.scope(), req.terms)},
		"json":                 {"1"},
		"path_column":          {"1"},
		"size_column":          {"1"},
		"date_modified_column": {"1"},
		"offset":               {strconv.Itoa(req.offset)},
		"count":                {strconv.Itoa(req.limit)},
		"sort":                 {"name"},
		"ascending":            {"1"},
	})
	if err != nil {
		return Page{}, err
	}
	total, items, err := decodeResponse(body)
	if err != nil {
		return Page{}, err
	}
	page := Page{
		Items:      make([]Result, 0, len(items)),
		Total:      total,
		Offset:     req.offset,
		NextOffset: req.next(len(items), total),
	}
	for _, item := range items {
		result, ok := item.result()
		if !ok || !req.root.contains(result.Path) {
			page.Dropped++
			continue
		}
		page.Items = append(page.Items, result)
	}
	return page, nil
}

// Health implements Provider with a count-only request.
func (e *Everything) Health(ctx context.Context) Health {
	start := time.Now()
	total, err := e.indexSize(ctx)
	h := Health{State: StateOK, Latency: time.Since(start), CheckedAt: time.Now()}
	switch {
	case err != nil:
		h.State, h.Detail = StateDegraded, healthDetail(err)
	case total == 0:
		h.State, h.Detail = StateDegraded, "index is empty"
	}
	return h
}

func (e *Everything) indexSize(ctx context.Context) (int, error) {
	body, err := e.get(ctx, url.Values{"search": {"*"}, "json": {"1"}, "count": {"0"}})
	if err != nil {
		return 0, err
	}
	total, _, err := decodeResponse(body)
	return total, err
}

func healthDetail(err error) string {
	detail := strings.TrimPrefix(err.Error(), ErrUnavailable.Error()+": ")
	if r := []rune(detail); len(r) > 160 {
		detail = string(r[:160])
	}
	return detail
}

// get performs one bounded request and returns the response body. Its errors
// never contain the request URL (which carries the user's search text) or the
// credentials.
func (e *Everything) get(ctx context.Context, params url.Values) ([]byte, error) {
	reqCtx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	target := e.base
	target.RawQuery = params.Encode()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, unavailable("cannot build the request")
	}
	req.Header.Set("Accept", "application/json")
	if e.useAuth {
		req.SetBasicAuth(e.username, e.password)
	}
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, e.requestError(ctx, reqCtx, err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, unavailable("authentication failed (HTTP " + strconv.Itoa(resp.StatusCode) + ")")
	case resp.StatusCode != http.StatusOK:
		return nil, unavailable("unexpected HTTP status " + strconv.Itoa(resp.StatusCode))
	case resp.ContentLength > maxBodyBytes:
		return nil, unavailable("response is larger than 8 MiB")
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes+1))
	if err != nil {
		return nil, e.requestError(ctx, reqCtx, err)
	}
	if len(body) > maxBodyBytes {
		return nil, unavailable("response is larger than 8 MiB")
	}
	return body, nil
}

func unavailable(msg string) error {
	return fmt.Errorf("%w: %s", ErrUnavailable, msg)
}

// requestError classifies a transport failure. A caller that gave up is not an
// engine outage, so cancellation is not reported as ErrUnavailable.
func (e *Everything) requestError(parent, reqCtx context.Context, err error) error {
	if errors.Is(parent.Err(), context.Canceled) {
		return fmt.Errorf("search: request canceled: %w", context.Canceled)
	}
	timedOut := errors.Is(reqCtx.Err(), context.DeadlineExceeded)
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		timedOut = timedOut || urlErr.Timeout()
		err = urlErr.Err
	}
	if timedOut || errors.Is(err, context.DeadlineExceeded) {
		return fmt.Errorf("%w: no answer within %s: %w", ErrUnavailable, e.timeout, context.DeadlineExceeded)
	}
	return fmt.Errorf("%w: request failed: %w", ErrUnavailable, err)
}

func newHTTPClient(custom *http.Client) *http.Client {
	var client http.Client
	if custom != nil {
		client = *custom
	} else {
		client.Transport = &http.Transport{
			Proxy:               nil,
			DialContext:         (&net.Dialer{Control: requireLoopback}).DialContext,
			MaxIdleConnsPerHost: 2,
			IdleConnTimeout:     30 * time.Second,
		}
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &client
}

// requireLoopback checks the address that is really dialed, so a "localhost"
// that resolves elsewhere cannot receive the credentials.
func requireLoopback(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return errors.New("refusing to connect to a non-loopback address")
	}
	return nil
}

func parseBaseURL(raw string) (url.URL, error) {
	// The raw value may contain credentials, so it never appears in an error.
	invalid := func(why string) (url.URL, error) {
		return url.URL{}, errors.New("search: invalid Everything base URL: " + why)
	}
	raw = strings.TrimSpace(raw)
	if strings.ContainsAny(raw, "?#") {
		return invalid("must not contain a query or fragment")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return invalid("not a valid URL")
	}
	switch {
	case u.Scheme != "http" && u.Scheme != "https":
		return invalid("scheme must be http or https")
	case u.Opaque != "" || u.Hostname() == "":
		return invalid("host is missing")
	case u.User != nil:
		return invalid("must not contain credentials; use the Username and Password options")
	case !isLoopbackHost(u.Hostname()):
		return invalid("host must be loopback (127.0.0.0/8, ::1 or localhost)")
	}
	if port := u.Port(); port != "" {
		if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
			return invalid("port is out of range")
		}
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/"
	u.RawPath = ""
	return *u, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// engineResponse is the JSON document of Everything 1.4.1.1024:
//
//	{"totalResults":1,"results":[{"type":"file","name":"readme.txt",
//	 "path":"D:\\data\\source","size":"11","date_modified":"133540924280000000"}]}
//
// size and date_modified are strings; date_modified is a Windows FILETIME.
// Folders report "size":"" unless folder sizes are indexed. The root of an
// index is reported with an empty path and the full path as its name.
type engineResponse struct {
	TotalResults looseValue   `json:"totalResults"`
	Results      []engineItem `json:"results"`
}

type engineItem struct {
	Type         string     `json:"type"`
	Name         string     `json:"name"`
	Path         string     `json:"path"`
	Size         looseValue `json:"size"`
	DateModified looseValue `json:"date_modified"`
}

// looseValue keeps the text of a JSON string or number so numbers survive
// without float rounding. null and structured values read as unknown.
type looseValue string

func (v *looseValue) UnmarshalJSON(b []byte) error {
	b = bytes.TrimSpace(b)
	switch {
	case len(b) == 0 || b[0] == '{' || b[0] == '[' || string(b) == "null":
		*v = ""
	case b[0] == '"':
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*v = looseValue(s)
	default:
		*v = looseValue(b)
	}
	return nil
}

func decodeResponse(body []byte) (total int, items []engineItem, err error) {
	var resp engineResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return 0, nil, fmt.Errorf("%w: malformed JSON response: %v", ErrUnavailable, err)
	}
	n, err := strconv.ParseInt(strings.TrimSpace(string(resp.TotalResults)), 10, 64)
	if err != nil || n < 0 || n > math.MaxInt32 {
		return 0, nil, unavailable("response has no valid totalResults")
	}
	return int(n), resp.Results, nil
}

func (i engineItem) result() (Result, bool) {
	dir, name := i.Path, i.Name
	if dir == "" {
		if k := strings.LastIndexByte(name, '\\'); k >= 0 {
			dir, name = name[:k], name[k+1:]
		}
	}
	if name == "" {
		return Result{}, false
	}
	full := name
	if dir != "" {
		full = strings.TrimRight(dir, `\`) + `\` + name
	}
	return Result{
		Name:    name,
		Path:    full,
		IsDir:   isFolder(i.Type),
		Size:    parseSize(i.Size),
		ModTime: parseTime(i.DateModified),
	}, true
}

func isFolder(kind string) bool {
	switch strings.ToLower(kind) {
	case "folder", "directory", "dir":
		return true
	}
	return false
}

func parseSize(v looseValue) int64 {
	if n, err := strconv.ParseInt(strings.TrimSpace(string(v)), 10, 64); err == nil && n >= 0 {
		return n
	}
	return -1
}

// parseTime reads a Windows FILETIME (what Everything sends) or an RFC 3339
// time; anything else is unknown and gives the zero time.
func parseTime(v looseValue) time.Time {
	s := strings.TrimSpace(string(v))
	if ft, err := strconv.ParseUint(s, 10, 64); err == nil {
		if ft < minFileTime || ft > math.MaxInt64 {
			return time.Time{}
		}
		d := int64(ft) - unixEpochFileTime
		return time.Unix(d/1e7, d%1e7*100).UTC()
	}
	if t, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return t.UTC()
	}
	return time.Time{}
}
