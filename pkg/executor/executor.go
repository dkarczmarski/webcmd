package executor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"time"

	"github.com/dkarczmarski/webcmd/pkg/callgate"
	"github.com/dkarczmarski/webcmd/pkg/config"
	"github.com/dkarczmarski/webcmd/pkg/gateexec"
	"github.com/dkarczmarski/webcmd/pkg/processrunner"
)

var (
	ErrBusy         = errors.New("executor busy")
	ErrPreExecution = errors.New("executor pre-execution error")
	ErrRuntime      = errors.New("executor runtime error")
)

var errStartProcess = errors.New("executor start process")

// ProcessStarter abstracts starting a process for command execution.
//
// It is implemented by processrunner.ProcessRunner, which binds cmdrunner.Runner internally.
// This keeps Executor decoupled from cmdrunner.Runner and makes testing easier.
type ProcessStarter interface {
	StartProcess(
		command string,
		args []string,
		writer io.Writer,
		graceTimeout *time.Duration,
		opts ...processrunner.Option,
	) (*processrunner.Process, error)
}

// GateExecutor abstracts gate-controlled execution.
//
// A concrete implementation can internally bind a gate registry and delegate
// execution to gateexec without exposing gate coordination details to Executor.
type GateExecutor interface {
	Run(ctx context.Context, gateConfig *config.CallGateConfig, key string, action gateexec.Action) (int, error)
}

// ExecuteRequest describes a command execution request handled by Executor.
//
// It includes the rendered command and arguments, output destination,
// execution mode, timeout settings, and optional call gate configuration.
type ExecuteRequest struct {
	Command                 string
	Arguments               []string
	OutputWriter            io.Writer
	Async                   bool
	GraceTerminationTimeout *time.Duration
	CallGate                *config.CallGateConfig
	DefaultGroup            string
	Timeout                 *time.Duration
}

// ExecuteResult contains the outcome of a command execution.
//
// ExitCode is set when available. Err is non-nil when execution failed
// before completion or when the process finished with an execution error.
type ExecuteResult struct {
	ExitCode int
	Err      error
}

// Executor orchestrates command execution.
//
// It combines process startup with optional gate-controlled execution,
// and supports both synchronous and asynchronous command handling.
type Executor struct {
	starter ProcessStarter
	exec    GateExecutor
}

func New(starter ProcessStarter, exec GateExecutor) *Executor {
	return &Executor{
		starter: starter,
		exec:    exec,
	}
}

// Execute runs a command according to the provided request.
//
// If call gate configuration is present, execution is performed under gate control.
// In synchronous mode, Execute waits for the process to finish.
// In asynchronous mode, Execute returns after the process starts successfully,
// while completion is handled in the background.
//
// The returned ExecuteResult contains the process exit code when available
// and any execution error.
func (e *Executor) Execute(ctx context.Context, req ExecuteRequest) ExecuteResult {
	action := e.createGateAction(req)

	exitCode, err := e.exec.Run(ctx, req.CallGate, req.DefaultGroup, action)
	if err != nil {
		switch {
		case errors.Is(err, callgate.ErrBusy):
			// ErrBusy is treated as a special case of ErrPreExecution.
			// It means the command could not start because the executor/gate is currently busy.
			return ExecuteResult{
				ExitCode: exitCode,
				Err:      fmt.Errorf("%w: %w: %w", ErrPreExecution, ErrBusy, err),
			}

		case errors.Is(err, gateexec.ErrPreAction), errors.Is(err, errStartProcess):
			return ExecuteResult{
				ExitCode: exitCode,
				Err:      fmt.Errorf("%w: %w", ErrPreExecution, err),
			}

		default:
			return ExecuteResult{
				ExitCode: exitCode,
				Err:      fmt.Errorf("%w: %w", ErrRuntime, err),
			}
		}
	}

	return ExecuteResult{
		ExitCode: exitCode,
		Err:      nil,
	}
}

func (e *Executor) createGateAction(req ExecuteRequest) gateexec.Action {
	return func(ctx context.Context) (int, <-chan struct{}, error) {
		log.Printf("[INFO] Executing command: %s %v", req.Command, req.Arguments)

		proc, err := e.starter.StartProcess(req.Command, req.Arguments, req.OutputWriter, req.GraceTerminationTimeout)
		if err != nil {
			return -1, nil, fmt.Errorf("%w: failed to start process: %w", errStartProcess, err)
		}

		if req.Async {
			asyncCtx := context.WithoutCancel(ctx)

			var cancel context.CancelFunc = func() {}

			if req.Timeout != nil {
				asyncCtx, cancel = context.WithTimeout(asyncCtx, *req.Timeout)
			}

			return 0, e.waitAsyncAndLog(asyncCtx, proc, cancel), nil
		}

		exitCode, err := proc.WaitSync(ctx)
		if err != nil {
			// Extract exit code if possible
			type exitCoder interface {
				ExitCode() int
			}

			var ec exitCoder
			if errors.As(err, &ec) {
				exitCode = ec.ExitCode()
			}

			return exitCode, nil, fmt.Errorf("process wait failed: %w", err)
		}

		return exitCode, nil, nil
	}
}

func (e *Executor) waitAsyncAndLog(
	ctx context.Context,
	proc *processrunner.Process,
	cancel context.CancelFunc,
) <-chan struct{} {
	resCh := proc.WaitAsync(ctx)
	done := make(chan struct{})

	go func() {
		defer close(done)
		defer cancel()

		result := <-resCh
		if result.Err != nil {
			log.Printf("[ERROR] Asynchronous command failed (exit code: %d), error: %v",
				result.ExitCode, result.Err)

			return
		}

		log.Printf("[INFO] Asynchronous command finished successfully (exit code: %d)",
			result.ExitCode)
	}()

	return done
}
