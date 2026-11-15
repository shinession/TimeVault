package auth

import (
	"context"
	"errors"
	"strings"
	"time"
)

// unavailableBackoff is how long a signed-in account is trusted as issued
// after the Directory could not be asked, before it is asked again.
const unavailableBackoff = 15 * time.Second

// Identity is an account found by a Directory.
type Identity struct {
	// ID is the sign-in identity, stable for the account (the ee_code).
	ID   string
	Name string
	Role Role
	// Ref is how the Directory finds the account again.
	Ref string
}

var (
	// ErrInvalidCredentials means the account is unknown or inactive, or the
	// password is wrong. It deliberately never says which.
	ErrInvalidCredentials = errors.New("auth: invalid account or password")
	// ErrUnavailable means the Directory could not be asked.
	ErrUnavailable = errors.New("auth: the user directory is unavailable")
)

// Directory is a source of company accounts.
type Directory interface {
	// Authenticate returns the account when login and password are right and
	// the account is active. It returns ErrInvalidCredentials otherwise and
	// ErrUnavailable when the directory cannot be reached.
	Authenticate(ctx context.Context, login, password string) (Identity, error)
	// Refresh re-reads an account during a session. ErrInvalidCredentials
	// means the account is gone or deactivated.
	Refresh(ctx context.Context, id Identity) (Identity, error)
}

func (m *Manager) hasLocalAdmin() bool { return m.o.AdminPasswordHash != "" }

// Login checks the credentials of the local administrator or, for every other
// name, of the Directory. Throttling is the caller's job (see Begin).
func (m *Manager) Login(ctx context.Context, login, password string) (Identity, error) {
	if m.o.Disabled {
		return Identity{}, ErrInvalidCredentials
	}
	login = strings.TrimSpace(login)
	// Whatever the case, the name of the local administrator is never looked up in the Directory.
	isLocal := m.hasLocalAdmin() && strings.EqualFold(login, m.o.AdminUser)
	if m.hasLocalAdmin() && (isLocal || m.o.Directory == nil) {
		// Hash even for a wrong name, so the response time does not tell whether the name exists.
		ok, _ := VerifyPassword(m.o.AdminPasswordHash, password)
		if ok && isLocal {
			return Identity{ID: m.o.AdminUser, Name: m.o.AdminUser, Role: RoleAdmin}, nil
		}
		return Identity{}, ErrInvalidCredentials
	}
	if m.o.Directory == nil {
		return Identity{}, ErrInvalidCredentials
	}
	return m.o.Directory.Authenticate(ctx, login, password)
}

// verdict is what the Directory last said about a signed-in account.
type verdict struct {
	until time.Time
	id    Identity
	gone  bool // deactivated or deleted
	trust bool // the Directory could not be asked: keep the session as issued
}

// Verify re-checks a signed-in session before it acts. A deactivated account
// or a changed role therefore takes effect within RecheckEvery; the session
// returned carries the current name and role. ErrInvalidCredentials means the
// session no longer belongs to an active account.
//
// When the Directory cannot be reached the session is trusted as issued, so a
// restore stays possible while the user directory is down. The cost is that a
// deactivation is only noticed once the directory is back.
func (m *Manager) Verify(ctx context.Context, s Session) (Session, error) {
	if !s.SignedIn() || s.AuthDisabled {
		return s, nil
	}
	if s.Ref == "" { // the local administrator
		if !m.hasLocalAdmin() || s.User != m.o.AdminUser {
			return Session{}, ErrInvalidCredentials
		}
		return s, nil
	}
	if m.o.Directory == nil {
		return Session{}, ErrInvalidCredentials
	}

	now := m.o.Now()
	m.mu.Lock()
	key := s.User + "\x00" + s.Ref
	v, ok := m.checked[key]
	m.mu.Unlock()
	if !ok || !now.Before(v.until) {
		id, err := m.o.Directory.Refresh(ctx, Identity{ID: s.User, Name: s.Name, Role: s.Role, Ref: s.Ref})
		switch {
		case err == nil:
			v = verdict{until: now.Add(m.o.RecheckEvery), id: id}
		case errors.Is(err, ErrInvalidCredentials):
			v = verdict{until: now.Add(m.o.RecheckEvery), gone: true}
		case ctx.Err() != nil:
			// The request was abandoned, not the directory lost: say nothing about the account and remember nothing.
			return Session{}, ctx.Err()
		default:
			v = verdict{until: now.Add(unavailableBackoff), trust: true}
		}
		m.mu.Lock()
		if len(m.checked) > 10_000 {
			clear(m.checked)
		}
		m.checked[key] = v
		m.mu.Unlock()
	}

	switch {
	case v.gone:
		return Session{}, ErrInvalidCredentials
	case v.trust:
		return s, nil
	}
	s.Name, s.Role, s.Ref = v.id.Name, v.id.Role, v.id.Ref
	return s, nil
}

// accountKey is what the sign-in limits of an account are counted under. It
// follows how the directory finds an account: case does not matter, and an
// e-mail address stands for the account its part before the @ names, whatever
// the domain after it, so adding domains does not make a new account out of
// the same employee ID.
func accountKey(login string) string {
	k := strings.ToLower(strings.TrimSpace(login))
	if local, _, found := strings.Cut(k, "@"); found && local != "" {
		k = local
	}
	if len(k) > 128 {
		k = k[:128]
	}
	return k
}
