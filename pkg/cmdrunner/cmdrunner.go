// Package cmdrunner provides a simple way to run system commands with context support,
// timeout handling, and combined output (stdout and stderr).
package cmdrunner

import (
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
	Pid() int
}

// Runner interface abstracts the creation and execution of commands.
type Runner interface {
	Command(name string, arg ...string) Command
	Kill(pid int, sig syscall.Signal) error
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

func (c *realCommand) Pid() int {
	if c.Cmd.Process == nil {
		return 0
	}

	return c.Cmd.Process.Pid
}

// RealRunner is a real implementation of the Runner interface.
type RealRunner struct{}

// Command creates a new Command.
//
//nolint:ireturn
func (r *RealRunner) Command(name string, arg ...string) Command {
	return &realCommand{exec.Command(name, arg...)}
}

// Kill sends a signal to a process group.
func (r *RealRunner) Kill(pid int, sig syscall.Signal) error {
	//nolint:wrapcheck
	return syscall.Kill(-pid, sig)
}
