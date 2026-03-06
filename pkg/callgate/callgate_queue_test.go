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

// FIFO / fairness: waiters should be granted in the same order they started waiting.
func TestSequence_Acquire_FIFOOrder(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	g := callgate.NewSequence()

	release1, err := g.Acquire(ctx)
	if err != nil {
		t.Fatalf("Acquire #1 error: %v", err)
	}

	const n = 500
	orderCh := make(chan int, n)

	var (
		releases [n]func()
		errs     [n]error
	)

	// Start goroutines in a known order.
	// Small sleeps help ensure they enqueue in that same order (keeps the test stable).
	for i := range n {
		go func(i int) {
			releases[i], errs[i] = g.Acquire(ctx)
			orderCh <- i
		}(i)

		time.Sleep(10 * time.Millisecond)
	}

	// Let the first waiter through.
	release1()

	// We expect acquisition order in strict FIFO.
	for want := range n {
		select {
		case got := <-orderCh:
			if got != want {
				t.Fatalf("FIFO violated: got=%d, want=%d", got, want)
			}

			if errs[got] != nil {
				t.Fatalf("Acquire waiter #%d error: %v", got, errs[got])
			}

			if releases[got] == nil {
				t.Fatalf("Acquire waiter #%d returned nil release", got)
			}

			// Release to allow the next waiter.
			releases[got]()

		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for waiter #%d to acquire", want)
		}
	}
}

// Race: ctx cancel vs grant happening "at the same time".
// Invariant:
//   - If Acquire returns error => release must be nil.
//   - If Acquire returns success => it must truly own the slot (next Acquire blocks until release()).
func TestSequence_Acquire_CancelVsGrantRace(t *testing.T) {
	t.Parallel()

	root := t.Context()
	g := callgate.NewSequence()

	const iters = 500

	for iter := range iters {
		// Hold the slot.
		release1, err := g.Acquire(root)
		if err != nil {
			t.Fatalf("iter %d: Acquire #1 error: %v", iter, err)
		}

		ctx, cancel := context.WithCancel(root)

		var (
			release2 func()
			err2     error
		)

		done := make(chan struct{})

		go func() {
			release2, err2 = g.Acquire(ctx)

			close(done)
		}()

		// Give the waiter a moment to enqueue.
		time.Sleep(1 * time.Millisecond)

		// Try to make cancel and release happen as simultaneously as possible.
		start := make(chan struct{})

		var wg sync.WaitGroup

		wg.Add(2)

		go func() {
			defer wg.Done()
			<-start
			cancel()
		}()
		go func() {
			defer wg.Done()
			<-start
			release1()
		}()

		close(start)
		wg.Wait()

		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("iter %d: waiter did not finish", iter)
		}

		if err2 != nil {
			// If canceled "won", must be an error and release must be nil.
			if release2 != nil {
				t.Fatalf("iter %d: expected nil release on error, got non-nil", iter)
			}

			if !errors.Is(err2, context.Canceled) && !errors.Is(err2, context.DeadlineExceeded) {
				t.Fatalf("iter %d: expected ctx error, got: %v", iter, err2)
			}

			continue
		}

		// Success path: must have a release func.
		if release2 == nil {
			t.Fatalf("iter %d: Acquire succeeded but release is nil", iter)
		}

		// Verify it *really* owns the slot: a third Acquire should block until release2().
		acquired3 := make(chan struct{})
		go func() {
			rel3, err3 := g.Acquire(root)
			if err3 == nil && rel3 != nil {
				rel3()
			}

			close(acquired3)
		}()

		select {
		case <-acquired3:
			// If it returned immediately, then release2 didn't actually own the slot.
			t.Fatalf("iter %d: Acquire #3 returned early; release2 likely didn't own the slot", iter)
		case <-time.After(30 * time.Millisecond):
		}

		release2()

		select {
		case <-acquired3:
		case <-time.After(2 * time.Second):
			t.Fatalf("iter %d: Acquire #3 did not return after release2()", iter)
		}
	}
}

// A canceled waiter should be removed from the queue and not block later waiters.
// Scenario:
//   - Hold slot with #1.
//   - Waiter #2 times out while waiting (must return error).
//   - Waiter #3 waits normally.
//   - Release #1: waiter #3 must acquire (i.e., canceled waiter must not "sit" at the head forever).
func TestSequence_Acquire_CanceledWaiterDoesNotBlockQueue(t *testing.T) {
	t.Parallel()

	root := t.Context()
	g := callgate.NewSequence()

	release1, err := g.Acquire(root)
	if err != nil {
		t.Fatalf("Acquire #1 error: %v", err)
	}
	defer release1()

	// Waiter #2: should time out while waiting.
	ctx2, cancel2 := context.WithTimeout(root, 30*time.Millisecond)
	defer cancel2()

	done2 := make(chan struct{})

	var (
		release2 func()
		err2     error
	)

	go func() {
		release2, err2 = g.Acquire(ctx2)

		close(done2)
	}()

	select {
	case <-done2:
	case <-time.After(2 * time.Second):
		t.Fatalf("waiter #2 did not return")
	}

	if release2 != nil {
		t.Fatalf("expected nil release for waiter #2, got non-nil")
	}

	if err2 == nil {
		t.Fatalf("expected error for waiter #2, got nil")
	}

	if !errors.Is(err2, context.DeadlineExceeded) {
		t.Fatalf("expected waiter #2 to wrap context.DeadlineExceeded, got: %v", err2)
	}

	// Waiter #3: should be able to acquire once #1 releases.
	acquired3 := make(chan struct{})

	var (
		release3 func()
		err3     error
	)

	go func() {
		release3, err3 = g.Acquire(root)

		close(acquired3)
	}()

	// Should still be blocked because #1 is held.
	select {
	case <-acquired3:
		t.Fatalf("Acquire #3 unexpectedly returned early (err=%v)", err3)
	case <-time.After(30 * time.Millisecond):
	}

	// Now release #1: #3 must get it (canceled #2 must not block).
	release1()

	select {
	case <-acquired3:
	case <-time.After(2 * time.Second):
		t.Fatalf("Acquire #3 did not return after release #1")
	}

	if err3 != nil {
		t.Fatalf("Acquire #3 error: %v", err3)
	}

	if release3 == nil {
		t.Fatalf("Acquire #3 returned nil release")
	}

	release3()
}
