package callgate_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/dkarczmarski/webcmd/pkg/callgate"
)

func TestSequence_Acquire_Release_AllowsReuse(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	g := callgate.NewSequence()

	release1, err := g.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire #1 error: %v", err)
	}

	release1()

	release2, err := g.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire #2 error: %v", err)
	}

	release2()
}

func TestSequence_Acquire_BlocksUntilRelease(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	g := callgate.NewSequence()

	release1, err := g.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire #1 error: %v", err)
	}

	acquired2 := make(chan struct{})

	var (
		release2 func()
		err2     error
	)

	go func() {
		release2, err2 = g.Acquire(ctx)

		close(acquired2)
	}()

	select {
	case <-acquired2:
		t.Fatalf("Acquire #2 unexpectedly returned before release #1 (err=%v)", err2)
	case <-time.After(50 * time.Millisecond):
	}

	release1()

	select {
	case <-acquired2:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("Acquire #2 did not return after release #1")
	}

	if err2 != nil {
		t.Fatalf("Acquire #2 error: %v", err2)
	}

	if release2 == nil {
		t.Fatalf("Acquire #2 returned nil release")
	}

	release2()
}

func TestSequence_Acquire_ContextCanceledBeforeAcquire(t *testing.T) {
	t.Parallel()

	root := t.Context()

	g := callgate.NewSequence()

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
}

func TestSequence_Acquire_ContextDeadlineWhileWaiting(t *testing.T) {
	t.Parallel()

	root := t.Context()

	g := callgate.NewSequence()

	release1, err := g.Acquire(root)
	if err != nil {
		t.Fatalf("Acquire #1 error: %v", err)
	}

	defer release1()

	ctx, cancel := context.WithTimeout(root, 50*time.Millisecond)
	defer cancel()

	start := time.Now()
	release2, err := g.Acquire(ctx)
	elapsed := time.Since(start)

	if release2 != nil {
		t.Fatalf("expected nil release on error, got non-nil")
	}

	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected error to wrap context.DeadlineExceeded, got: %v", err)
	}

	if elapsed < 10*time.Millisecond {
		t.Fatalf("Acquire returned too quickly (%s), expected it to wait for ctx deadline", elapsed)
	}
}

func TestSequence_Release_IsIdempotent(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	g := callgate.NewSequence()

	release1, err := g.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire #1 error: %v", err)
	}

	release1()
	release1()
	release1()

	release2, err := g.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire #2 error: %v", err)
	}

	acquired3 := make(chan struct{})

	var err3 error

	go func() {
		_, err3 = g.Acquire(ctx)

		close(acquired3)
	}()

	select {
	case <-acquired3:
		t.Fatalf("Acquire #3 unexpectedly returned early (err=%v); token may have been released multiple times", err3)
	case <-time.After(50 * time.Millisecond):
	}

	release2()

	select {
	case <-acquired3:
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("Acquire #3 did not return after release #2")
	}
}

func TestSequence_SerializesCriticalSection_NoOverlap(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	g := callgate.NewSequence()

	const n = 20

	var (
		inCS    int
		inCSMu  sync.Mutex
		wg      sync.WaitGroup
		maxSeen int
	)

	wg.Add(n)

	for range n {
		go func() {
			defer wg.Done()

			release, err := g.Acquire(ctx)
			if err != nil {
				t.Errorf("Acquire error: %v", err)

				return
			}

			defer release()

			inCSMu.Lock()
			inCS++

			if inCS > maxSeen {
				maxSeen = inCS
			}

			inCSMu.Unlock()

			time.Sleep(5 * time.Millisecond)

			inCSMu.Lock()
			inCS--
			inCSMu.Unlock()
		}()
	}

	wg.Wait()

	if maxSeen != 1 {
		t.Fatalf("critical section overlapped: max concurrent inCS = %d, want 1", maxSeen)
	}
}
