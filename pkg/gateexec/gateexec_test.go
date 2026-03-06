package gateexec_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dkarczmarski/webcmd/pkg/config"
	"github.com/dkarczmarski/webcmd/pkg/gateexec"
)

func TestExecutor_Run_BasicPaths(t *testing.T) {
	t.Parallel()

	t.Run("nil gate config: action called once, registry not used, exit/error propagated", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		reg := &fakeRegistry{}
		exec := gateexec.New(reg)

		wantExit := 42
		wantErr := errors.New("action error")

		actionCalls := 0
		action := func(context.Context) (int, <-chan struct{}, error) {
			actionCalls++

			return wantExit, nil, wantErr
		}

		gotExit, gotErr := exec.Run(ctx, nil, "X", action)

		assertEqual(t, actionCalls, 1, "action calls")
		assertEqual(t, reg.callCount(), 0, "registry calls")
		assertEqual(t, gotExit, wantExit, "exit")
		assertIs(t, gotErr, wantErr)
	})

	cases := []struct {
		name       string
		cfg        *config.CallGateConfig
		defaultGrp string
		wantGroup  string
		wantMode   string
	}{
		{
			name:       "default group used when GroupName is nil",
			cfg:        &config.CallGateConfig{GroupName: nil, Mode: "single"},
			defaultGrp: "X",
			wantGroup:  "X",
			wantMode:   "single",
		},
		{
			name:       "GroupName overrides default group",
			cfg:        &config.CallGateConfig{GroupName: strPtr("Y"), Mode: "single"},
			defaultGrp: "X",
			wantGroup:  "Y",
			wantMode:   "single",
		},
		{
			name:       "mode is passed to registry as second argument",
			cfg:        &config.CallGateConfig{GroupName: nil, Mode: "custom-mode"},
			defaultGrp: "X",
			wantGroup:  "X",
			wantMode:   "custom-mode",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			gate := &fakeGate{}
			reg := &fakeRegistry{gateToReturn: gate}
			exec := gateexec.New(reg)

			actionCalls := 0
			action := func(context.Context) (int, <-chan struct{}, error) {
				actionCalls++

				return 0, nil, nil
			}

			_, _ = exec.Run(ctx, tc.cfg, tc.defaultGrp, action)

			assertEqual(t, actionCalls, 1, "action calls")
			assertEqual(t, reg.callCount(), 1, "registry calls")
			assertEqual(t, reg.lastGroup, tc.wantGroup, "registry group argument")
			assertEqual(t, reg.lastMode, tc.wantMode, "registry mode argument")
		})
	}
}

//nolint:lll
func TestExecutor_Run_PreActionErrors(t *testing.T) {
	t.Parallel()

	t.Run("GetOrCreate error: returns -1 and wraps ErrPreAction and original error; does not call action or Acquire", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		someErr := errors.New("registry failure")

		gate := &fakeGate{}
		reg := &fakeRegistry{
			gateToReturn: gate,
			errToReturn:  someErr,
		}
		exec := gateexec.New(reg)

		actionCalls := 0
		action := func(context.Context) (int, <-chan struct{}, error) {
			actionCalls++

			return 0, nil, nil
		}

		cfg := &config.CallGateConfig{Mode: "single"}
		exit, err := exec.Run(ctx, cfg, "X", action)

		assertEqual(t, exit, -1, "exit")
		assertIs(t, err, gateexec.ErrPreAction)
		assertIs(t, err, someErr)

		assertEqual(t, actionCalls, 0, "action calls")
		assertEqual(t, reg.callCount(), 1, "registry calls")
		assertEqual(t, gate.acquireCount(), 0, "gate Acquire calls")
	})

	t.Run("Acquire error: returns -1 and wraps ErrPreAction and original error; does not call action; does not call release", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		someErr := errors.New("acquire failure")

		gate := &fakeGate{acquireErr: someErr}
		reg := &fakeRegistry{gateToReturn: gate}
		exec := gateexec.New(reg)

		actionCalls := 0
		action := func(context.Context) (int, <-chan struct{}, error) {
			actionCalls++

			return 0, nil, nil
		}

		cfg := &config.CallGateConfig{Mode: "single"}
		exit, err := exec.Run(ctx, cfg, "X", action)

		assertEqual(t, exit, -1, "exit")
		assertIs(t, err, gateexec.ErrPreAction)
		assertIs(t, err, someErr)

		assertEqual(t, actionCalls, 0, "action calls")
		assertEqual(t, gate.acquireCount(), 1, "gate Acquire calls")
		assertEqual(t, gate.releaseCount(), 0, "release calls")
	})

	t.Run("Acquire receives the same context passed to Run", func(t *testing.T) {
		t.Parallel()

		ctx := context.WithValue(t.Context(), struct{}{}, "marker") //nolint:staticcheck

		gate := &fakeGate{}
		reg := &fakeRegistry{gateToReturn: gate}
		exec := gateexec.New(reg)

		action := func(context.Context) (int, <-chan struct{}, error) {
			return 0, nil, nil
		}

		cfg := &config.CallGateConfig{Mode: "single"}
		_, _ = exec.Run(ctx, cfg, "X", action)

		if gate.lastCtx != ctx {
			t.Fatalf("Acquire context: got %v, want the exact same instance", gate.lastCtx)
		}
	})
}

