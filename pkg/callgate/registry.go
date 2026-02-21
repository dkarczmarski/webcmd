package callgate

import (
	"errors"
	"fmt"
	"sync"
)

var ErrGroupNotFound = errors.New("group not found")

type Factory func() CallGate

type Registry struct {
	mu              sync.RWMutex
	m               map[string]CallGate
	factoryProvider FactoryProvider
}

type RegistryConfig struct {
	FactoryProvider FactoryProvider
}

type RegistryOption func(*RegistryConfig)

func WithDefaults() RegistryOption {
	return func(cfg *RegistryConfig) {
		if cfg.FactoryProvider == nil {
			cfg.FactoryProvider = NewDefaultFactoryProvider()
		}
	}
}

func NewRegistry(opts ...RegistryOption) *Registry {
	cfg := &RegistryConfig{} //nolint:exhaustruct
	for _, opt := range opts {
		opt(cfg)
	}

	//nolint:exhaustruct
	return &Registry{
		m:               make(map[string]CallGate),
		factoryProvider: cfg.FactoryProvider,
	}
}

// GetOrCreateWithFactory returns the CallGate associated with the given group.
//
// If a gate for the group already exists, it is returned.
//
// If the group does not exist and factory is provided, a new gate is created
// using the factory, stored in the registry, and then returned.
//
// If the group does not exist and factory is nil, GetOrCreateWithFactory returns
// ErrGroupNotFound.
func (r *Registry) GetOrCreateWithFactory(group string, factory Factory) (CallGate, error) { //nolint:ireturn
	r.mu.RLock()
	if l, ok := r.m[group]; ok {
		r.mu.RUnlock()

		return l, nil
	}
	r.mu.RUnlock()

	r.mu.Lock()
	defer r.mu.Unlock()

	if l, ok := r.m[group]; ok {
		return l, nil
	}

	if factory == nil {
		return nil, fmt.Errorf("%w: %s", ErrGroupNotFound, group)
	}

	gate := factory()
	if gate == nil {
		return nil, fmt.Errorf("%w: group=%q", ErrFactoryReturnedNil, group)
	}

	r.m[group] = gate

	return gate, nil
}

// GetOrCreate returns the CallGate associated with the given group.
//
// It uses the factoryProvider to get a factory for the given name, and then
// calls GetOrCreateWithFactory to ensure the gate exists.
func (r *Registry) GetOrCreate(group string, name string) (CallGate, error) { //nolint:ireturn
	if r.factoryProvider == nil {
		return nil, fmt.Errorf("%w: factoryProvider is nil", ErrBadConfiguration)
	}

	factory, err := r.factoryProvider.GetFactory(name)
	if err != nil {
		return nil, fmt.Errorf("factory provider: %w", err)
	}

	return r.GetOrCreateWithFactory(group, factory)
}
