package gateexec_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dkarczmarski/webcmd/pkg/callgate"
	"github.com/dkarczmarski/webcmd/pkg/config"
	"github.com/dkarczmarski/webcmd/pkg/gateexec"
)

func TestExecutor_Integration_Single_ConcurrentSecondGetsErrBusy(t *testing.T) {
	t.Parallel()

	ctx := t.Context()

	// Real registry with default gates.
	registry := callgate.NewRegistry(callgate.WithDefaults())
	exec := gateexec.New(registry)

	cfg := &config.CallGateConfig{
		Mode:      "single",
		GroupName: strPtr("G"),
	}

	firstStarted := make(chan struct{})
	firstDone := make(chan struct{})

	// Action #1 acquires the slot and holds it until firstDone is closed.
	action1 := func(context.Context) (int, <-chan struct{}, error) { //nolint:unparam
		close(firstStarted)

		return 1, firstDone, nil
	}

	// Action #2 should not run; it should fail with ErrBusy at Acquire time.
	action2Called := make(chan struct{}, 1)
	action2 := func(context.Context) (int, <-chan struct{}, error) {
		action2Called <- struct{}{}

		return 2, nil, nil
	}

	// Start action1 and wait until it definitely holds the slot.
	errCh1 := make(chan error, 1)
	go func() {
		_, err := exec.Run(ctx, cfg, "DEFAULT", action1)
		errCh1 <- err
	}()

	waitClosed(t, firstStarted, 700*time.Millisecond, "firstStarted")

	// Now attempt to run action2 while action1 is still holding the slot.
	exit2, err2 := exec.Run(ctx, cfg, "DEFAULT", action2)

	// Ensure action2 wasn't called (Acquire should fail before action is executed).
	select {
	case <-action2Called:
		t.Fatalf("action2 should not be called when gate is busy")
	default:
	}

	// gateexec should report a pre-action error and include ErrBusy.
	if exit2 != -1 {
		t.Fatalf("expected exit=-1 for pre-action error, got %d", exit2)
	}

	if err2 == nil {
		t.Fatalf("expected non-nil error")
	}

	if !errors.Is(err2, gateexec.ErrPreAction) {
		t.Fatalf("expected errors.Is(err, gateexec.ErrPreAction)=true, err=%v", err2)
	}

	if !errors.Is(err2, callgate.ErrBusy) {
		t.Fatalf("expected errors.Is(err, callgate.ErrBusy)=true, err=%v", err2)
	}

	// Now release action1 and ensure it completes without error.
	close(firstDone)

	select {
	case err1 := <-errCh1:
		if err1 != nil {
			t.Fatalf("action1 Run returned unexpected error: %v", err1)
		}
	case <-time.After(700 * time.Millisecond):
		t.Fatalf("timeout waiting for action1 Run to finish")
	}
}

func TestExecutor_Integration_Sequence_SerializesAndMakesProgress(t *testing.T) {
	t.Parallel()

	const (
		N       = 50
		timeout = 700 * time.Millisecond
	)

	ctx := t.Context()

	// Real registry with default gates (production-like).
	registry := callgate.NewRegistry(callgate.WithDefaults())
	exec := gateexec.New(registry)

	cfg := &config.CallGateConfig{
		Mode:      "sequence",
		GroupName: strPtr("G"),
	}

	started := make(chan int, N)
	errCh := make(chan error, N)

	// Each action will block until its done[i] is closed.
	done := make([]chan struct{}, N)
	for i := range N {
		done[i] = make(chan struct{})

		//nolint:unparam
		action := func(context.Context) (int, <-chan struct{}, error) {
			// If gate is correct, this should happen strictly one-at-a-time.
			started <- i

			return i, done[i], nil
		}

		go func() {
			_, err := exec.Run(ctx, cfg, "DEFAULT", action)
			errCh <- err
		}()
	}

	// Wait for the first action to enter.
	var current int
	select {
	case current = <-started:
	case <-time.After(timeout):
		t.Fatalf("timeout waiting for first action to start")
	}

	seen := make(map[int]bool, N)
	seen[current] = true

	// While current is holding the slot, nobody else should enter.
	select {
	case v := <-started:
		t.Fatalf("unexpected concurrent entry (sequence broken): %d", v)
	default: // OK
	}

	// Now release actions one by one. Each release should allow exactly one next entry.
	for step := 1; step < N; step++ {
		close(done[current])

		select {
		case next := <-started:
			if seen[next] {
				t.Fatalf("action %d started more than once", next)
			}

			seen[next] = true
			current = next
		case <-time.After(timeout):
			t.Fatalf("timeout waiting for next action to start (step=%d)", step)
		}

		// Again: still only one action can be in at a time.
		select {
		case v := <-started:
			t.Fatalf("unexpected concurrent entry (sequence broken): %d", v)
		default: // OK
		}
	}

	// Release the last one.
	close(done[current])

	// All Run() calls should complete with nil error.
	for range N {
		select {
		case err := <-errCh:
			if err != nil {
				t.Fatalf("Run returned unexpected error: %v", err)
			}
		case <-time.After(timeout):
			t.Fatalf("timeout waiting for Run results")
		}
	}

	// Sanity: everyone entered exactly once.
	if len(seen) != N {
		t.Fatalf("expected %d actions to start, got %d", N, len(seen))
	}
}

func waitClosed(t *testing.T, ch <-chan struct{}, timeout time.Duration, name string) {
	t.Helper()
	select {
	case <-ch:
		return
	case <-time.After(timeout):
		t.Fatalf("timeout waiting for %s (%s)", name, timeout)
	}
}