//nolint:lll
func TestExecutor_Run_ReleaseAndDone(t *testing.T) {
	t.Parallel()

	t.Run("done is nil: release is called synchronously before Run returns", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		gate := &fakeGate{}
		reg := &fakeRegistry{gateToReturn: gate}
		exec := gateexec.New(reg)

		action := func(context.Context) (int, <-chan struct{}, error) {
			return 0, nil, nil
		}

		cfg := &config.CallGateConfig{Mode: "single"}
		_, _ = exec.Run(ctx, cfg, "X", action)

		// If release is synchronous, it must already be 1 here.
		assertEqual(t, gate.releaseCount(), 1, "release calls")
	})

	t.Run("done is not nil: release is not called immediately; called once after done is closed", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		gate := &fakeGate{}
		reg := &fakeRegistry{gateToReturn: gate}
		exec := gateexec.New(reg)

		doneCh := make(chan struct{})

		action := func(context.Context) (int, <-chan struct{}, error) {
			return 0, doneCh, nil
		}

		cfg := &config.CallGateConfig{Mode: "single"}
		_, _ = exec.Run(ctx, cfg, "X", action)

		assertEqual(t, gate.releaseCount(), 0, "release calls immediately after Run")

		close(doneCh)

		eventually(t, 300*time.Millisecond, func() bool {
			return gate.releaseCount() == 1
		})

		assertEqual(t, gate.releaseCount(), 1, "release calls after done is closed")
	})

	t.Run("action returns error with done=nil: release still called once and Run returns the action error", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		gate := &fakeGate{}
		reg := &fakeRegistry{gateToReturn: gate}
		exec := gateexec.New(reg)

		runErr := errors.New("run failed")
		action := func(context.Context) (int, <-chan struct{}, error) {
			return 123, nil, runErr
		}

		cfg := &config.CallGateConfig{Mode: "single"}
		exit, err := exec.Run(ctx, cfg, "X", action)

		assertEqual(t, exit, 123, "exit")
		assertIs(t, err, runErr)

		assertEqual(t, gate.releaseCount(), 1, "release calls")
	})

	t.Run("action returns error with done!=nil: Run returns the action error; release happens only after done is closed", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		gate := &fakeGate{}
		reg := &fakeRegistry{gateToReturn: gate}
		exec := gateexec.New(reg)

		doneCh := make(chan struct{})
		runErr := errors.New("run failed")

		action := func(context.Context) (int, <-chan struct{}, error) {
			return 5, doneCh, runErr
		}

		cfg := &config.CallGateConfig{Mode: "single"}
		exit, err := exec.Run(ctx, cfg, "X", action)

		assertEqual(t, exit, 5, "exit")
		assertIs(t, err, runErr)

		// Not yet released until done is closed.
		assertEqual(t, gate.releaseCount(), 0, "release calls immediately after Run")

		close(doneCh)

		eventually(t, 300*time.Millisecond, func() bool {
			return gate.releaseCount() == 1
		})

		assertEqual(t, gate.releaseCount(), 1, "release calls after done is closed")
	})
}
