package search

import (
	"context"
	"time"
)

// Disabled is the Provider used when the search index is off.
type Disabled struct{}

var _ Provider = Disabled{}

// NewDisabled returns a provider whose searches always fail with ErrDisabled.
func NewDisabled() *Disabled { return &Disabled{} }

// Name implements Provider.
func (Disabled) Name() string { return "disabled" }

// Search implements Provider.
func (Disabled) Search(context.Context, Query) (Page, error) {
	return Page{}, ErrDisabled
}

// Health implements Provider.
func (Disabled) Health(context.Context) Health {
	return Health{State: StateDisabled, Detail: "the search index is off", CheckedAt: time.Now()}
}
