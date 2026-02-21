// Package callgate provides simple primitives for controlling when a function is allowed to run.
//
// A CallGate decides whether execution should proceed, wait, or be rejected.
// Implementations must be safe for concurrent use.
package callgate

import (
	"context"
	"errors"
)

var (
	ErrBusy               = errors.New("callgate: busy")
	ErrFactoryReturnedNil = errors.New("callgate: factory returned nil")
	ErrBadConfiguration   = errors.New("callgate: bad configuration")
)

// CallGate controls when a function is allowed to run.
//
// A CallGate implementation decides whether a call should run immediately,
// wait until another call finishes, or be rejected.
//
// Acquire tries to obtain permission to execute. It returns a release function
// that must be called after the work is done.
type CallGate interface {
	Acquire(ctx context.Context) (release func(), err error)
}
