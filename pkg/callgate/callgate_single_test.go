package callgate_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/dkarczmarski/webcmd/pkg/callgate"
)

func TestSingle_Acquire_Release_AllowsReuse(t *testing.T) {
	t.Parallel()

	g := callgate.NewSingle()

	ctx := t.Context()

	release1, err := g.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire #1 error: %v", err)
	}

	if release1 == nil {
		t.Fatalf("Acquire #1 returned nil release")
	}

	release1()

	release2, err := g.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire #2 error: %v", err)
	}

	if release2 == nil {
		t.Fatalf("Acquire #2 returned nil release")
	}

	release2()
}

func TestSingle_Acquire_WhenBusy_ReturnsErrBusy(t *testing.T) {
	t.Parallel()

	g := callgate.NewSingle()

	ctx := t.Context()

	release1, err := g.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire #1 error: %v", err)
	}

	defer release1()

	release2, err := g.Acquire(ctx)
	if release2 != nil {
		t.Fatalf("expected nil release when busy, got non-nil")
	}

	if err == nil {
		t.Fatalf("expected ErrBusy, got nil")
	}

	if !errors.Is(err, callgate.ErrBusy) {
		t.Fatalf("expected error to be ErrBusy, got: %v", err)
	}
}

func TestSingle_Acquire_ContextCanceled_TakesPrecedenceOverBusy(t *testing.T) {
	t.Parallel()

	g := callgate.NewSingle()

	root := t.Context()

	release1, err := g.Acquire(root)
	if err != nil {
		t.Fatalf("Acquire #1 error: %v", err)
	}

	defer release1()

	ctx, cancel := context.WithCancel(root)
	cancel()

	release2, err := g.Acquire(ctx)
	if release2 != nil {
		t.Fatalf("expected nil release on error, got non-nil")
	}

	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected error to wrap context.Canceled, got: %v", err)
	}

	if errors.Is(err, callgate.ErrBusy) {
		t.Fatalf("did not expect ErrBusy when ctx is canceled, got: %v", err)
	}
}

func TestSingle_Release_IsIdempotent(t *testing.T) {
	t.Parallel()

	g := callgate.NewSingle()

	ctx := t.Context()

	release1, err := g.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire error: %v", err)
	}

	release1()
	release1()
	release1()

	release2, err := g.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire after repeated release error: %v", err)
	}

	release2()
}

func TestSingle_ConcurrentAcquire_OnlyOneSucceedsWhileHeld(t *testing.T) {
	t.Parallel()

	g := callgate.NewSingle()

	ctx := t.Context()

	release, err := g.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire holder error: %v", err)
	}
	defer release()

	const n = 50

	var wg sync.WaitGroup

	var (
		successes   int32
		busyCount   int32
		otherErrors int32
	)

	wg.Add(n)

	for range n {
		go func() {
			defer wg.Done()

			r, e := g.Acquire(ctx)
			if e == nil {
				atomic.AddInt32(&successes, 1)

				r()

				return
			}

			if errors.Is(e, callgate.ErrBusy) {
				atomic.AddInt32(&busyCount, 1)

				return
			}

			atomic.AddInt32(&otherErrors, 1)
		}()
	}

	wg.Wait()

	if successes != 0 {
		t.Fatalf("expected 0 successes while held, got %d", successes)
	}

	if otherErrors != 0 {
		t.Fatalf("expected 0 other errors, got %d", otherErrors)
	}

	if busyCount != n {
		t.Fatalf("expected %d ErrBusy, got %d", n, busyCount)
	}
}
