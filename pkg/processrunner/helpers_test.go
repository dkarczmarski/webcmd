package processrunner_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/dkarczmarski/webcmd/pkg/cmdrunner"
	"github.com/dkarczmarski/webcmd/pkg/processrunner"
)

// syncBuffer is a thread-safe bytes.Buffer.
// We need it because the process writes stdout/stderr concurrently.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

//nolint:wrapcheck
func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.b.String()
}

func mustStartShell(
	t *testing.T,
	runner cmdrunner.Runner,
	w *syncBuffer,
	grace *time.Duration,
	opts []processrunner.Option,
	script string,
) *processrunner.Process {
	t.Helper()

	p, err := processrunner.StartProcess(
		runner,
		"sh",
		[]string{"-c", script},
		w,
		grace,
		opts...,
	)
	if err != nil {
		t.Fatalf("StartProcess failed: %v", err)
	}

	return p
}

func waitForSubstring(t *testing.T, w *syncBuffer, sub string, timeout time.Duration) {
	t.Helper()

	// Polling is intentional: stdout/stderr is asynchronous and ordering is not guaranteed.
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if strings.Contains(w.String(), sub) {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("timeout waiting for substring %q in output. got=%q", sub, w.String())
}

func waitSyncWithTimeout(
	t *testing.T,
	ctx context.Context, //nolint:revive
	p *processrunner.Process,
	timeout time.Duration, //nolint:unparam
) (int, error) {
	t.Helper()

	// WaitSync is blocking; run it in a goroutine so we can enforce an upper bound.
	type res struct {
		code int
		err  error
	}

	ch := make(chan res, 1)

	go func() {
		code, err := p.WaitSync(ctx)
		ch <- res{code: code, err: err}
	}()

	select {
	case r := <-ch:
		return r.code, r.err
	case <-time.After(timeout):
		t.Fatalf("WaitSync timed out after %s", timeout)

		// Unreachable because Fatalf stops the test, but required for compilation.
		return 0, errors.New("unreachable")
	}
}

func waitForProcessGone(t *testing.T, pid int, timeout time.Duration) {
	t.Helper()

	// kill(pid, 0) is an existence check.
	// We poll because the process may briefly remain as a zombie / not yet be reaped.
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		err := syscall.Kill(pid, 0)
		if err != nil && errors.Is(err, syscall.ESRCH) {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	// One last check for a better error message.
	err := syscall.Kill(pid, 0)
	if err == nil {
		t.Fatalf("expected process %d to be gone, but kill(0) still succeeds", pid)
	}

	t.Fatalf("expected process %d to be gone; last kill(0) err=%v", pid, err)
}
