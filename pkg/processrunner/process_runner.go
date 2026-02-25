// Package processrunner provides process lifecycle management:
// start process in its own process group, wait synchronously/asynchronously,
// and terminate the process group on context cancellation with optional grace timeout.
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
	ErrStartCommand       = errors.New("failed to start command")
	ErrInvalidPID         = errors.New("invalid PID")
	ErrProcessGroupSignal = errors.New("failed to send signal to process group")
)

type Process struct {
	cmd     cmdrunner.Command
	runner  cmdrunner.Runner
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

	//nolint:exhaustruct
	cmd.SetSysProcAttr(&syscall.SysProcAttr{
		Setpgid: true,
	})
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
	done := make(chan struct{})

	go func() {
		p.terminateOnContextDone(ctx, done)
	}()

	err := p.cmd.Wait()

	close(done)

	return p.determineExitCodeAndError(ctx, err)
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
		exitCode, finalErr := p.determineExitCodeAndError(ctx, err)

		resultCh <- Result{
			ExitCode: exitCode,
			Err:      finalErr,
		}
	}()

	go func() {
		p.terminateOnContextDone(ctx, done)
	}()

	return resultCh
}

func (p *Process) terminateOnContextDone(ctx context.Context, done <-chan struct{}) {
	select {
	case <-ctx.Done():
		pid := p.cmd.Pid()

		if p.timeout == nil {
			_ = p.signalProcessGroup(pid, syscall.SIGKILL)

			return
		}

		_ = p.signalProcessGroup(pid, syscall.SIGTERM)

		t := time.NewTimer(*p.timeout)
		defer t.Stop()

		select {
		case <-t.C:
			_ = p.signalProcessGroup(pid, syscall.SIGKILL)
		case <-done:
		}

	case <-done:
	}
}

func (p *Process) signalProcessGroup(pid int, sig syscall.Signal) error {
	if pid <= 0 {
		return fmt.Errorf("cannot send %s to process group: pid=%d: %w", sig, pid, ErrInvalidPID)
	}

	pgid := -pid
	if err := p.runner.Kill(pgid, sig); err != nil {
		return fmt.Errorf("failed to send %s to process group %d: %w: %w", sig, pgid, err, ErrProcessGroupSignal)
	}

	return nil
}

func (p *Process) determineExitCodeAndError(ctx context.Context, err error) (int, error) {
	// If the context was canceled or timed out,
	// it means the process was terminated externally.
	if p.isTimeoutOrCanceled(ctx) {
		// Return -1 and the context error.
		//nolint:wrapcheck
		return -1, ctx.Err()
	}

	// If Wait() returned no error,
	// the process exited normally (exit code available in ProcessState).
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
		// On Unix systems, we can check the WaitStatus.
		// If the process was terminated by a signal,
		// it means external intervention (SIGTERM/SIGKILL).
		if status, ok := exitErr.Sys().(syscall.WaitStatus); ok && status.Signaled() {
			return -1, err
		}

		// Otherwise, the process exited normally (even if exit code != 0).
		// In this case, exit code is a valid result and error is nil.
		return exitErr.ExitCode(), nil
	}

	// Any other error from Wait() is treated as an external/infrastructure error.
	return -1, err
}

func (p *Process) isTimeoutOrCanceled(ctx context.Context) bool {
	return ctx.Err() != nil && (errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(ctx.Err(), context.Canceled))
}
