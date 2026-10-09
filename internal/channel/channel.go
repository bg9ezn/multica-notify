// Package channel defines the one abstraction between rendered notifications
// and the outside world, and the registry that builds channels from config.
//
// The design mirrors Multica's own integrations/channel package: adding a
// channel type means registering a factory, not editing the core. The core
// never learns what a given receiver's JSON looks like.
package channel

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/bg9ezn/multica-notify/internal/message"

	"github.com/bg9ezn/multica-notify/internal/channel/apprise"
	"github.com/bg9ezn/multica-notify/internal/channel/ntfy"
	"github.com/bg9ezn/multica-notify/internal/channel/webhook"
)

// Channel is one outgoing notification target.
type Channel interface {
	Name() string
	Send(ctx context.Context, msg message.Message) error
}

// Factory constructs a channel from raw options. Returning (nil, nil) marks
// the channel intentionally disabled (webhook with an empty URL): the caller
// logs and skips it instead of failing startup.
type Factory func(name string, opts map[string]string) (Channel, error)

// Registry maps channel type names to factories. It is safe for concurrent
// use and designed to be extended at startup, before traffic flows.
type Registry struct {
	mu        sync.RWMutex
	factories map[string]Factory
}

// NewRegistry returns a registry with the built-in channel types registered.
func NewRegistry() *Registry {
	r := &Registry{factories: make(map[string]Factory)}
	_ = r.Register("apprise", fromConcrete(apprise.New))
	_ = r.Register("ntfy", fromConcrete(ntfy.New))
	_ = r.Register("webhook", fromConcrete(webhook.New))
	return r
}

// Register adds a factory. Registering an empty type name is a programming
// error and returns an error rather than panicking.
func (r *Registry) Register(typeName string, f Factory) error {
	if typeName == "" {
		return fmt.Errorf("channel: cannot register empty type name")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.factories[typeName] = f
	return nil
}

// Build constructs one channel. An unknown type names a config typo and is
// reported with the known types.
func (r *Registry) Build(typeName, name string, opts map[string]string) (Channel, error) {
	r.mu.RLock()
	f, ok := r.factories[typeName]
	r.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("channel: unknown type %q for channel %q (known: %v)", typeName, name, r.Types())
	}
	return f(name, opts)
}

// Types lists the registered type names, sorted for stable error messages.
func (r *Registry) Types() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.factories))
	for name := range r.factories {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// fromConcrete adapts an adapter constructor returning its concrete pointer
// type into a Factory. P is constrained to be both *T and a Channel, so a nil
// concrete value — an adapter disabling itself — is detectable: it becomes a
// nil Factory result the caller logs and skips.
func fromConcrete[T any, P interface {
	*T
	Channel
}](ctor func(name string, opts map[string]string) (P, error)) Factory {
	return func(name string, opts map[string]string) (Channel, error) {
		c, err := ctor(name, opts)
		if err != nil {
			return nil, err
		}
		if c == nil {
			return nil, nil
		}
		return c, nil
	}
}
