package redisuser

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"timevault/internal/auth"
)

// Directory lets the accounts of the user directory sign in to TimeVault. It
// implements auth.Directory, and its Ping and Addr are what the service needs
// to report the state of the directory.
type Directory struct {
	store *Store
	log   *slog.Logger
	// accounts is the list of accounts kept by Accounts; clock tells it how old that is.
	accounts accountList
	clock    func() time.Time
}

// NewDirectory wraps an open Store. A nil logger discards its messages.
func NewDirectory(store *Store, log *slog.Logger) *Directory {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Directory{store: store, log: log, clock: time.Now}
}

// Ping checks that Redis answers.
func (d *Directory) Ping(ctx context.Context) error { return d.store.Ping(ctx) }

// Addr describes the Redis server for display, without credentials.
func (d *Directory) Addr() string { return d.store.Addr() }

// Authenticate finds the account a person typed (employee ID, name or e-mail)
// and checks the password. A record that is not activated, has no password or
// has another one is answered like an unknown account.
func (d *Directory) Authenticate(ctx context.Context, login, password string) (auth.Identity, error) {
	rec, err := d.store.Lookup(ctx, login)
	if err != nil {
		return auth.Identity{}, d.answer(err, "sign-in")
	}
	if password == "" || !rec.Activated || !rec.CheckPassword(password) {
		return auth.Identity{}, auth.ErrInvalidCredentials
	}
	return identityOf(rec), nil
}

// Refresh reads the account of a signed-in session again. The role and the name
// are the current ones, and the reference follows the record when the other
// application moved it to a new key.
func (d *Directory) Refresh(ctx context.Context, id auth.Identity) (auth.Identity, error) {
	rec, err := d.store.Get(ctx, id.Ref, id.ID)
	if err != nil {
		return auth.Identity{}, d.answer(err, "session check")
	}
	if !rec.Activated {
		return auth.Identity{}, auth.ErrInvalidCredentials
	}
	return identityOf(rec), nil
}

func identityOf(rec Record) auth.Identity {
	role := auth.RoleUser
	if rec.IsAdmin() {
		role = auth.RoleAdmin
	}
	name := rec.Name
	if name == "" {
		name = rec.ECode
	}
	return auth.Identity{ID: rec.ECode, Name: name, Role: role, Ref: rec.Ref}
}

// answer turns an error of the Store into one of the two answers an
// auth.Directory gives: the account is not valid, or the directory cannot say.
func (d *Directory) answer(err error, what string) error {
	switch {
	case errors.Is(err, ErrNotFound):
		return auth.ErrInvalidCredentials
	case errors.Is(err, ErrAmbiguous):
		d.log.Warn(what + " refused: the login matches more than one account in the user directory")
		return auth.ErrInvalidCredentials
	case errors.Is(err, ErrUnreadable):
		d.log.Warn(what+" refused: a record of the user directory cannot be read; run \"timevault-server check-redis-users\"", "error", err)
		return auth.ErrInvalidCredentials
	}
	// ErrUnavailable, or a request that was abandoned: nothing is known about the account.
	return fmt.Errorf("%w: %w", auth.ErrUnavailable, err)
}
