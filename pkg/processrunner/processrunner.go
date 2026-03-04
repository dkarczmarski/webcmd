// Package processrunner provides process lifecycle management:
//   - starts a process in its own process group,
//   - allows synchronous and asynchronous waiting,
//   - and terminates the whole process group on context cancellation
//     with an optional grace timeout (SIGTERM -> SIGKILL).
package processrunner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"syscall"
	"time"

	"github.com/dkarczmarski/webcmd/pkg/cmdrunner"
)

var (
	// ErrStartCommand indicates that the underlying command failed to start.
	ErrStartCommand = errors.New("failed to start command")

	// ErrInvalidPID indicates that an invalid PID was used when attempting to signal.
	ErrInvalidPID = errors.New("invalid PID")

	// ErrProcessGroupSignal indicates failure when sending a signal to a process group.
	ErrProcessGroupSignal = errors.New("failed to send signal to process group")
)

type Process struct {
	// cmd is the underlying command abstraction.
	cmd cmdrunner.Command

	// runner is used to send signals (e.g., kill process group).
	runner cmdrunner.Runner

	// timeout defines how long to wait after SIGTERM before sending SIGKILL.
	// If nil, the process group is killed immediately with SIGKILL.
	timeout *time.Duration
}

func StartProcess(
	runner cmdrunner.Runner,
	command string,
	args []string,
	writer io.Writer,
	graceTimeout *time.Duration,
) (*Process, error) {
	cmd := runner.Command(command, args...)

	// Ensure the command runs in its own process group.
	// This allows signaling the entire group (including children)
	// by sending a signal to -pid.
	//nolint:exhaustruct
	cmd.SetSysProcAttr(&syscall.SysProcAttr{
		Setpgid: true,
	})

	// Redirect both stdout and stderr to the provided writer.
	cmd.SetStdout(writer)
	cmd.SetStderr(writer)

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrStartCommand, err)
	}

	return &Process{
		cmd:     cmd,
		runner:  runner,
		timeout: graceTimeout,
	}, nil
}

func (p *Process) WaitSync(ctx context.Context) (int, error) {
	// done is closed when Wait() finishes.
	// It is used to coordinate with terminateOnContextDone
	// to avoid sending signals after the process already exited.
	done := make(chan struct{})
	defer close(done)

	// Start a goroutine that listens for context cancellation
	// and attempts to terminate the process group if needed.
	go func() {
		p.terminateOnContextDone(ctx, done)
	}()

	// Wait blocks until the process exits.
	// According to the intended semantics, the result of Wait()
	// has priority over a later ctx.Done().
	err := p.cmd.Wait()

	return p.exitFromWaitError(err)
}

type Result struct {
	ExitCode int
	Err      error
}

// WaitAsync starts waiting in background and returns a channel that receives exactly one Result.
func (p *Process) WaitAsync(ctx context.Context) <-chan Result {
	resultCh := make(chan Result, 1)
	done := make(chan struct{})

	go func() {
		defer close(done)
		defer close(resultCh)

		err := p.cmd.Wait()
		exitCode, finalErr := p.exitFromWaitError(err)

		resultCh <- Result{ExitCode: exitCode, Err: finalErr}
	}()

	go func() {
		p.terminateOnContextDone(ctx, done)
	}()

	return resultCh
}

func (p *Process) terminateOnContextDone(ctx context.Context, done <-chan struct{}) {
	select {
	case <-ctx.Done():
		// If Wait() has already completed (done is closed),
		// do not attempt to signal the process group.
		// The result from Wait() is considered authoritative.
		select {
		case <-done:
			return
		default:
		}

		pid := p.cmd.Pid()
		if pid <= 0 {
			// Invalid PID: nothing to signal.
			return
		}

		// If no grace timeout is defined,
		// immediately kill the entire process group.
		if p.timeout == nil {
			_ = p.signalProcessGroup(pid, syscall.SIGKILL)

			return
		}

		// First attempt graceful shutdown with SIGTERM.
		_ = p.signalProcessGroup(pid, syscall.SIGTERM)

		// Wait for either:
		// - the grace timeout to expire (then send SIGKILL),
		// - or the process to exit naturally (done closed).
		t := time.NewTimer(*p.timeout)
		defer t.Stop()

		select {
		case <-t.C:
			_ = p.signalProcessGroup(pid, syscall.SIGKILL)
		case <-done: // Process exited during grace period.
		}

	case <-done: // Process exited before context cancellation.
		return
	}
}

func (p *Process) signalProcessGroup(pid int, sig syscall.Signal) error {
	if pid <= 0 {
		return fmt.Errorf("cannot send %s to process group: pid=%d: %w", sig, pid, ErrInvalidPID)
	}

	// Negative PID means: send signal to the process group
	// whose PGID equals the absolute value of pid.
	pgid := -pid

	if err := p.runner.Kill(pgid, sig); err != nil {
		return fmt.Errorf(
			"failed to send %s to process group %d: %w: %w",
			sig,
			pgid,
			err,
			ErrProcessGroupSignal,
		)
	}

	return nil
}

func (p *Process) exitFromWaitError(err error) (int, error) {
	// No error from Wait(): process exited normally.
	if err == nil {
		if ps := p.cmd.ProcessState(); ps != nil {
			return ps.ExitCode(), nil
		}

		// This should not normally happen after Wait(),
		// but return 0 as a safe default.
		return 0, nil
	}

	// If the error is *exec.ExitError,
	// the process exited by itself (possibly with non-zero exit code)
	// OR it was terminated by a signal.
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		// On Unix systems, WaitStatus allows distinguishing
		// between normal exit and signal-based termination.
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			// Terminated by a signal (e.g., SIGTERM/SIGKILL).
			return -1, err
		}

		// Exited normally with a non-zero exit code.
		// In this case, the exit code is returned and error is nil.
		return exitErr.ExitCode(), nil
	}

	// Any other error from Wait() is treated as an infrastructure/runtime error.
	return -1, err
}

// Pid returns PID of the underlying process (0 if not started / unknown).
func (p *Process) Pid() int {
	if p == nil || p.cmd == nil {
		return 0
	}

	return p.cmd.Pid()
}

// ProcessGroupID returns PGID (on Unix). On error returns 0 and error.
func (p *Process) ProcessGroupID() (int, error) {
	pid := p.Pid()
	if pid <= 0 {
		return 0, ErrInvalidPID
	}

	//nolint: wrapcheck
	return syscall.Getpgid(pid)
}
