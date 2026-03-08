package executor_test

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/dkarczmarski/webcmd/pkg/callgate"
	"github.com/dkarczmarski/webcmd/pkg/config"
	"github.com/dkarczmarski/webcmd/pkg/executor"
	"github.com/dkarczmarski/webcmd/pkg/gateexec"
	"github.com/dkarczmarski/webcmd/pkg/processrunner"
)

func TestExecutor_Execute_Sync(t *testing.T) {
	t.Parallel()

	fr := &fakeRunner{}
	fr.cmd = &fakeCommand{
		pid: 123,
		onStart: func(c *fakeCommand) {
			_, _ = c.safeStdout().Write([]byte("process output"))
		},
	}

	pr := processrunner.New(fr)
	ge := &fakeGateExecutor{}
	exec := executor.New(pr, ge)

	req := executor.ExecuteRequest{
		Command:      "echo",
		Arguments:    []string{"hello"},
		OutputWriter: io.Discard,
		DefaultGroup: "test",
	}

	res := exec.Execute(t.Context(), req)

	if res.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", res.ExitCode)
	}

	if res.Err != nil {
		t.Fatalf("expected nil error, got %v", res.Err)
	}

	gotCmd, gotArgs := fr.SnapshotCommand()
	if gotCmd != "echo" {
		t.Fatalf("expected command %q, got %q", "echo", gotCmd)
	}

	if len(gotArgs) != 1 || gotArgs[0] != "hello" {
		t.Fatalf("expected args %v, got %v", []string{"hello"}, gotArgs)
	}
}

func TestExecutor_Execute_GatePreActionError(t *testing.T) {
	t.Parallel()

	pr := processrunner.New(&fakeRunner{})
	ge := &fakeGateExecutor{err: gateexec.ErrPreAction}
	exec := executor.New(pr, ge)

	req := executor.ExecuteRequest{
		Command: "echo",
	}

	res := exec.Execute(t.Context(), req)

	if res.Err == nil {
		t.Fatalf("expected error, got nil")
	}

	if !errors.Is(res.Err, gateexec.ErrPreAction) {
		t.Fatalf("expected ErrPreAction, got %v", res.Err)
	}
}

func TestExecutor_Execute_StartProcessError(t *testing.T) {
	t.Parallel()

	fr := &fakeRunner{}
	fr.cmd = &fakeCommand{
		pid:      123,
		startErr: errors.New("boom start"),
	}

	pr := processrunner.New(fr)
	ge := &fakeGateExecutor{}
	exec := executor.New(pr, ge)

	req := executor.ExecuteRequest{
		Command:      "echo",
		Arguments:    []string{"hello"},
		OutputWriter: io.Discard,
	}

	res := exec.Execute(t.Context(), req)

	if res.Err == nil {
		t.Fatalf("expected error, got nil")
	}

	if res.ExitCode != -1 {
		t.Fatalf("expected exit code -1, got %d", res.ExitCode)
	}

	msg := res.Err.Error()
	if !strings.Contains(msg, "failed to start process") {
		t.Fatalf("expected error to contain %q, got %q", "failed to start process", msg)
	}
}

func TestExecutor_Execute_WaitSyncInfrastructureError(t *testing.T) {
	t.Parallel()

	fr := &fakeRunner{}
	fr.cmd = &fakeCommand{
		pid:     123,
		waitErr: errors.New("wait infrastructure failure"),
		onStart: func(c *fakeCommand) {
			_, _ = c.safeStdout().Write([]byte("started"))
		},
	}

	pr := processrunner.New(fr)
	ge := &fakeGateExecutor{}
	exec := executor.New(pr, ge)

	req := executor.ExecuteRequest{
		Command:      "echo",
		Arguments:    []string{"hello"},
		OutputWriter: io.Discard,
	}

	res := exec.Execute(t.Context(), req)

	if res.Err == nil {
		t.Fatalf("expected error, got nil")
	}

	if res.ExitCode != -1 {
		t.Fatalf("expected exit code -1, got %d", res.ExitCode)
	}

	msg := res.Err.Error()
	if !strings.Contains(msg, "process wait failed") {
		t.Fatalf("expected error to contain %q, got %q", "process wait failed", msg)
	}
}

func TestExecutor_Execute_Async_ReturnsBeforeWait(t *testing.T) {
	t.Parallel()

	block := make(chan struct{})

	fr := &fakeRunner{}
	fr.cmd = &fakeCommand{
		pid:       123,
		waitBlock: block,
	}

	pr := processrunner.New(fr)
	ge := &fakeGateExecutor{}
	exec := executor.New(pr, ge)

	req := executor.ExecuteRequest{
		Command:   "echo hello",
		Async:     true,
		Arguments: []string{},
	}

	done := make(chan struct{})

	var res executor.ExecuteResult

	go func() {
		res = exec.Execute(t.Context(), req)

		close(done)
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		close(block)
		t.Fatalf("Execute did not return quickly for Async=true")
	}

	if res.Err != nil {
		close(block)
		t.Fatalf("expected nil error, got %v", res.Err)
	}

	if res.ExitCode != 0 {
		close(block)
		t.Fatalf("expected exit code 0, got %d", res.ExitCode)
	}

	close(block)
}

