package executor_test

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"syscall"

	"github.com/dkarczmarski/webcmd/pkg/callgate"
	"github.com/dkarczmarski/webcmd/pkg/cmdrunner"
	"github.com/dkarczmarski/webcmd/pkg/config"
	"github.com/dkarczmarski/webcmd/pkg/gateexec"
)

type fakeRunner struct {
	mu sync.Mutex

	gotCommand string
	gotArgs    []string

	cmd *fakeCommand
}

func (r *fakeRunner) Command(name string, arg ...string) cmdrunner.Command {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.gotCommand = name

	r.gotArgs = append([]string(nil), arg...)

	if r.cmd == nil {
		r.cmd = &fakeCommand{pid: 123}
	}

	return r.cmd
}

func (r *fakeRunner) Kill(_ int, _ syscall.Signal) error {
	return nil
}

func (r *fakeRunner) SnapshotCommand() (string, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()

	return r.gotCommand, append([]string(nil), r.gotArgs...)
}

type fakeCommand struct {
	mu sync.Mutex

	pid    int
	stdout io.Writer

	startErr error
	waitErr  error

	waitBlock <-chan struct{}

	onStart func(*fakeCommand)
}

func (c *fakeCommand) SetStdout(w io.Writer) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stdout = w
}

func (c *fakeCommand) safeStdout() io.Writer {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.stdout == nil {
		return io.Discard
	}

	return c.stdout
}

func (c *fakeCommand) SetStderr(_ io.Writer)                 {}
func (c *fakeCommand) SetSysProcAttr(_ *syscall.SysProcAttr) {}
func (c *fakeCommand) ProcessState() *os.ProcessState        { return nil }
func (c *fakeCommand) Pid() int                              { return c.pid }

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

func (c *fakeCommand) Wait() error {
	if c.waitBlock != nil {
		<-c.waitBlock
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	return c.waitErr
}

var (
	_ cmdrunner.Runner  = (*fakeRunner)(nil)
	_ cmdrunner.Command = (*fakeCommand)(nil)
)

type fakeGateExecutor struct {
	mu   sync.Mutex
	err  error
	busy map[string]bool
}

//nolint:unparam
func (g *fakeGateExecutor) hold(mode, group string) func() {
	g.mu.Lock()

	if g.busy == nil {
		g.busy = make(map[string]bool)
	}

	gid := mode + "|" + group
	g.busy[gid] = true
	g.mu.Unlock()

	return func() {
		g.mu.Lock()
		delete(g.busy, gid)
		g.mu.Unlock()
	}
}

func (g *fakeGateExecutor) Run(
	ctx context.Context,
	gateConfig *config.CallGateConfig,
	key string,
	action gateexec.Action,
) (int, error) {
	if g.err != nil {
		return -1, g.err
	}

	if gateConfig != nil {
		mode := gateConfig.Mode
		if mode == "" {
			mode = "single"
		}

		if mode != "single" {
			return -1, errors.Join(gateexec.ErrPreAction, errors.New("invalid callgate mode: "+mode))
		}

		group := key
		if gateConfig.GroupName != nil {
			group = *gateConfig.GroupName
		}

		gid := mode + "|" + group

		g.mu.Lock()
		isBusy := g.busy[gid]
		g.mu.Unlock()

		if isBusy {
			return -1, errors.Join(gateexec.ErrPreAction, callgate.ErrBusy)
		}
	}

	exitCode, done, err := action(ctx)
	if done != nil {
		go func() { <-done }()
	}

	return exitCode, err
}

func ptrString(s string) *string { return &s }
