package callgate_test

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/dkarczmarski/webcmd/pkg/callgate"
)

type testGate struct {
	id int64
}

func (testGate) Acquire(ctx context.Context) (func(), error) {
	_ = ctx

	return func() {}, nil
}

func newTestGateID() func() callgate.CallGate {
	var nextID int64

	return func() callgate.CallGate {
		id := atomic.AddInt64(&nextID, 1)

		return testGate{id: id}
	}
}

func TestRegistry_GetOrCreate_WhenMissingAndFactoryNil_ReturnsErrGroupNotFound(t *testing.T) {
	t.Parallel()

	r := callgate.NewRegistry()

	gate, err := r.GetOrCreateWithFactory("missing", nil)
	if gate != nil {
		t.Fatalf("expected nil gate, got non-nil")
	}

	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	if !errors.Is(err, callgate.ErrGroupNotFound) {
		t.Fatalf("expected ErrGroupNotFound, got: %v", err)
	}
}

func TestRegistry_GetOrCreate_WhenMissingAndFactoryProvided_CreatesAndStores(t *testing.T) {
	t.Parallel()

	r := callgate.NewRegistry()

	var created int32

	factory := newTestGateID()

	wrappedFactory := func() callgate.CallGate {
		atomic.AddInt32(&created, 1)

		return factory()
	}

	gate1, err := r.GetOrCreateWithFactory("group-a", wrappedFactory)
	if err != nil {
		t.Fatalf("GetOrCreateWithFactory #1 error: %v", err)
	}

	if gate1 == nil {
		t.Fatalf("expected non-nil gate")
	}

	gate2, err := r.GetOrCreateWithFactory("group-a", nil)
	if err != nil {
		t.Fatalf("GetOrCreateWithFactory #2 error: %v", err)
	}

	if gate2 == nil {
		t.Fatalf("expected non-nil gate")
	}

	if gate1 != gate2 {
		t.Fatalf("expected same instance to be returned for the same group")
	}

	if atomic.LoadInt32(&created) != 1 {
		t.Fatalf("expected factory to be called once, got %d", created)
	}
}

func TestRegistry_GetOrCreate_WhenExists_IgnoresFactory(t *testing.T) {
	t.Parallel()

	r := callgate.NewRegistry()

	first := testGate{id: 1}

	firstFactory := func() callgate.CallGate {
		return first
	}

	gate1, err := r.GetOrCreateWithFactory("group-b", firstFactory)
	if err != nil {
		t.Fatalf("GetOrCreateWithFactory #1 error: %v", err)
	}

	if gate1 == nil {
		t.Fatalf("expected non-nil gate")
	}

	var called int32

	shouldNotBeCalled := func() callgate.CallGate {
		atomic.AddInt32(&called, 1)

		return testGate{id: 2}
	}

	gate2, err := r.GetOrCreateWithFactory("group-b", shouldNotBeCalled)
	if err != nil {
		t.Fatalf("GetOrCreateWithFactory #2 error: %v", err)
	}

	if gate2 == nil {
		t.Fatalf("expected non-nil gate")
	}

	if gate1 != gate2 {
		t.Fatalf("expected existing gate to be returned")
	}

	if atomic.LoadInt32(&called) != 0 {
		t.Fatalf("expected factory not to be called when gate exists, got %d", called)
	}
}

func TestRegistry_GetOrCreate_WhenFactoryReturnsNil_ReturnsErrFactoryReturnedNil(t *testing.T) {
	t.Parallel()

	r := callgate.NewRegistry()

	factory := func() callgate.CallGate {
		return nil
	}

	gate, err := r.GetOrCreateWithFactory("group-nil", factory)
	if gate != nil {
		t.Fatalf("expected nil gate, got non-nil")
	}

	if err == nil {
		t.Fatalf("expected error, got nil")
	}

	if !errors.Is(err, callgate.ErrFactoryReturnedNil) {
		t.Fatalf("expected ErrFactoryReturnedNil, got: %v", err)
	}
}

func TestRegistry_GetOrCreate_ConcurrentSameGroup_FactoryCalledOnce(t *testing.T) {
	t.Parallel()

	r := callgate.NewRegistry()

	const n = 50

	var created int32

	createdGate := testGate{id: 1}

	factory := func() callgate.CallGate {
		atomic.AddInt32(&created, 1)

		return createdGate
	}

	start := make(chan struct{})

	var wg sync.WaitGroup

	wg.Add(n)

	results := make([]callgate.CallGate, n)
	errs := make([]error, n)

	for i := range n {
		go func(i int) {
			defer wg.Done()

			<-start

			g, e := r.GetOrCreateWithFactory("group-concurrent", factory)

			results[i] = g
			errs[i] = e
		}(i)
	}

	close(start)

	wg.Wait()

	if atomic.LoadInt32(&created) != 1 {
		t.Fatalf("expected factory to be called once, got %d", created)
	}

	first := results[0]
	if first == nil {
		t.Fatalf("expected non-nil gate")
	}

	for i := range n {
		if errs[i] != nil {
			t.Fatalf("unexpected error at %d: %v", i, errs[i])
		}

		if results[i] != first {
			t.Fatalf("expected same gate instance for all calls; mismatch at %d", i)
		}
	}
}

func TestRegistry_GetOrCreate_DifferentGroups_CreateDistinctInstances(t *testing.T) {
	t.Parallel()

	r := callgate.NewRegistry()

	factory := newTestGateID()

	gateA, err := r.GetOrCreateWithFactory("group-a", factory)
	if err != nil {
		t.Fatalf("GetOrCreateWithFactory group-a error: %v", err)
	}

	gateB, err := r.GetOrCreateWithFactory("group-b", factory)
	if err != nil {
		t.Fatalf("GetOrCreateWithFactory group-b error: %v", err)
	}

	if gateA == nil || gateB == nil {
		t.Fatalf("expected non-nil gates")
	}

	if gateA == gateB {
		t.Fatalf("expected different instances for different groups")
	}
}
