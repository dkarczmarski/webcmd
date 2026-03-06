package callgate

import (
	"context"
	"fmt"
	"sync"
)

type Sequence struct {
	mu    sync.Mutex
	busy  bool
	queue []chan struct{} // FIFO of waiters
}

func NewSequence() *Sequence {
	return &Sequence{} //nolint:exhaustruct
}

// Acquire blocks until the execution slot is available.
//
// This gate allows only one execution at a time.
// Waiters are served in strict FIFO order.
//
// When the slot is acquired, Acquire returns a release function.
// The caller must call release() when the work is done.
//
// If the context is canceled before acquiring the slot, Acquire returns ctx.Err().
func (cg *Sequence) Acquire(ctx context.Context) (func(), error) {
	// Fast path: not busy and no queue => acquire immediately.
	cg.mu.Lock()
	if !cg.busy && len(cg.queue) == 0 {
		cg.busy = true

		cg.mu.Unlock()

		return cg.releaseFunc(), nil
	}

	// Otherwise, enqueue and wait for our turn.
	waiter := make(chan struct{})

	cg.queue = append(cg.queue, waiter)
	cg.mu.Unlock()

	select {
	case <-ctx.Done():
		// Remove ourselves from the queue if we haven't been granted the slot yet.
		cg.mu.Lock()
		removed := cg.removeWaiterLocked(waiter)
		cg.mu.Unlock()

		// If we were NOT removed, it means we were already granted (channel closed)
		// roughly concurrently. In that case, we must return success, not ctx.Err().
		// We detect "granted" by checking removed==false; but there is a race:
		// - if channel closed just before we locked, removeWaiterLocked won't find it.
		// In that case, proceed as acquired.
		if removed {
			return nil, fmt.Errorf("acquire sequence: %w", ctx.Err())
		}

		return cg.releaseFunc(), nil

	case <-waiter:
		// Granted in FIFO order.
		return cg.releaseFunc(), nil
	}
}

func (cg *Sequence) releaseFunc() func() {
	once := sync.Once{}

	return func() {
		once.Do(func() {
			cg.mu.Lock()
			defer cg.mu.Unlock()

			// If someone is waiting, grant the next one in FIFO order.
			if len(cg.queue) > 0 {
				next := cg.queue[0]
				// pop front
				copy(cg.queue[0:], cg.queue[1:])
				cg.queue = cg.queue[:len(cg.queue)-1]

				// Keep busy=true, transfer ownership to the next waiter.
				close(next)

				return
			}

			// No waiters => free the slot.
			cg.busy = false
		})
	}
}

func (cg *Sequence) removeWaiterLocked(waiter chan struct{}) bool {
	for i := range cg.queue {
		if cg.queue[i] == waiter {
			copy(cg.queue[i:], cg.queue[i+1:])
			cg.queue = cg.queue[:len(cg.queue)-1]

			return true
		}
	}

	return false
}
