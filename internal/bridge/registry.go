package bridge

import (
	"context"
	"fmt"
	"sync"
)

// Registry holds registered provider adapters keyed by provider ID.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
}

// NewRegistry creates a new empty provider registry.
func NewRegistry() *Registry {
	return &Registry{providers: map[string]Provider{}}
}

// Register adds a provider to the registry.
func (r *Registry) Register(p Provider) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := p.ID()
	if _, exists := r.providers[id]; exists {
		return fmt.Errorf("provider %q already registered", id)
	}
	r.providers[id] = p
	return nil
}

// Get returns a provider by ID.
func (r *Registry) Get(id string) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[id]
	if !ok {
		return nil, fmt.Errorf("%w: %q", ErrProviderUnavailable, id)
	}
	return p, nil
}

// List returns all registered provider IDs.
func (r *Registry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	ids := make([]string, 0, len(r.providers))
	for id := range r.providers {
		ids = append(ids, id)
	}
	return ids
}

// HealthAll checks health of all providers and returns results.
func (r *Registry) HealthAll(ctx context.Context) map[string]error {
	r.mu.RLock()
	providers := make(map[string]Provider, len(r.providers))
	for id, p := range r.providers {
		providers[id] = p
	}
	r.mu.RUnlock()

	results := make(map[string]error, len(providers))
	for id, p := range providers {
		results[id] = p.Health(ctx)
	}
	return results
}
