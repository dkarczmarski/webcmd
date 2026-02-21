package callgate

import (
	"context"
	"fmt"
	"sync"
)

type Sequence struct {
	token chan struct{}
}

func NewSequence() *Sequence {
	l := &Sequence{
		token: make(chan struct{}, 1),
	}
	l.token <- struct{}{}

	return l
}

// Acquire blocks until the execution slot is available.
//
// This gate allows only one execution at a time. Calls are processed in sequence:
// if one is running, the next call waits until it completes.
//
// When the slot is acquired, Acquire returns a release function. The caller must
// call release() when the work is done.
//
// If the context is canceled before acquiring the slot, Acquire returns ctx.Err().
func (cg *Sequence) Acquire(ctx context.Context) (func(), error) {
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("acquire sequence: %w", ctx.Err())
	case <-cg.token:
		return cg.releaseFunc(), nil
	}
}

func (cg *Sequence) releaseFunc() func() {
	once := sync.Once{}

	return func() {
		once.Do(func() {
			cg.token <- struct{}{}
		})
	}
}
