package server

import (
	"crypto/rand"
	"encoding/base32"
	"errors"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"timevault/internal/audit"
	"timevault/internal/auth"
)

const (
	// ssoFlowTTL is how long a sign-in has to be completed, as long as the sign-on service keeps its result.
	ssoFlowTTL = 5 * time.Minute
	// ssoMaxFlows and ssoMaxFlowsPerIP bound the memory an anonymous visitor can make the server hold.
	ssoMaxFlows      = 2000
	ssoMaxFlowsPerIP = 5
	// ssoPollGap is the least time between two looks at the sign-on result of one flow.
	ssoPollGap = time.Second
	// ssoTokenBytes random bytes make a token of 24 characters of a-z and 2-7, which the sign-on service accepts.
	ssoTokenBytes = 15
)

var errSSOBusy = errors.New("server: too many single sign-ons are waiting")

// ssoFlow is a sign-in that was started on this server and waits for the sign-on service.
type ssoFlow struct {
	ip      string // the client address that started it
	next    string // where the browser goes when the sign-in succeeds
	created time.Time
	polled  time.Time
}

// ssoFlows remembers the sign-ins in progress. A token is only accepted while it is here, and is removed
// when its sign-in ends, so a result of the sign-on service can be used once, and only for a token this server
// issued.
type ssoFlows struct {
	mu    sync.Mutex
	flows map[string]*ssoFlow
}

func (f *ssoFlows) start(ip, next string, now time.Time) (string, error) {
	var raw [ssoTokenBytes]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	token := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(raw[:]))

	f.mu.Lock()
	defer f.mu.Unlock()
	if f.flows == nil {
		f.flows = make(map[string]*ssoFlow)
	}
	perIP := 0
	for t, fl := range f.flows {
		if now.Sub(fl.created) >= ssoFlowTTL {
			delete(f.flows, t)
		} else if fl.ip == ip {
			perIP++
		}
	}
	if perIP >= ssoMaxFlowsPerIP || len(f.flows) >= ssoMaxFlows {
		return "", errSSOBusy
	}
	f.flows[token] = &ssoFlow{ip: ip, next: next, created: now}
	return token, nil
}

// lookup returns a copy of the flow of a token that is still valid.
func (f *ssoFlows) lookup(token string, now time.Time) (ssoFlow, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fl, ok := f.flows[token]
	if !ok || now.Sub(fl.created) >= ssoFlowTTL {
		delete(f.flows, token)
		return ssoFlow{}, false
	}
	return *fl, true
}

// allowPoll reports whether the sign-on result of a flow may be looked at now, and records the look.
func (f *ssoFlows) allowPoll(token string, now time.Time) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	fl, ok := f.flows[token]
	if !ok || now.Sub(fl.polled) < ssoPollGap {
		return false
	}
	fl.polled = now
	return true
}

func (f *ssoFlows) drop(token string) {
	f.mu.Lock()
	delete(f.flows, token)
	f.mu.Unlock()
}

// ssoEnabled reports whether the login page offers single sign-on: the address of the sign-on service is
// configured and the user directory can complete one.
func (s *Server) ssoEnabled() bool { return s.opts.SSOHost != "" && s.auth.SSO() != nil }

func (s *Server) now() time.Time {
	if s.opts.Now != nil {
		return s.opts.Now()
	}
	return time.Now()
}

// handleSSOStart issues the token of a new single sign-in. The browser opens the sign-on page of the sign-on service
// with it, in a window of its own, and then asks handleSSOPoll for the outcome.
func (s *Server) handleSSOStart(w http.ResponseWriter, r *http.Request) {
	if !s.ssoEnabled() {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad_request", "the request is not valid")
		return
	}
	if !sameOrigin(r) {
		writeJSONError(w, http.StatusForbidden, "csrf", "cross-site request refused")
		return
	}
	token, err := s.sso.start(s.clientIP(r), s.safeNext(r.PostFormValue("next")), s.now())
	switch {
	case errors.Is(err, errSSOBusy):
		writeJSONError(w, http.StatusTooManyRequests, "busy", "too many single sign-ons are waiting; try again in a few minutes")
		return
	case err != nil:
		s.log.Error("start a single sign-on", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "internal", "the single sign-on could not be started")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"token":      token,
		"url":        s.opts.SSOHost + "/sso/api/login?token=" + url.QueryEscape(token),
		"expires_in": int(ssoFlowTTL / time.Second),
	})
}

