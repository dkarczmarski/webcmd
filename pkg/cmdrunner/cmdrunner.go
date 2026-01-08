// Package cmdrunner provides a simple way to run system commands with context support,
// timeout handling, and combined output (stdout and stderr).
package cmdrunner

//go:generate go run go.uber.org/mock/mockgen -typed -source=cmdrunner.go -destination=internal/mocks/mock_cmdrunner.go -package=mocks

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
)

// Result represents the result of a command execution.
type Result struct {
	ExitCode int
	Output   string
}

// Command interface abstracts exec.Cmd.
type Command interface {
	Run() error
	Start() error
	Wait() error
	SetStdout(w io.Writer)
	SetStderr(w io.Writer)
	ProcessState() *os.ProcessState
}

// Runner interface abstracts the creation and execution of commands.
type Runner interface {
	Command(ctx context.Context, name string, arg ...string) Command
}

type realCommand struct {
	*exec.Cmd
}

func (c *realCommand) SetStdout(w io.Writer) {
	c.Cmd.Stdout = w
}

func (c *realCommand) SetStderr(w io.Writer) {
	c.Cmd.Stderr = w
}

func (c *realCommand) ProcessState() *os.ProcessState {
	return c.Cmd.ProcessState
}

// RealRunner is a real implementation of the Runner interface.
type RealRunner struct{}

// Command creates a new Command.
//
//nolint:ireturn
func (r *RealRunner) Command(ctx context.Context, name string, arg ...string) Command {
	return &realCommand{exec.CommandContext(ctx, name, arg...)}
}

// RunCommand runs a command and returns its result.
func RunCommand(ctx context.Context, command string, arguments []string, writer io.Writer) (int, error) {
	return RunCommandWithRunner(ctx, &RealRunner{}, command, arguments, writer)
}

// RunCommandWithRunner runs a command using the provided runner.
func RunCommandWithRunner(
	ctx context.Context,
	runner Runner,
	command string,
	arguments []string,
	writer io.Writer,
) (int, error) {
	cmd := runner.Command(ctx, command, arguments...)

	cmd.SetStdout(writer)
	cmd.SetStderr(writer)

	err := cmd.Run()

	return determineExitCodeAndError(ctx, cmd, err)
}

func determineExitCodeAndError(ctx context.Context, cmd Command, err error) (int, error) {
	if err != nil {
		if isTimeoutOrCanceled(ctx) {
			//nolint:wrapcheck // error is intentionally forwarded as-is to the client
			return -1, ctx.Err()
		}

		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return exitError.ExitCode(), err
		}

		return -1, err
	}

	if cmd.ProcessState() != nil {
		return cmd.ProcessState().ExitCode(), nil
	}

	return 0, nil
}

func isTimeoutOrCanceled(ctx context.Context) bool {
	return ctx.Err() != nil && (errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(ctx.Err(), context.Canceled))
}
