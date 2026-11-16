package auth

import (
	"context"
	"errors"
	"time"
)

// ErrSSOPending means the single sign-on service has not recorded anything for the token (yet).
var ErrSSOPending = errors.New("auth: single sign-on has not answered yet")

// SSOResult is what the single sign-on service recorded for one login token after the browser that opened
// the sign-on page was identified with its Windows account.
type SSOResult struct {
	// Status is "ok" when the Windows user was identified, "no_user" when the browser sent none.
	Status string
	// ECode is the employee ID, the part of the Windows account name after the domain.
	ECode string
	// WindowsUser is the account as Windows names it, such as DOMAIN\user. It is for the audit trail only.
	WindowsUser string
	// IP is the address of the browser as the sign-on service saw it.
	IP string
	// At is when the sign-on service recorded the result.
	At time.Time
}

// SSODirectory is implemented by a Directory that can also complete a single sign-on.
type SSODirectory interface {
	// SSOResult reads what the sign-on service recorded for a token. It returns ErrSSOPending while there is
	// nothing, ErrInvalidCredentials when what is there cannot be trusted and ErrUnavailable when the
	// service's store cannot be asked.
	SSOResult(ctx context.Context, token string) (SSOResult, error)
	// Identify finds the active account of an employee ID, without a password. ErrInvalidCredentials means
	// there is no such account or it is deactivated; ErrUnavailable that the directory cannot be asked.
	Identify(ctx context.Context, ecode string) (Identity, error)
}

// SSO returns the single sign-on side of the Directory, or nil when the Directory has none.
func (m *Manager) SSO() SSODirectory {
	if m.o.Directory == nil {
		return nil
	}
	sso, _ := m.o.Directory.(SSODirectory)
	return sso
}
