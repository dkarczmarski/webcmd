package gateexec_test

import (
	"context"
	"sync/atomic"

	"github.com/dkarczmarski/webcmd/pkg/callgate"
)

type fakeRegistry struct {
	calls     int32
	lastGroup string
	lastMode  string

	gateToReturn callgate.CallGate
	errToReturn  error
}

func (r *fakeRegistry) GetOrCreate(group string, mode string) (callgate.CallGate, error) { //nolint:ireturn
	atomic.AddInt32(&r.calls, 1)
	r.lastGroup = group
	r.lastMode = mode

	return r.gateToReturn, r.errToReturn
}

func (r *fakeRegistry) callCount() int {
	return int(atomic.LoadInt32(&r.calls))
}

type fakeGate struct {
	acquireCalls int32
	lastCtx      context.Context //nolint:containedctx

	acquireErr error

	releaseCalls int32
}

func (g *fakeGate) Acquire(ctx context.Context) (func(), error) {
	atomic.AddInt32(&g.acquireCalls, 1)
	g.lastCtx = ctx

	if g.acquireErr != nil {
		return nil, g.acquireErr
	}

	release := func() {
		atomic.AddInt32(&g.releaseCalls, 1)
	}

	return release, nil
}

func (g *fakeGate) acquireCount() int {
	return int(atomic.LoadInt32(&g.acquireCalls))
}

func (g *fakeGate) releaseCount() int {
	return int(atomic.LoadInt32(&g.releaseCalls))
}

// compile-time interface check.
var _ callgate.CallGate = (*fakeGate)(nil)
