//go:build integration

//nolint:paralleltest
package processrunner_test

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/dkarczmarski/webcmd/pkg/cmdrunner"
	"github.com/dkarczmarski/webcmd/pkg/processrunner"
)

func Test_StartProcess_RedirectsStdoutAndStderrToWriter(t *testing.T) {
	runner := &cmdrunner.RealRunner{}

	var w syncBuffer

	p := mustStartShell(t, runner, &w, nil, nil, `echo out; echo err 1>&2`)

	code, err := p.WaitSync(t.Context())
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	if code != 0 {
		t.Fatalf("expected exit code 0, got: %d", code)
	}

	out := w.String()

	if !strings.Contains(out, "out") {
		t.Fatalf("expected output to contain %q, got: %q", "out", out)
	}

	if !strings.Contains(out, "err") {
		t.Fatalf("expected output to contain %q, got: %q", "err", out)
	}
}

func Test_StartProcess_ReturnsErrStartCommand_WhenBinaryMissing(t *testing.T) {
	runner := &cmdrunner.RealRunner{}

	var w syncBuffer

	_, err := processrunner.StartProcess(runner, "__no_such_binary__", nil, &w, nil)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	if !errors.Is(err, processrunner.ErrStartCommand) {
		t.Fatalf("expected ErrStartCommand, got: %v", err)
	}
}

func Test_StartProcess_SetsOwnProcessGroup_Setpgid(t *testing.T) {
	runner := &cmdrunner.RealRunner{}

	var w syncBuffer

	p := mustStartShell(t, runner, &w, nil, nil, `sleep 10`)

	t.Cleanup(func() {
		// Ensure cleanup even if the test fails early.
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		_, _ = waitSyncWithTimeout(t, ctx, p, 2*time.Second)
	})

	pid := p.Pid()
	if pid <= 0 {
		t.Fatalf("expected pid > 0, got: %d", pid)
	}

	pgid, err := p.ProcessGroupID()
	if err != nil {
		t.Fatalf("ProcessGroupID failed: %v", err)
	}

	// With Setpgid=true we expect the process to be the leader of its own group.
	if pgid != pid {
		t.Fatalf("expected pgid == pid (setpgid), got pgid=%d pid=%d", pgid, pid)
	}
}

func Test_WaitSync_Exit0_ReturnsZeroAndNil(t *testing.T) {
	runner := &cmdrunner.RealRunner{}

	var w syncBuffer

	p := mustStartShell(t, runner, &w, nil, nil, `exit 0`)

	code, err := p.WaitSync(t.Context())
	if err != nil || code != 0 {
		t.Fatalf("expected (0,nil), got (%d,%v)", code, err)
	}
}

func Test_WaitSync_NonZeroExit_ReturnsExitCodeAndNilError(t *testing.T) {
	runner := &cmdrunner.RealRunner{}

	var w syncBuffer

	p := mustStartShell(t, runner, &w, nil, nil, `exit 7`)

	code, err := p.WaitSync(t.Context())
	// By design: normal non-zero exit returns the exit code and nil error.
	if err != nil {
		t.Fatalf("expected nil error for normal non-zero exit, got: %v", err)
	}

	if code != 7 {
		t.Fatalf("expected exit code 7, got: %d", code)
	}
}

func Test_WaitAsync_EmitsExactlyOneResult_ThenClosesChannel(t *testing.T) {
	runner := &cmdrunner.RealRunner{}

	var w syncBuffer

	p := mustStartShell(t, runner, &w, nil, nil, `exit 0`)

	ch := p.WaitAsync(t.Context())

	res, ok := <-ch
	if !ok {
		t.Fatalf("expected one result, channel closed")
	}

	if res.Err != nil || res.ExitCode != 0 {
		t.Fatalf("expected {0,nil}, got {%d,%v}", res.ExitCode, res.Err)
	}

	_, ok = <-ch
	if ok {
		t.Fatalf("expected channel to be closed after one result")
	}
}

