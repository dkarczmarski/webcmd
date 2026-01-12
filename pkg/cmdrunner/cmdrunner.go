// Package cmdrunner provides a simple way to run system commands with context support,
// timeout handling, and combined output (stdout and stderr).
package cmdrunner

import (
	"context"
	"io"
	"os"
	"os/exec"
	"syscall"
)

// Result represents the result of a command execution.
type Result struct {
	ExitCode int
	Output   string
}

// Command interface abstracts exec.Cmd.
type Command interface {
	Start() error
	Wait() error
	SetStdout(w io.Writer)
	SetStderr(w io.Writer)
	SetSysProcAttr(attr *syscall.SysProcAttr)
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

func (c *realCommand) SetSysProcAttr(attr *syscall.SysProcAttr) {
	c.Cmd.SysProcAttr = attr
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
