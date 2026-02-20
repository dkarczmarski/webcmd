package callgate

import (
	"context"
	"fmt"
	"sync"
)

type Single struct {
	token chan struct{}
}

func NewSingle() *Single {
	l := &Single{
		token: make(chan struct{}, 1),
	}
	l.token <- struct{}{}

	return l
}

// Acquire tries to obtain permission to run.
//
// If no other execution is running, it returns a release function. The caller
// must call the returned function when the work is done.
//
// If another execution is already running, Acquire returns ErrBusy.
//
// If the context is canceled before acquiring, Acquire returns the context error.
func (cg *Single) Acquire(ctx context.Context) (func(), error) {
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("acquire single: %w", ctx.Err())
	case <-cg.token:
		return cg.releaseFunc(), nil
	default:
		return nil, ErrBusy
	}
}

func (cg *Single) releaseFunc() func() {
	once := sync.Once{}

	return func() {
		once.Do(func() {
			cg.token <- struct{}{}
		})
	}
}
