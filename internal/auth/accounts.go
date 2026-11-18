package auth

import (
	"context"
	"errors"
	"strings"
)

// Account is a company account as it is shown in a list to pick from. It holds no more than a person needs to
// recognise a colleague.
type Account struct {
	// ID is the employee ID, in upper case.
	ID   string
	Name string
}

// ErrNoAccountList means the user directory cannot list or look up its accounts: there is none, or it is not able to.
var ErrNoAccountList = errors.New("auth: the user directory cannot list accounts")

// AccountLister is implemented by a Directory that can list its active accounts.
type AccountLister interface {
	// Accounts lists the active accounts, ordered by name. ErrUnavailable means the directory cannot be asked.
	Accounts(ctx context.Context) ([]Account, error)
}

// Accounts lists the active company accounts, for the lists in which a person is picked. It is
// ErrNoAccountList when there is no directory or it cannot list.
func (m *Manager) Accounts(ctx context.Context) ([]Account, error) {
	lister, ok := m.o.Directory.(AccountLister)
	if !ok {
		return nil, ErrNoAccountList
	}
	return lister.Accounts(ctx)
}

// FindAccount looks one employee ID up: the active account that has it. ErrInvalidCredentials means there is none
// (it does not say whether the account is unknown or deactivated), ErrUnavailable that the directory cannot be
// asked, and ErrNoAccountList that there is no directory that can answer.
func (m *Manager) FindAccount(ctx context.Context, id string) (Account, error) {
	sso := m.SSO()
	if sso == nil {
		return Account{}, ErrNoAccountList
	}
	found, err := sso.Identify(ctx, id)
	if err != nil {
		return Account{}, err
	}
	return Account{ID: strings.ToUpper(found.ID), Name: found.Name}, nil
}