func TestExecutor_Execute_CallGateInvalidMode_ReturnsPreActionError(t *testing.T) {
	t.Parallel()

	fr := &fakeRunner{}
	pr := processrunner.New(fr)
	ge := &fakeGateExecutor{}
	exec := executor.New(pr, ge)

	req := executor.ExecuteRequest{
		Command: "echo hello",
		CallGate: &config.CallGateConfig{
			GroupName: ptrString("test-group"),
			Mode:      "invalid-mode",
		},
	}

	res := exec.Execute(t.Context(), req)

	if res.Err == nil {
		t.Fatalf("expected error, got nil")
	}

	if !errors.Is(res.Err, gateexec.ErrPreAction) {
		t.Fatalf("expected ErrPreAction, got %v", res.Err)
	}

	if !strings.Contains(res.Err.Error(), "invalid callgate mode") {
		t.Fatalf("expected error message to contain %q, got %q", "invalid callgate mode", res.Err.Error())
	}
}

func TestExecutor_Execute_CallGateBusy_ReturnsBusyError(t *testing.T) {
	t.Parallel()

	fr := &fakeRunner{}
	pr := processrunner.New(fr)
	ge := &fakeGateExecutor{}
	exec := executor.New(pr, ge)

	req := executor.ExecuteRequest{
		Command: "echo hello",
		CallGate: &config.CallGateConfig{
			GroupName: ptrString("test-group"),
			Mode:      "single",
		},
	}

	release := ge.hold("single", "test-group")
	defer release()

	res := exec.Execute(t.Context(), req)

	if res.Err == nil {
		t.Fatalf("expected error, got nil")
	}

	if !errors.Is(res.Err, callgate.ErrBusy) {
		t.Fatalf("expected ErrBusy, got %v", res.Err)
	}
}

func TestExecutor_Execute_CallGateDefaultGroup_Isolates(t *testing.T) {
	t.Parallel()

	fr := &fakeRunner{}
	fr.cmd = &fakeCommand{
		pid: 123,
		onStart: func(c *fakeCommand) {
			_, _ = c.safeStdout().Write([]byte("ok"))
		},
	}

	pr := processrunner.New(fr)
	ge := &fakeGateExecutor{}
	exec := executor.New(pr, ge)

	req1 := executor.ExecuteRequest{
		Command:      "echo hello",
		DefaultGroup: "group1",
		CallGate: &config.CallGateConfig{
			GroupName: nil,
			Mode:      "single",
		},
	}

	req2 := executor.ExecuteRequest{
		Command:      "echo hello",
		DefaultGroup: "group2",
		OutputWriter: io.Discard,
		CallGate: &config.CallGateConfig{
			GroupName: nil,
			Mode:      "single",
		},
	}

	release := ge.hold("single", "group1")
	defer release()

	res1 := exec.Execute(t.Context(), req1)
	if res1.Err == nil {
		t.Fatalf("expected res1 to fail (gate busy)")
	}

	res2 := exec.Execute(t.Context(), req2)
	if res2.Err != nil {
		t.Fatalf("expected res2 to succeed, got err=%v", res2.Err)
	}
}

func TestExecutor_Execute_CallGateEmptyGroupName_Shared(t *testing.T) {
	t.Parallel()

	fr := &fakeRunner{}
	pr := processrunner.New(fr)
	ge := &fakeGateExecutor{}
	exec := executor.New(pr, ge)

	req := executor.ExecuteRequest{
		Command: "echo hello",
		CallGate: &config.CallGateConfig{
			GroupName: ptrString(""),
			Mode:      "single",
		},
	}

	release := ge.hold("single", "")
	defer release()

	res := exec.Execute(t.Context(), req)
	if res.Err == nil {
		t.Fatalf("expected res to fail (gate busy)")
	}
}

func TestExecutor_Execute_CallGateSharedGroupName_Shared(t *testing.T) {
	t.Parallel()

	fr := &fakeRunner{}
	pr := processrunner.New(fr)
	ge := &fakeGateExecutor{}
	exec := executor.New(pr, ge)

	shared := "shared"

	req1 := executor.ExecuteRequest{
		Command: "echo hello",
		CallGate: &config.CallGateConfig{
			GroupName: &shared,
			Mode:      "single",
		},
	}

	req2 := executor.ExecuteRequest{
		Command: "echo hello",
		CallGate: &config.CallGateConfig{
			GroupName: &shared,
			Mode:      "single",
		},
	}

	release := ge.hold("single", "shared")
	defer release()

	res1 := exec.Execute(t.Context(), req1)
	if res1.Err == nil {
		t.Fatalf("expected res1 to fail (gate busy)")
	}

	res2 := exec.Execute(t.Context(), req2)
	if res2.Err == nil {
		t.Fatalf("expected res2 to fail (gate busy)")
	}
}
