package server_test

import (
	"context"
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"syscall"

	"github.com/dkarczmarski/webcmd/pkg/callgate"
	"github.com/dkarczmarski/webcmd/pkg/cmdrunner"
	"github.com/dkarczmarski/webcmd/pkg/config"
	"github.com/dkarczmarski/webcmd/pkg/gateexec"
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
type fakeCommand struct {
	mu sync.Mutex

	stdout io.Writer
	stderr io.Writer

	sys *syscall.SysProcAttr

	startErr error
	waitErr  error

	// waitBlock allows tests to artificially block Wait().
	waitBlock <-chan struct{}

	pid int

	// onStart allows tests to simulate process output immediately after Start().
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
func (c *fakeCommand) Wait() error {
	if c.waitBlock != nil {
		<-c.waitBlock
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	return c.waitErr
}

func (c *fakeCommand) ProcessState() *os.ProcessState { //nolint:forbidigo
	return nil
}

func (c *fakeCommand) Pid() int { return c.pid }

// fakeGateExecutor mimics the behavior expected by handlers.RunCommand:
//
// IMPORTANT:
//   - "busy" must be returned as gateexec.ErrPreAction-wrapped error,
//     otherwise handler treats it as a "command failure" (silent error => HTTP 200).
//   - invalid mode is also treated as pre-action/config error => HTTP 500 via ErrorSink.
type fakeGateExecutor struct {
	mu   sync.Mutex
	busy map[string]bool // gateID -> held?
}

func newFakeGateExecutor() *fakeGateExecutor {
	return &fakeGateExecutor{busy: make(map[string]bool)}
}

func (g *fakeGateExecutor) Run(
	ctx context.Context,
	gateConfig *config.CallGateConfig,
	key string,
	action gateexec.Action,
) (int, error) {
	// No gate => no gating.
	if gateConfig == nil {
		exitCode, done, err := action(ctx)
		if done != nil {
			go func() { <-done }()
		}

		return exitCode, err
	}

	mode := gateConfig.Mode
	if mode == "" {
		mode = "single"
	}

	// In production this kind of error typically happens before action runs,
	// so it should behave like "pre-action" failure.
	if mode != "single" {
		return -1, fmtPreActionf("invalid callgate mode: %s", mode)
	}

	group := key
	if gateConfig.GroupName != nil {
		// Explicit group (can be empty string).
		group = *gateConfig.GroupName
	}

	gid := gateID(mode, group)

	// TryAcquire: if busy => return PRE-ACTION busy.
	g.mu.Lock()
	if g.busy[gid] {
		g.mu.Unlock()
		// Critical: wrap busy as gateexec.ErrPreAction so handler translates it to HTTP 429.
		return -1, fmtPreActionWrap(callgate.ErrBusy)
	}

	g.busy[gid] = true
	g.mu.Unlock()

	exitCode, done, err := action(ctx)
	if err != nil {
		g.mu.Lock()
		delete(g.busy, gid)
		g.mu.Unlock()

		return exitCode, err
	}

	if done != nil {
		// Async: release when done closes.
		go func() {
			<-done
			g.mu.Lock()
			delete(g.busy, gid)
			g.mu.Unlock()
		}()

		return exitCode, nil
	}

	// Sync: release immediately.
	g.mu.Lock()
	delete(g.busy, gid)
	g.mu.Unlock()

	return exitCode, nil
}

func gateID(mode, group string) string { return mode + "|" + group }

// fmtPreActionWrap wraps an underlying error as gateexec.ErrPreAction.
func fmtPreActionWrap(err error) error {
	// This shape makes errors.Is(x, gateexec.ErrPreAction) AND errors.Is(x, err) both true.
	return errors.Join(gateexec.ErrPreAction, err)
}

// fmtPreActionf creates a pre-action error with message.
// We still wrap gateexec.ErrPreAction so handler treats it as "failed to start command".
func fmtPreActionf(format string, args ...any) error {
	return errors.Join(gateexec.ErrPreAction, errors.New(sprintf(format, args...)))
}

// sprintf avoids importing fmt just for Sprintf in tests (optional).
func sprintf(format string, args ...any) string {
	// Minimal formatter, good enough for our messages used in assertions.
	// If you prefer, replace this whole helper with fmt.Sprintf and import fmt.
	s := format
	for _, a := range args {
		s = strings.Replace(s, "%s", toString(a), 1)
	}

	return s
}

func toString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	default:
		return "<?>"
	}
}

// flusherRecorder extends httptest.ResponseRecorder to track Flush() calls.
type flusherRecorder struct {
	*httptest.ResponseRecorder
	flushed bool
}

func (f *flusherRecorder) Flush() { f.flushed = true }

type errorReader struct{}

func (e *errorReader) Read(_ []byte) (int, error) { return 0, errors.New("read error") }
