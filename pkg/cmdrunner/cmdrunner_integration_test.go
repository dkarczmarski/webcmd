//go:build integration

//nolint:paralleltest
package cmdrunner_test

import (
	"bytes"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/dkarczmarski/webcmd/pkg/cmdrunner"
)

const (
	shortTimeout = 2 * time.Second
	longTimeout  = 5 * time.Second
)

func TestSuccessfulCommandExitCodeZero(t *testing.T) {
	r := &cmdrunner.RealRunner{}

	c := startShellCommand(t, r, "echo hello")
	if err := waitWithTimeout(t, c, shortTimeout); err != nil {
		t.Fatalf("Wait() returned error for successful command: %v", err)
	}

	ps := c.ProcessState()
	if ps == nil {
		t.Fatalf("expected ProcessState() to be non-nil after Wait()")
	}

	if got := ps.ExitCode(); got != 0 {
		t.Fatalf("expected exit code 0, got %d", got)
	}
}

func TestFailingCommandExitCodeNonZero(t *testing.T) {
	r := &cmdrunner.RealRunner{}

	c := startShellCommand(t, r, "exit 42")
	err := waitWithTimeout(t, c, shortTimeout)

	if err == nil {
		t.Fatalf("expected Wait() to return error for non-zero exit")
	}

	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		t.Fatalf("expected *exec.ExitError, got %T: %v", err, err)
	}

	ps := c.ProcessState()
	if ps == nil {
		t.Fatalf("expected ProcessState() to be non-nil after Wait()")
	}

	if got := ps.ExitCode(); got != 42 {
		t.Fatalf("expected exit code 42, got %d", got)
	}
}

func TestStartNonExistingBinaryReturnsError(t *testing.T) {
	r := &cmdrunner.RealRunner{}

	c := r.Command("__not_a_cmd__")
	if err := c.Start(); err == nil {
		t.Fatalf("expected Start() to fail for non-existing binary")
	}

	// Pid() should remain 0 because the process was never started.
	if got := c.Pid(); got != 0 {
		t.Fatalf("expected Pid() == 0 when Start() fails, got %d", got)
	}
}

func TestStdoutRedirectionWorks(t *testing.T) {
	r := &cmdrunner.RealRunner{}

	var out bytes.Buffer

	c := r.Command("sh", "-c", "printf 'abc'")
	c.SetStdout(&out)

	if err := c.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	if err := waitWithTimeout(t, c, shortTimeout); err != nil {
		t.Fatalf("Wait() failed: %v", err)
	}

	if got := out.String(); got != "abc" {
		t.Fatalf("expected stdout %q, got %q", "abc", got)
	}
}

func TestStderrRedirectionWorks(t *testing.T) {
	r := &cmdrunner.RealRunner{}

	var errBuf bytes.Buffer

	c := r.Command("sh", "-c", "printf 'err' 1>&2")

	c.SetStderr(&errBuf)

	if err := c.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	if err := waitWithTimeout(t, c, shortTimeout); err != nil {
		t.Fatalf("Wait() failed: %v", err)
	}

	if got := errBuf.String(); got != "err" {
		t.Fatalf("expected stderr %q, got %q", "err", got)
	}
}

func TestCombinedStdoutAndStderrToSingleWriter(t *testing.T) {
	r := &cmdrunner.RealRunner{}

	var combined bytes.Buffer

	c := r.Command("sh", "-c", "echo out; echo err 1>&2")

	c.SetStdout(&combined)
	c.SetStderr(&combined)

	if err := c.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	if err := waitWithTimeout(t, c, shortTimeout); err != nil {
		t.Fatalf("Wait() failed: %v", err)
	}

	got := combined.String()
	// Order is not guaranteed; just verify both are present.
	if !strings.Contains(got, "out") || !strings.Contains(got, "err") {
		t.Fatalf("expected combined output to contain both 'out' and 'err', got %q", got)
	}
}

func TestKillProcessByPidSIGTERM(t *testing.T) {
	r := &cmdrunner.RealRunner{}

	c := startShellCommand(t, r, "sleep 30")
	pid := c.Pid()

	if pid <= 0 {
		t.Fatalf("expected valid pid, got %d", pid)
	}

	if err := r.Kill(pid, syscall.SIGTERM); err != nil {
		t.Fatalf("Kill(pid, SIGTERM) failed: %v", err)
	}

	// Process should exit quickly after SIGTERM.
	_ = waitWithTimeout(t, c, longTimeout)

	// We avoid asserting a specific exit code because shells/wrappers can map signals differently.

	ps := c.ProcessState()
	if ps == nil {
		t.Fatalf("expected ProcessState() to be non-nil after Wait()")
	}
}

