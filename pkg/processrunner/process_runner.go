package processrunner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os/exec"
	"syscall"
	"time"

	"github.com/dkarczmarski/webcmd/pkg/cmdrunner"
)

type contextKey string

const RequestIDKey contextKey = "requestID"

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
		return nil, fmt.Errorf("failed to start command: %w", err)
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

func (p *Process) WaitAsync(ctx context.Context) {
	rid := requestIDFromContext(ctx)
	done := make(chan struct{})

	go func() {
		log.Printf("[INFO] rid=%s Asynchronously waiting for command to finish", rid)

		waitErr := p.cmd.Wait()

		close(done)

		if waitErr != nil {
			log.Printf("[ERROR] rid=%s Asynchronous command failed, error: %v", rid, waitErr)
		} else {
			log.Printf("[INFO] rid=%s Asynchronous command finished successfully", rid)
		}
	}()

	go func() {
		p.terminateOnContextDone(ctx, done)
	}()
}

func (p *Process) terminateOnContextDone(
	ctx context.Context,
	done <-chan struct{},
) {
	rid := requestIDFromContext(ctx)
	select {
	case <-ctx.Done():
		pid := p.cmd.Pid()

		if p.timeout == nil {
			log.Printf(
				"[INFO] rid=%s Context closed, no grace termination timeout set, sending SIGKILL to process group",
				rid,
			)
			p.signalProcessGroup(pid, syscall.SIGKILL)

			return
		}

		log.Printf("[INFO] rid=%s Context closed, sending SIGTERM to process group", rid)
		p.signalProcessGroup(pid, syscall.SIGTERM)

		t := time.NewTimer(*p.timeout)
		defer t.Stop()

		select {
		case <-t.C:
			log.Printf("[INFO] rid=%s Process still running after %v, sending SIGKILL to process group",
				rid, *p.timeout)
			p.signalProcessGroup(pid, syscall.SIGKILL)
		case <-done:
		}

	case <-done:
	}
}

func (p *Process) signalProcessGroup(pid int, sig syscall.Signal) {
	if pid <= 0 {
		log.Printf("[WARN] Cannot send %s to process group: PID is %d", sig, pid)

		return
	}

	pgid := -pid
	if err := p.runner.Kill(pgid, sig); err != nil {
		log.Printf("[ERROR] Failed to send %s to process group %d: %v", sig, pgid, err)
	}
}

func (p *Process) determineExitCodeAndError(ctx context.Context, err error) (int, error) {
	if err != nil {
		if p.isTimeoutOrCanceled(ctx) {
			// Timeout or cancellation takes precedence over other errors as this is intentional.
			//nolint:wrapcheck
			return -1, ctx.Err()
		}

		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return exitError.ExitCode(), err
		}

		return -1, err
	}

	if p.cmd.ProcessState() != nil {
		return p.cmd.ProcessState().ExitCode(), nil
	}

	return 0, nil
}

func (p *Process) isTimeoutOrCanceled(ctx context.Context) bool {
	return ctx.Err() != nil && (errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(ctx.Err(), context.Canceled))
}

// requestIDFromContext extracts request ID from context.
func requestIDFromContext(ctx context.Context) string {
	if v := ctx.Value(RequestIDKey); v != nil {
		if rid, ok := v.(string); ok && rid != "" {
			return rid
		}
	}

	// Try with string key for compatibility
	if v := ctx.Value("requestID"); v != nil {
		if rid, ok := v.(string); ok && rid != "" {
			return rid
		}
	}

	return "-"
}