func Test_WaitAsync_NonZeroExit_ReturnsExitCodeAndNilError(t *testing.T) {
	runner := &cmdrunner.RealRunner{}

	var w syncBuffer

	p := mustStartShell(t, runner, &w, nil, nil, `exit 13`)

	ch := p.WaitAsync(t.Context())

	res := <-ch

	if res.Err != nil {
		t.Fatalf("expected nil error for normal non-zero exit, got: %v", res.Err)
	}

	if res.ExitCode != 13 {
		t.Fatalf("expected exit code 13, got: %d", res.ExitCode)
	}
}

func Test_CancelCtx_KillsProcessGroupImmediately_WithSIGKILL_WhenNoGraceTimeout(t *testing.T) {
	runner := &cmdrunner.RealRunner{}

	var w syncBuffer

	var (
		observedMu sync.Mutex
		observed   []syscall.Signal
	)

	// SignalObserver gives deterministic assertions without timing-based heuristics.
	obs := func(_ int, sig syscall.Signal) {
		observedMu.Lock()
		defer observedMu.Unlock()

		observed = append(observed, sig)
	}

	opts := []processrunner.Option{processrunner.WithSignalObserver(obs)}

	p := mustStartShell(t, runner, &w, nil, opts, `trap "" TERM; while true; do sleep 1; done`)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	done := make(chan struct{})

	var (
		code int
		err  error
	)

	go func() {
		defer close(done)

		code, err = p.WaitSync(ctx)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("process did not terminate promptly after cancel")
	}

	if code != -1 {
		t.Fatalf("expected exit code -1 for signal termination, got: %d", code)
	}

	if err == nil {
		t.Fatalf("expected non-nil error for signal termination, got nil")
	}

	observedMu.Lock()
	defer observedMu.Unlock()

	foundKILL := false

	for _, s := range observed {
		if s == syscall.SIGKILL {
			foundKILL = true

			continue
		}

		if s == syscall.SIGTERM {
			t.Fatalf("did not expect SIGTERM when graceTimeout is nil; observed=%v", observed)
		}
	}

	if !foundKILL {
		t.Fatalf("expected SIGKILL to be sent; observed=%v", observed)
	}
}

