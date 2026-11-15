package search

import (
	"context"
	"time"
)

// Disabled is the Provider used when no search engine is configured.
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
	return Health{State: StateDisabled, Detail: "no search engine configured", CheckedAt: time.Now()}
}