func TestKillProcessGroupByNegativePidSIGTERM(t *testing.T) {
	r := &cmdrunner.RealRunner{}

	// This command creates a new process group so that killing -pid targets the group.
	// The script starts a background sleep plus a foreground sleep.
	c := r.Command("sh", "-c", "(sleep 30) & sleep 30")
	c.SetSysProcAttr(&syscall.SysProcAttr{Setpgid: true})

	if err := c.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	pid := c.Pid()
	if pid <= 0 {
		t.Fatalf("expected valid pid, got %d", pid)
	}

	// Give the shell a moment to spawn the background process.
	time.Sleep(150 * time.Millisecond)

	// Kill the entire process group: pid < 0 targets process group id = -pid.
	if err := r.Kill(-pid, syscall.SIGTERM); err != nil {
		t.Fatalf("Kill(-pid, SIGTERM) failed: %v", err)
	}

	// The group should terminate quickly.
	_ = waitWithTimeout(t, c, longTimeout)

	ps := c.ProcessState()
	if ps == nil {
		t.Fatalf("expected ProcessState() to be non-nil after Wait()")
	}
}

func TestKillAfterProcessExitReturnsESRCH(t *testing.T) {
	r := &cmdrunner.RealRunner{}

	c := startShellCommand(t, r, "true")
	pid := c.Pid()

	if pid <= 0 {
		t.Fatalf("expected valid pid, got %d", pid)
	}

	if err := waitWithTimeout(t, c, shortTimeout); err != nil {
		t.Fatalf("Wait() failed: %v", err)
	}

	err := r.Kill(pid, syscall.SIGTERM)
	if err == nil {
		t.Fatalf("expected Kill() to fail for already-exited process")
	}

	// On Linux, killing a non-existing pid typically returns ESRCH.
	if !errors.Is(err, syscall.ESRCH) {
		t.Fatalf("expected ESRCH, got %T: %v", err, err)
	}
}

func TestParallelStartsAndWaits(t *testing.T) {
	r := &cmdrunner.RealRunner{}

	const n = 10

	var wg sync.WaitGroup

	wg.Add(n)

	errCh := make(chan error, n)

	for i := range n {
		go func(i int) {
			defer wg.Done()

			// Each command prints a unique token.
			script := fmt.Sprintf("echo token_%d", i)
			c := r.Command("sh", "-c", script)

			var out bytes.Buffer

			c.SetStdout(&out)

			if err := c.Start(); err != nil {
				errCh <- fmt.Errorf("function Start() failed (i=%d): %w", i, err)

				return
			}

			if c.Pid() <= 0 {
				errCh <- fmt.Errorf("invalid pid (i=%d): %d", i, c.Pid())

				return
			}

			if err := waitWithTimeout(t, c, shortTimeout); err != nil {
				errCh <- fmt.Errorf("function Wait() failed (i=%d): %w", i, err)

				return
			}

			if !strings.Contains(out.String(), fmt.Sprintf("token_%d", i)) {
				errCh <- fmt.Errorf("unexpected stdout (i=%d): %q", i, out.String())

				return
			}
		}(i)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Error(err)
	}
}

func TestPidIsZeroBeforeStartAndNonZeroAfterStart(t *testing.T) {
	r := &cmdrunner.RealRunner{}

	c := r.Command("sh", "-c", "sleep 0.2")
	if got := c.Pid(); got != 0 {
		t.Fatalf("expected Pid() == 0 before Start(), got %d", got)
	}

	if err := c.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	if got := c.Pid(); got <= 0 {
		t.Fatalf("expected Pid() > 0 after Start(), got %d", got)
	}

	_ = waitWithTimeout(t, c, shortTimeout)
}

func TestProcessStateNilBeforeWaitNonNilAfterWait(t *testing.T) {
	r := &cmdrunner.RealRunner{}

	c := r.Command("sh", "-c", "sleep 0.2")

	// Before Start(), ProcessState should be nil.
	if ps := c.ProcessState(); ps != nil {
		t.Fatalf("expected ProcessState() == nil before Start(), got %+v", ps)
	}

	if err := c.Start(); err != nil {
		t.Fatalf("Start() failed: %v", err)
	}

	// After Start() but before Wait(), ProcessState is typically still nil.
	if ps := c.ProcessState(); ps != nil {
		// We keep this as a strict assertion to catch unexpected behavior changes.
		t.Fatalf("expected ProcessState() == nil before Wait(), got %+v", ps)
	}

	if err := waitWithTimeout(t, c, shortTimeout); err != nil {
		t.Fatalf("Wait() failed: %v", err)
	}

	if ps := c.ProcessState(); ps == nil {
		t.Fatalf("expected ProcessState() to be non-nil after Wait()")
	}
}

// startShellCommand creates and starts: sh -c <script>.
func startShellCommand(t *testing.T, r cmdrunner.Runner, script string) cmdrunner.Command {
	t.Helper()

	c := r.Command("sh", "-c", script)
	if err := c.Start(); err != nil {
		t.Fatalf("Start() failed: %v (script=%q)", err, script)
	}

	return c
}

// waitWithTimeout waits for cmd.Wait() with a timeout to avoid hanging tests.
func waitWithTimeout(t *testing.T, c cmdrunner.Command, timeout time.Duration) error {
	t.Helper()

	errCh := make(chan error, 1)
	go func() {
		errCh <- c.Wait()
	}()

	select {
	case err := <-errCh:
		return err
	case <-time.After(timeout):
		t.Fatalf("command did not exit within %s", timeout)

		return nil
	}
}
