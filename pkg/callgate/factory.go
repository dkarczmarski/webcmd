package callgate

import (
	"errors"
	"fmt"
)

var ErrInvalidCallGateMode = errors.New("invalid callgate mode")

type FactoryProvider interface {
	GetFactory(name string) (Factory, error)
}

type DefaultFactoryProvider struct{}

func NewDefaultFactoryProvider() *DefaultFactoryProvider {
	return &DefaultFactoryProvider{}
}

func (p *DefaultFactoryProvider) GetFactory(name string) (Factory, error) {
	switch name {
	case "single":
		return func() CallGate {
			return NewSingle()
		}, nil
	case "sequence":
		return func() CallGate {
			return NewSequence()
		}, nil
	default:
		return nil, fmt.Errorf("%w: invalid callgate mode: %s", ErrInvalidCallGateMode, name)
	}
}
