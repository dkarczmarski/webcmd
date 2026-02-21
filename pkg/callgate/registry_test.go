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

func TestRegistry_WithDefaults(t *testing.T) {
	t.Parallel()

	r := callgate.NewRegistry(callgate.WithDefaults())

	if r == nil {
		t.Fatal("expected non-nil registry")
	}
}

type mockFactoryProvider struct {
	getFactoryFn func(name string) (callgate.Factory, error)
	calledName   string
	calledCount  int32
}

func (m *mockFactoryProvider) GetFactory(name string) (callgate.Factory, error) {
	atomic.AddInt32(&m.calledCount, 1)
	m.calledName = name

	return m.getFactoryFn(name)
}

func WithMockProvider(provider callgate.FactoryProvider) callgate.RegistryOption {
	return func(cfg *callgate.RegistryConfig) {
		cfg.FactoryProvider = provider
	}
}

func TestRegistry_GetOrCreate_Cases(t *testing.T) {
	t.Parallel()

	t.Run("returns ErrBadConfiguration when factoryProvider is nil", func(t *testing.T) {
		t.Parallel()

		r := callgate.NewRegistry() // default has nil provider unless WithDefaults is used

		gate, err := r.GetOrCreate("group", "name")
		if gate != nil {
			t.Error("expected nil gate")
		}

		if !errors.Is(err, callgate.ErrBadConfiguration) {
			t.Errorf("expected ErrBadConfiguration, got: %v", err)
		}
	})

	t.Run("calls factoryProvider with correct name", func(t *testing.T) {
		t.Parallel()

		mock := &mockFactoryProvider{
			getFactoryFn: func(_ string) (callgate.Factory, error) {
				return nil, callgate.ErrInvalidCallGateMode
			},
		}

		r := callgate.NewRegistry(WithMockProvider(mock))

		const testName = "test-name"

		_, _ = r.GetOrCreate("group", testName)

		if mock.calledCount != 1 {
			t.Errorf("expected provider to be called once, got %d", mock.calledCount)
		}

		if mock.calledName != testName {
			t.Errorf("expected provider to be called with %q, got %q", testName, mock.calledName)
		}
	})

	t.Run("propagates error from factoryProvider", func(t *testing.T) {
		t.Parallel()

		expectedErr := errors.New("provider error")
		mock := &mockFactoryProvider{
			getFactoryFn: func(_ string) (callgate.Factory, error) {
				return nil, expectedErr
			},
		}

		r := callgate.NewRegistry(WithMockProvider(mock))

		gate, err := r.GetOrCreate("group", "name")
		if gate != nil {
			t.Error("expected nil gate")
		}

		if !errors.Is(err, expectedErr) {
			t.Errorf("expected error %v, got %v", expectedErr, err)
		}
	})

	t.Run("delegates to GetOrCreateWithFactory when factory is returned", func(t *testing.T) {
		t.Parallel()

		var factoryCalled int32

		myGate := testGate{id: 123}
		factory := func() callgate.CallGate {
			atomic.AddInt32(&factoryCalled, 1)

			return myGate
		}

		mock := &mockFactoryProvider{
			getFactoryFn: func(_ string) (callgate.Factory, error) {
				return factory, nil
			},
		}

		r := callgate.NewRegistry(WithMockProvider(mock))

		gate, err := r.GetOrCreate("group-x", "name-x")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if gate != myGate {
			t.Errorf("expected gate %v, got %v", myGate, gate)
		}

		if atomic.LoadInt32(&factoryCalled) != 1 {
			t.Errorf("expected factory to be called, got %d", factoryCalled)
		}
	})

	t.Run("delegates to GetOrCreateWithFactory even when factory is nil", func(t *testing.T) {
		t.Parallel()

		dummyNilFactory := func() callgate.CallGate { return nil }

		mock := &mockFactoryProvider{
			getFactoryFn: func(_ string) (callgate.Factory, error) {
				return dummyNilFactory, nil
			},
		}

		r := callgate.NewRegistry(WithMockProvider(mock))

		// When group doesn't exist and factory returns nil, GetOrCreateWithFactory returns ErrFactoryReturnedNil
		gate, err := r.GetOrCreate("non-existent-group", "name")
		if gate != nil {
			t.Error("expected nil gate")
		}

		if !errors.Is(err, callgate.ErrFactoryReturnedNil) {
			t.Errorf("expected ErrFactoryReturnedNil, got %v", err)
		}
	})
}
