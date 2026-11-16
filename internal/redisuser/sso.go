package redisuser

import (
	"context"
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/redis/go-redis/v9"

	"timevault/internal/auth"
)

// ssoPrefix is where the single sign-on service stores its results: "sso_login_<token>" holds
// zlib.compress(pickle.dumps(dict)) for a few minutes, as for the dash application.
const ssoPrefix = "sso_login_"

// ssoToken is the shape of a login token the sign-on service accepts.
var ssoToken = regexp.MustCompile(`^[a-z0-9]{16,40}$`)

const maxSSOField = 254 // runes of a text the sign-on service stored

// SSOResult reads what the single sign-on service recorded for a login token. It returns ErrNotFound while
// there is nothing for the token, ErrUnreadable when what is there is not the expected result and
// ErrUnavailable when Redis cannot be asked. The key is only read, never deleted: a result is used once by
// the caller, and the sign-on service lets it expire.
func (s *Store) SSOResult(ctx context.Context, token string) (auth.SSOResult, error) {
	if !ssoToken.MatchString(token) {
		return auth.SSOResult{}, ErrNotFound
	}
	ctx, cancel := context.WithTimeout(ctx, s.budget)
	defer cancel()
	raw, err := s.rdb.Get(ctx, ssoPrefix+token).Bytes()
	switch {
	case errors.Is(err, redis.Nil):
		return auth.SSOResult{}, ErrNotFound
	case err != nil:
		return auth.SSOResult{}, s.unavailable(err)
	}
	v, err := decodeField(raw)
	if err != nil {
		return auth.SSOResult{}, fmt.Errorf("%w: %v", ErrUnreadable, err)
	}
	dict, ok := v.(*pyDict)
	if !ok {
		return auth.SSOResult{}, fmt.Errorf("%w: the result is not a dict", ErrUnreadable)
	}
	return ssoFromDict(dict)
}

// ssoFromDict reads the fields of a result. The keys it does not know are ignored; a known key of the wrong
// type makes the whole result unreadable.
func ssoFromDict(d *pyDict) (auth.SSOResult, error) {
	var res auth.SSOResult
	for i, k := range d.keys {
		key, ok := k.(string)
		if !ok {
			continue
		}
		val := d.vals[i]
		var dst *string
		switch key {
		case "status":
			dst = &res.Status
		case "ee_code":
			dst = &res.ECode
		case "windows_user":
			dst = &res.WindowsUser
		case "ip":
			dst = &res.IP
		case "ts":
			t, ok := ssoTime(val)
			if !ok {
				return auth.SSOResult{}, fmt.Errorf("%w: ts is not a time", ErrUnreadable)
			}
			res.At = t
			continue
		default:
			continue
		}
		text, ok := asOptionalString(val)
		if !ok || len(text) > 4*maxSSOField || utf8.RuneCountInString(text) > maxSSOField || !utf8.ValidString(text) || hasControl(text) {
			return auth.SSOResult{}, fmt.Errorf("%w: %s is not a plain text", ErrUnreadable, key)
		}
		*dst = strings.TrimSpace(text)
	}
	if res.Status == "" {
		return auth.SSOResult{}, fmt.Errorf("%w: the result has no status", ErrUnreadable)
	}
	return res, nil
}

// ssoTime reads time.time() of Python: seconds since the epoch as a float or an int.
func ssoTime(v value) (time.Time, bool) {
	var sec float64
	switch x := native(v).(type) {
	case float64:
		sec = x
	case int64:
		sec = float64(x)
	case time.Time:
		return x, true
	default:
		return time.Time{}, false
	}
	if math.IsNaN(sec) || sec < minUnix || sec > maxUnix {
		return time.Time{}, false
	}
	whole, frac := math.Modf(sec)
	return time.Unix(int64(whole), int64(frac*1e9)).UTC(), true
}

var _ auth.SSODirectory = (*Directory)(nil)

// SSOResult implements auth.SSODirectory.
func (d *Directory) SSOResult(ctx context.Context, token string) (auth.SSOResult, error) {
	res, err := d.store.SSOResult(ctx, token)
	switch {
	case err == nil:
		return res, nil
	case errors.Is(err, ErrNotFound):
		return auth.SSOResult{}, auth.ErrSSOPending
	case errors.Is(err, ErrUnreadable):
		d.log.Warn("single sign-on refused: the result of the sign-on service cannot be read", "error", err)
		return auth.SSOResult{}, auth.ErrInvalidCredentials
	}
	return auth.SSOResult{}, fmt.Errorf("%w: %w", auth.ErrUnavailable, err)
}

// Identify implements auth.SSODirectory: the newest record of the employee ID, which must be activated.
func (d *Directory) Identify(ctx context.Context, ecode string) (auth.Identity, error) {
	ecode, ok := cleanToken(ecode)
	if !ok {
		return auth.Identity{}, auth.ErrInvalidCredentials
	}
	rec, err := d.store.Get(ctx, "", ecode)
	if err != nil {
		return auth.Identity{}, d.answer(err, "single sign-on")
	}
	if !rec.Activated {
		return auth.Identity{}, auth.ErrInvalidCredentials
	}
	return identityOf(rec), nil
}