// handleSSOPoll reports how a single sign-in went. The answer is always 200 with a status: pending (ask again),
// ok (the session cookie is set and next is where to go), or a final failure: expired, mismatch, no_user,
// denied, unavailable.
func (s *Server) handleSSOPoll(w http.ResponseWriter, r *http.Request) {
	if !s.ssoEnabled() {
		http.NotFound(w, r)
		return
	}
	if err := r.ParseForm(); err != nil {
		writeJSONError(w, http.StatusBadRequest, "bad_request", "the request is not valid")
		return
	}
	if !sameOrigin(r) {
		writeJSONError(w, http.StatusForbidden, "csrf", "cross-site request refused")
		return
	}
	reply := func(status string, kv ...any) {
		out := map[string]any{"status": status}
		for i := 0; i+1 < len(kv); i += 2 {
			out[kv[i].(string)] = kv[i+1]
		}
		writeJSON(w, http.StatusOK, out)
	}

	token := r.PostFormValue("token")
	now, ip := s.now(), s.clientIP(r)
	flow, ok := s.sso.lookup(token, now)
	if !ok {
		reply("expired")
		return
	}
	failed := func(actor, reason string, extra map[string]any) {
		detail := map[string]any{"method": "sso", "reason": reason}
		for k, v := range extra {
			detail[k] = v
		}
		s.svc.Audit(r.Context(), audit.Event{Actor: truncate(actor, 64), Action: audit.ActionLoginFailed, Result: audit.ResultDenied, RemoteAddr: ip, Detail: detail})
	}
	// refuseAddress ends a sign-in whose addresses do not match. The audit record and the log say which addresses were
	// compared and how this request reached the server, which is what tells a proxy that hides the browser from a
	// proxy that TimeVault does not believe.
	refuseAddress := func(actor, reason, message string, extra map[string]any) {
		for k, v := range s.addressDetail(r) {
			extra[k] = v
		}
		failed(actor, reason, extra)
		keys := make([]string, 0, len(extra))
		for k := range extra {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		attrs := []any{"reason", reason}
		for _, k := range keys {
			attrs = append(attrs, k, extra[k])
		}
		s.log.Warn(message, append(attrs, "hint", addressHint)...)
		reply("mismatch")
	}
	if s.opts.SSORequireSameIP && ip != flow.ip && !sameAddress(ip, flow.ip) {
		s.sso.drop(token)
		refuseAddress("sso", "browser_address_changed", "single sign-on refused: the address of the browser changed during the sign-in",
			map[string]any{"started_from": flow.ip})
		return
	}
	if !s.sso.allowPoll(token, now) {
		reply("pending")
		return
	}

	signOn := s.auth.SSO()
	res, err := signOn.SSOResult(r.Context(), token)
	switch {
	case errors.Is(err, auth.ErrSSOPending):
		reply("pending")
		return
	case errors.Is(err, auth.ErrUnavailable):
		s.log.Warn("single sign-on failed: the user directory cannot be reached", "error", err)
		reply("unavailable")
		return
	case err != nil:
		s.sso.drop(token)
		failed("sso", "result_unreadable", nil)
		reply("denied")
		return
	}
	if res.Status != "ok" || res.ECode == "" {
		s.sso.drop(token)
		failed(ssoActor(res.WindowsUser), "no_windows_user", map[string]any{"status": res.Status})
		reply("no_user")
		return
	}
	if s.opts.SSORequireSameIP && !sameAddress(res.IP, flow.ip) {
		s.sso.drop(token)
		refuseAddress(ssoActor(res.WindowsUser), "address_mismatch", "single sign-on refused: the sign-on service saw another address for the browser than TimeVault did",
			map[string]any{"sso_address": truncate(res.IP, 64), "started_from": flow.ip})
		return
	}
	id, err := signOn.Identify(r.Context(), res.ECode)
	switch {
	case errors.Is(err, auth.ErrUnavailable):
		// The result is still there, so the next look tries again.
		s.log.Warn("single sign-on failed: the user directory cannot be reached", "error", err)
		reply("unavailable")
		return
	case err != nil:
		s.sso.drop(token)
		failed(ssoActor(res.WindowsUser), "no_active_account", map[string]any{"employee_id": truncate(res.ECode, 64)})
		reply("denied")
		return
	}
	// From here the result is used up, whatever happens next.
	s.sso.drop(token)
	sess, err := s.auth.StartSession(w, id)
	if err != nil {
		s.log.Error("start the session of a single sign-on", "error", err)
		writeJSONError(w, http.StatusInternalServerError, "internal", "the session could not be started")
		return
	}
	s.svc.Audit(r.Context(), audit.Event{Actor: sess.User, Action: audit.ActionLogin, RemoteAddr: ip,
		Detail: map[string]any{"role": string(sess.Role), "method": "sso", "windows_user": truncate(res.WindowsUser, 64)}})
	reply("ok", "next", s.u(flow.next))
}

// ssoActor names who a failed single sign-on is recorded for: the Windows account when the sign-on service knew it.
func ssoActor(windowsUser string) string {
	if windowsUser != "" {
		return windowsUser
	}
	return "sso"
}

// parseAddr reads an address as it comes from a header or a socket: with or without a port, IPv6 in brackets or
// not, an IPv4 address mapped into IPv6 as the plain IPv4 address.
func parseAddr(text string) (netip.Addr, bool) {
	text = strings.TrimSpace(text)
	if ap, err := netip.ParseAddrPort(text); err == nil {
		return ap.Addr().WithZone("").Unmap(), true
	}
	text = strings.TrimSuffix(strings.TrimPrefix(text, "["), "]")
	if a, err := netip.ParseAddr(text); err == nil {
		return a.WithZone("").Unmap(), true
	}
	return netip.Addr{}, false
}

// sameAddress reports whether two texts name the same client address; one that is no address never matches.
func sameAddress(a, b string) bool {
	x, okA := parseAddr(a)
	y, okB := parseAddr(b)
	return okA && okB && x == y
}

// connectionAddr is the address of the connection that carried the request: the browser, or the proxy in front of
// the server.
func connectionAddr(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

// addressHint is logged with a sign-in that was refused because two addresses did not match.
const addressHint = "started_from is the address TimeVault took for the browser. If it is the address of a server, a proxy in front " +
	"of TimeVault hides the browser (docs/runbook.md, Two proxies in a row). If trusted_proxy is false and forwarded is set, " +
	"TIMEVAULT_TRUSTED_PROXIES does not list the proxy that connects."

// addressDetail says how a request reached the server: the address of its connection (a proxy, when there is one),
// whether that is a trusted proxy, and the X-Forwarded-For header that came with it. Both are text from the
// network, so they are cut short.
func (s *Server) addressDetail(r *http.Request) map[string]any {
	conn := connectionAddr(r)
	trusted := false
	if a, ok := parseAddr(conn); ok {
		trusted = s.trustedProxy(a)
	}
	d := map[string]any{"connection": truncate(conn, 64), "trusted_proxy": trusted}
	if fwd := strings.Join(r.Header.Values("X-Forwarded-For"), ", "); fwd != "" {
		d["forwarded"] = truncate(fwd, 256)
	}
	return d
}

// clientIP is the address of the browser. It is the address of the connection, unless that is one of the
// configured reverse proxies: then it is the nearest address in the X-Forwarded-For header that is not a proxy
// itself. Anything the proxies did not add, the header's left side, is never believed.
func (s *Server) clientIP(r *http.Request) string {
	peer := connectionAddr(r)
	if len(s.opts.TrustedProxies) == 0 {
		return peer
	}
	if a, ok := parseAddr(peer); !ok || !s.trustedProxy(a) {
		return peer
	}
	var hops []string
	for _, line := range r.Header.Values("X-Forwarded-For") {
		for _, hop := range strings.Split(line, ",") {
			if hop = strings.TrimSpace(hop); hop != "" {
				hops = append(hops, hop)
			}
		}
	}
	for i := len(hops) - 1; i >= 0; i-- {
		a, ok := parseAddr(hops[i])
		if !ok {
			return peer // a hop that is no address: do not guess
		}
		if !s.trustedProxy(a) {
			return a.String()
		}
	}
	return peer
}

func (s *Server) trustedProxy(a netip.Addr) bool {
	for _, p := range s.opts.TrustedProxies {
		if p.Contains(a) {
			return true
		}
	}
	return false
}
