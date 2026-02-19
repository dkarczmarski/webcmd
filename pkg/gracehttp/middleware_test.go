package gracehttp_test

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/dkarczmarski/webcmd/pkg/gracehttp"
)

// RejectOnShutdown wrapper behavior (unit-level, no network):
//   - before shutdown: passes to next
//   - after shutdown: returns 503 and does NOT call next
func TestRejectOnShutdown_Wrapper(t *testing.T) {
	t.Parallel()

	var called atomic.Int32

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called.Add(1)
		w.WriteHeader(http.StatusOK)
	})

	shutdownCh := make(chan struct{})
	h := gracehttp.RejectOnShutdown(shutdownCh, next)

	// Before shutdown
	rr1 := httptest.NewRecorder()
	req1 := httptest.NewRequest(http.MethodGet, "http://example/", nil)
	h.ServeHTTP(rr1, req1)

	if rr1.Code != http.StatusOK {
		t.Fatalf("before shutdown expected 200, got %d", rr1.Code)
	}

	if called.Load() != 1 {
		t.Fatalf("expected next to be called once, got %d", called.Load())
	}

	// After shutdown
	close(shutdownCh)

	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodGet, "http://example/", nil)
	h.ServeHTTP(rr2, req2)

	if rr2.Code != http.StatusServiceUnavailable {
		t.Fatalf("after shutdown expected 503, got %d", rr2.Code)
	}

	if called.Load() != 1 {
		t.Fatalf("expected next NOT to be called after shutdown; calls=%d", called.Load())
	}
}
