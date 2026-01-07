// Package cmdrunner provides a simple way to run system commands with context support,
// timeout handling, and combined output (stdout and stderr).
package cmdrunner

//go:generate mockgen -typed -source=cmdrunner.go -destination=internal/mocks/mock_cmdrunner.go -package=mocks

import (
	"bytes"
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
func RunCommand(ctx context.Context, command string, arguments []string) Result {
	return RunCommandWithRunner(ctx, &RealRunner{}, command, arguments)
}

// RunCommandWithRunner runs a command using the provided runner.
func RunCommandWithRunner(
	ctx context.Context,
	runner Runner,
	command string,
	arguments []string,
) Result {
	cmd := runner.Command(ctx, command, arguments...)

	var output bytes.Buffer

	cmd.SetStdout(&output)
	cmd.SetStderr(&output)

	err := cmd.Run()

	exitCode := determineExitCode(ctx, cmd, err, &output)

	return Result{
		ExitCode: exitCode,
		Output:   output.String(),
	}
}

func determineExitCode(ctx context.Context, cmd Command, err error, output *bytes.Buffer) int {
	if err != nil {
		if isTimeoutOrCanceled(ctx) {
			if output.Len() == 0 {
				_, _ = output.WriteString("command timed out or canceled")
			}

			return -1
		}

		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return exitError.ExitCode()
		}

		return -1
	}

	if cmd.ProcessState() != nil {
		return cmd.ProcessState().ExitCode()
	}

	return 0
}

func isTimeoutOrCanceled(ctx context.Context) bool {
	return ctx.Err() != nil && (errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(ctx.Err(), context.Canceled))
}
