package handlers_test

import (
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"sync"
	"syscall"

	"github.com/dkarczmarski/webcmd/pkg/cmdrunner"
)

// fakeRunner is a lightweight test double for cmdrunner.Runner.
// It records the last command invocation and returns a configurable fakeCommand.
type fakeRunner struct {
	mu sync.Mutex

	gotCommand string
	gotArgs    []string

	cmd *fakeCommand

	killMu    sync.Mutex
	killCalls []killCall
	killHook  func(pid int, sig syscall.Signal) error
}

type killCall struct {
	pid int
	sig syscall.Signal
}

// Command records the command invocation and returns a fake command instance.
// A shallow copy of args is stored to avoid accidental mutation by the caller.
func (r *fakeRunner) Command(command string, args ...string) cmdrunner.Command {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.gotCommand = command

	r.gotArgs = append([]string(nil), args...)

	if r.cmd == nil {
		r.cmd = &fakeCommand{pid: 123}
	}

	return r.cmd
}

// Kill records kill signals sent to the process group.
// Tests can optionally inject a hook to simulate failures or synchronization.
func (r *fakeRunner) Kill(pid int, sig syscall.Signal) error {
	r.killMu.Lock()
	r.killCalls = append(r.killCalls, killCall{pid: pid, sig: sig})
	hook := r.killHook
	r.killMu.Unlock()

	if hook != nil {
		return hook(pid, sig)
	}

	return nil
}

// SnapshotCommand returns the last recorded command and a copy of its args.
// Copying prevents data races if tests read the values while the handler runs.
func (r *fakeRunner) SnapshotCommand() (string, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.gotCommand, append([]string(nil), r.gotArgs...)
}

// fakeCommand simulates cmdrunner.Command.
// It allows tests to control process lifecycle behavior (Start/Wait)
// and capture stdout/stderr writers configured by the handler.
type fakeCommand struct {
	mu sync.Mutex

	stdout io.Writer
	stderr io.Writer

	sys *syscall.SysProcAttr

	startErr error
	waitErr  error

	// waitBlock allows tests to artificially block Wait().
	// This is useful for testing asynchronous behavior where the handler
	// must return before the command finishes.
	waitBlock <-chan struct{}

	pid int

	// onStart allows tests to simulate process output immediately after Start().
	// This mimics a running command writing to stdout/stderr.
	onStart func(c *fakeCommand)
}

func (c *fakeCommand) SetSysProcAttr(attr *syscall.SysProcAttr) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sys = attr
}

func (c *fakeCommand) SetStdout(w io.Writer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stdout = w
}

func (c *fakeCommand) SetStderr(w io.Writer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stderr = w
}

// Start simulates starting the command.
// If startErr is set, the start fails.
// Otherwise an optional hook can simulate process output.
func (c *fakeCommand) Start() error {
	c.mu.Lock()
	err := c.startErr
	onStart := c.onStart
	c.mu.Unlock()

	if err != nil {
		return err
	}

	if onStart != nil {
		onStart(c)
	}

	return nil
}

// Wait simulates process completion.
// Tests can block this call using waitBlock to verify async behavior.
func (c *fakeCommand) Wait() error {
	if c.waitBlock != nil {
		<-c.waitBlock
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	return c.waitErr
}

func (c *fakeCommand) ProcessState() *os.ProcessState { //nolint:forbidigo // explicit nil is what we need for tests
	return nil
}

func (c *fakeCommand) Pid() int { return c.pid }

// flusherRecorder extends httptest.ResponseRecorder to track Flush() calls.
// Streaming handlers require http.Flusher, which ResponseRecorder does not track.
type flusherRecorder struct {
	*httptest.ResponseRecorder
	flushed bool
}

func (f *flusherRecorder) Flush() { f.flushed = true }

func ptrString(s string) *string { return &s }

type errorReader struct{}

// errorReader simulates a request body that fails during Read(),
// allowing the handler's error path to be tested.
func (e *errorReader) Read(_ []byte) (int, error) { return 0, errors.New("read error") }