func Test_CancelCtx_KillsChildProcess_UsingProcessGroupKill(t *testing.T) {
	runner := &cmdrunner.RealRunner{}

	var w syncBuffer

	p := mustStartShell(t, runner, &w, nil, nil, `sleep 100 & child=$!; echo child:$child; wait`)

	waitForSubstring(t, &w, "child:", 1*time.Second)

	re := regexp.MustCompile(`child:(\d+)`)
	m := re.FindStringSubmatch(w.String())

	if len(m) != 2 {
		t.Fatalf("failed to parse child pid from output: %q", w.String())
	}

	childPID, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("invalid child pid %q: %v", m[1], err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	code, werr := waitSyncWithTimeout(t, ctx, p, 2*time.Second)
	if code != -1 || werr == nil {
		t.Fatalf("expected (-1, non-nil) after cancel kill, got (%d,%v)", code, werr)
	}

	// Main promise: cancelling context kills the whole process group, including background children.
	waitForProcessGone(t, childPID, 1*time.Second)
}

func Test_CancelCtx_SendsSIGTERM_ThenSIGKILL_AfterGraceTimeout_WhenProcessIgnoresTERM(t *testing.T) {
	runner := &cmdrunner.RealRunner{}

	var w syncBuffer

	var (
		observedMu sync.Mutex
		observed   []syscall.Signal
	)

	obs := func(_ int, sig syscall.Signal) {
		observedMu.Lock()
		defer observedMu.Unlock()

		observed = append(observed, sig)
	}

	grace := 200 * time.Millisecond
	opts := []processrunner.Option{processrunner.WithSignalObserver(obs)}

	// "ready" is printed AFTER trap is installed.
	// Without this barrier, the test can cancel too early and kill the process
	// before it starts ignoring SIGTERM, making the test flaky.
	p := mustStartShell(t, runner, &w, &grace, opts, `trap "" TERM; echo ready; while true; do sleep 1; done`)

	waitForSubstring(t, &w, "ready", 1*time.Second)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	cancel()

	code, err := waitSyncWithTimeout(t, ctx, p, 2*time.Second)
	if code != -1 || err == nil {
		t.Fatalf("expected (-1, non-nil) after forced kill, got (%d,%v)", code, err)
	}

	observedMu.Lock()
	defer observedMu.Unlock()

	termIdx := -1
	killIdx := -1

	for i, s := range observed {
		if s == syscall.SIGTERM && termIdx == -1 {
			termIdx = i

			continue
		}

		if s == syscall.SIGKILL && killIdx == -1 {
			killIdx = i
		}
	}

	if termIdx == -1 || killIdx == -1 {
		t.Fatalf("expected to observe SIGTERM and SIGKILL; observed=%v", observed)
	}

	if termIdx > killIdx {
		t.Fatalf("expected SIGTERM before SIGKILL; observed=%v", observed)
	}
}

func Test_CancelCtx_AllowsGracefulExit_DuringGracePeriod_WithoutSIGKILL(t *testing.T) {
	runner := &cmdrunner.RealRunner{}

	var w syncBuffer

	var (
		observedMu sync.Mutex
		observed   []syscall.Signal
	)

	obs := func(_ int, sig syscall.Signal) {
		observedMu.Lock()
		defer observedMu.Unlock()

		observed = append(observed, sig)
	}

	grace := 1 * time.Second
	opts := []processrunner.Option{processrunner.WithSignalObserver(obs)}

	// "ready" is printed AFTER trap is installed to avoid cancel-race flakiness.
	p := mustStartShell(t, runner, &w, &grace, opts, `trap "exit 0" TERM; echo ready; while true; do sleep 1; done`)

	waitForSubstring(t, &w, "ready", 1*time.Second)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	cancel()

	code, err := waitSyncWithTimeout(t, ctx, p, 2*time.Second)
	if err != nil {
		t.Fatalf("expected nil error for graceful exit, got: %v", err)
	}

	if code != 0 {
		t.Fatalf("expected exit code 0 for graceful exit, got: %d", code)
	}

	observedMu.Lock()
	defer observedMu.Unlock()

	foundTERM := false

	for _, s := range observed {
		if s == syscall.SIGTERM {
			foundTERM = true

			continue
		}

		if s == syscall.SIGKILL {
			t.Fatalf("did not expect SIGKILL for graceful exit; observed=%v", observed)
		}
	}

	if !foundTERM {
		t.Fatalf("expected SIGTERM to be sent; observed=%v", observed)
	}
}

func Test_WaitResultHasPriorityOverLaterContextCancel(t *testing.T) {
	runner := &cmdrunner.RealRunner{}

	var w syncBuffer

	p := mustStartShell(t, runner, &w, nil, nil, `sleep 0.05; exit 5`)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	// Cancel after the process has already exited; the Wait() result should win.
	time.AfterFunc(80*time.Millisecond, cancel)

	code, err := waitSyncWithTimeout(t, ctx, p, 2*time.Second)
	if err != nil {
		t.Fatalf("expected nil error for normal exit, got: %v", err)
	}

	if code != 5 {
		t.Fatalf("expected exit code 5, got: %d", code)
	}
}

func Test_CancelAfterWaitCompleted_HasNoSideEffects(t *testing.T) {
	runner := &cmdrunner.RealRunner{}

	var w syncBuffer

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	p := mustStartShell(t, runner, &w, nil, nil, `exit 0`)

	ch := p.WaitAsync(ctx)

	res := <-ch
	if res.Err != nil || res.ExitCode != 0 {
		t.Fatalf("expected {0,nil}, got {%d,%v}", res.ExitCode, res.Err)
	}

	// Cancel after completion should not hang or panic.
	cancel()

	select {
	case _, ok := <-ch:
		if ok {
			t.Fatalf("expected channel closed")
		}
	default:
	}
}

func Test_CancelledContextBeforeWaitSync_KillsRunningProcess(t *testing.T) {
	runner := &cmdrunner.RealRunner{}

	var w syncBuffer

	p := mustStartShell(t, runner, &w, nil, nil, `while true; do sleep 1; done`)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	code, err := waitSyncWithTimeout(t, ctx, p, 2*time.Second)
	if code != -1 {
		t.Fatalf("expected exit code -1 for signal termination, got: %d", code)
	}

	if err == nil {
		t.Fatalf("expected non-nil error for signal termination, got nil")
	}
}

func Test_OutputIsStreamedBeforeWaitCompletes(t *testing.T) {
	runner := &cmdrunner.RealRunner{}

	var w syncBuffer

	p := mustStartShell(t, runner, &w, nil, nil, `echo one; sleep 0.2; echo two`)

	// Verify streaming output: writer receives data before Wait completes.
	waitForSubstring(t, &w, "one", 500*time.Millisecond)

	code, err := p.WaitSync(t.Context())
	if err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	if code != 0 {
		t.Fatalf("expected exit code 0, got: %d", code)
	}

	out := w.String()
	if !strings.Contains(out, "two") {
		t.Fatalf("expected output to contain %q, got: %q", "two", out)
	}
}

func Test_WaitSyncAndWaitAsync_SemanticsAreConsistent(t *testing.T) {
	runner := &cmdrunner.RealRunner{}

	t.Run("exit 0", func(t *testing.T) {
		var (
			w1 syncBuffer
			w2 syncBuffer
		)

		p1 := mustStartShell(t, runner, &w1, nil, nil, `exit 0`)

		code1, err1 := p1.WaitSync(t.Context())

		p2 := mustStartShell(t, runner, &w2, nil, nil, `exit 0`)

		res2 := <-p2.WaitAsync(t.Context())

		if code1 != res2.ExitCode || (err1 != nil) != (res2.Err != nil) {
			t.Fatalf(
				"mismatch: WaitSync=(%d,%v) WaitAsync={%d,%v}",
				code1,
				err1,
				res2.ExitCode,
				res2.Err,
			)
		}
	})

	t.Run("exit 9", func(t *testing.T) {
		var (
			w1 syncBuffer
			w2 syncBuffer
		)

		p1 := mustStartShell(t, runner, &w1, nil, nil, `exit 9`)

		code1, err1 := p1.WaitSync(t.Context())

		p2 := mustStartShell(t, runner, &w2, nil, nil, `exit 9`)

		res2 := <-p2.WaitAsync(t.Context())

		if code1 != 9 || err1 != nil {
			t.Fatalf("expected WaitSync=(9,nil), got (%d,%v)", code1, err1)
		}

		if res2.ExitCode != 9 || res2.Err != nil {
			t.Fatalf("expected WaitAsync={9,nil}, got {%d,%v}", res2.ExitCode, res2.Err)
		}
	})

	t.Run("killed by cancel", func(t *testing.T) {
		var (
			w1 syncBuffer
			w2 syncBuffer
		)

		p1 := mustStartShell(t, runner, &w1, nil, nil, `while true; do sleep 1; done`)

		ctx1, cancel1 := context.WithCancel(t.Context())
		cancel1()

		code1, err1 := waitSyncWithTimeout(t, ctx1, p1, 2*time.Second)

		p2 := mustStartShell(t, runner, &w2, nil, nil, `while true; do sleep 1; done`)

		ctx2, cancel2 := context.WithCancel(t.Context())
		cancel2()

		res2 := <-p2.WaitAsync(ctx2)

		if code1 != -1 || err1 == nil {
			t.Fatalf("expected WaitSync=(-1,non-nil), got (%d,%v)", code1, err1)
		}

		if res2.ExitCode != -1 || res2.Err == nil {
			t.Fatalf("expected WaitAsync={-1,non-nil}, got {%d,%v}", res2.ExitCode, res2.Err)
		}
	})
}
