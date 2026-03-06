package gracehttp_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/dkarczmarski/webcmd/pkg/gracehttp"
)

const (
	testReadyTimeout   = 2 * time.Second
	testRequestTimeout = 2 * time.Second
)

// Shutdown() closes ShutdownRequested() and cancels AppContext().
func TestGracefulShutdown_ShutdownClosesChannelsAndCancelsContext(t *testing.T) {
	t.Parallel()

	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	ts := newTestServer(t, h, 200*time.Millisecond)

	if err := ts.srv.Shutdown(); err != nil {
		t.Fatalf("Shutdown(): %v", err)
	}

	select {
	case <-ts.srv.ShutdownRequested():
		// ok
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("ShutdownRequested() was not closed")
	}

	select {
	case <-ts.srv.AppContext().Done():
		// ok
	default:
		t.Fatalf("AppContext() was not canceled")
	}

	if !errors.Is(ts.srv.AppContext().Err(), context.Canceled) {
		t.Fatalf("expected AppContext().Err()==context.Canceled, got %v", ts.srv.AppContext().Err())
	}
}

// Run() returns nil after Shutdown().
func TestGracefulShutdown_RunReturnsNilAfterShutdown(t *testing.T) {
	t.Parallel()

	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	ts := newTestServer(t, h, 300*time.Millisecond)

	if err := ts.srv.Shutdown(); err != nil {
		t.Fatalf("Shutdown(): %v", err)
	}

	err := requireRunEnds(t, ts.runErrC, 2*time.Second)
	if err != nil {
		t.Fatalf("Run() expected nil, got %v", err)
	}
}

// 1st signal initiates graceful shutdown: closes channels and Run() exits nil.
func TestGracefulShutdown_FirstSignalInitiatesGracefulShutdown(t *testing.T) {
	t.Parallel()

	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	ts := newTestServer(t, h, 300*time.Millisecond)

	ts.sigCh <- os.Interrupt

	select {
	case <-ts.srv.ShutdownRequested():
		// ok
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("ShutdownRequested() was not closed after signal")
	}

	select {
	case <-ts.srv.AppContext().Done():
		// ok
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("AppContext() not canceled after signal")
	}

	err := requireRunEnds(t, ts.runErrC, 2*time.Second)
	if err != nil {
		t.Fatalf("Run() expected nil, got %v", err)
	}
}

// The request finishes during the grace period.
func TestGracefulShutdown_RequestFinishesDuringGracePeriod(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	finishDelay := 60 * time.Millisecond

	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		time.Sleep(finishDelay)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("done"))
	})

	grace := 400 * time.Millisecond
	ts := newTestServer(t, h, grace)

	respC := make(chan *http.Response, 1)
	errC := make(chan error, 1)

	go func() {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.url+"/", nil)

		resp, err := ts.client.Do(req)
		if err != nil {
			errC <- err

			return
		}

		select {
		case respC <- resp:
		case <-t.Context().Done():
			_ = resp.Body.Close()
		}
	}()

	select {
	case <-started:
		// ok
	case <-time.After(300 * time.Millisecond):
		t.Fatalf("handler did not start")
	}

	// Initiate shutdown while request is in-flight
	if err := ts.srv.Shutdown(); err != nil {
		t.Fatalf("Shutdown(): %v", err)
	}

	// Request should still complete successfully
	select {
	case err := <-errC:
		t.Fatalf("request failed: %v", err)
	case resp := <-respC:
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)

		if resp.StatusCode != http.StatusOK {
			t.Fatalf("expected 200, got %d, body=%q", resp.StatusCode, string(b))
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("request did not complete")
	}
}

// In-flight request exceeds grace period: Shutdown returns a wrapped ErrShutdown and the server is closed.
func TestGracefulShutdown_TimeoutReturnsErrorAndCloses(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})
	unblock := make(chan struct{})

	// Handler intentionally ignores r.Context().Done() and blocks until we manually unblock it.
	// This guarantees that Shutdown() cannot complete within the configured grace period.
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-unblock
		w.WriteHeader(http.StatusOK)
	})

	grace := 50 * time.Millisecond
	ts := newTestServer(t, h, grace)

	reqErrC := make(chan error, 1)
	go func() {
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.url+"/", nil)

		resp, err := ts.client.Do(req)
		if err != nil {
			reqErrC <- err

			return
		}

		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		reqErrC <- fmt.Errorf("unexpected success: status=%d", resp.StatusCode)
	}()

	select {
	case <-started:
		// ok
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("handler did not start")
	}

	// Trigger graceful shutdown.
	// Since the handler does not exit, this should exceed the grace timeout.
	err := ts.srv.Shutdown()
	if err == nil {
		close(unblock)
		t.Fatalf("expected shutdown error due to grace timeout, got nil")
	}

	if !errors.Is(err, gracehttp.ErrShutdown) {
		close(unblock) // cleanup
		t.Fatalf("expected ErrShutdown, got %v", err)
	}

	close(unblock)

	select {
	case rerr := <-reqErrC:
		if rerr == nil {
			t.Fatalf("expected request error, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("request did not finish after shutdown")
	}
}

// After shutdown, new connections should fail.
func TestGracefulShutdown_NewConnectionsFailAfterShutdown(t *testing.T) {
	t.Parallel()

	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	ts := newTestServer(t, h, 50*time.Millisecond)

	if err := ts.srv.Shutdown(); err != nil {
		t.Fatalf("Shutdown failed: %v", err)
	}

	// New connections should fail immediately because Shutdown() closes the listener.
	req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.url+"/", nil)

	resp, err := ts.client.Do(req)
	if err == nil {
		defer resp.Body.Close()
		t.Fatalf("expected connection failure after shutdown, got nil error")
	}
}

// Second signal forces hard close (Close() + ln.Close()): a blocked request should be cut quickly.
func TestGracefulShutdown_SecondSignalForcesHardClose(t *testing.T) {
	t.Parallel()

	started := make(chan struct{})

	h := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	})

	ts := newTestServer(t, h, 5*time.Second)

	reqCtx, cancel := context.WithTimeout(t.Context(), 4*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, ts.url+"/", nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}

	reqErrC := make(chan error, 1)
	go func() {
		resp, err := ts.client.Do(req)
		if err != nil {
			reqErrC <- err

			return
		}

		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		reqErrC <- fmt.Errorf("unexpected success: status=%d", resp.StatusCode)
	}()

	select {
	case <-started:
		// ok
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("handler did not start")
	}

	// 1st signal -> graceful shutdown initiated
	ts.sigCh <- os.Interrupt

	select {
	case <-ts.srv.ShutdownRequested():
		// ok
	case <-time.After(300 * time.Millisecond):
		t.Fatalf("ShutdownRequested not closed after first signal")
	}

	// 2nd signal -> force close
	ts.sigCh <- os.Interrupt

	// The hanging request should fail relatively quickly after hard close.
	select {
	case rerr := <-reqErrC:
		if rerr == nil {
			t.Fatalf("expected request error after hard close, got nil")
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("request did not fail after second signal hard close")
	}

	_ = requireRunEnds(t, ts.runErrC, 3*time.Second)
}

// Concurrent Shutdown() and first signal: no panic, Run exits, shutdown channel closed.
func TestGracefulShutdown_ConcurrentShutdownAndSignal_NoPanic(t *testing.T) {
	t.Parallel()

	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	ts := newTestServer(t, h, 300*time.Millisecond)

	var wg sync.WaitGroup

	wg.Add(2)

	go func() {
		defer wg.Done()

		_ = ts.srv.Shutdown()
	}()

	go func() {
		defer wg.Done()

		ts.sigCh <- os.Interrupt
	}()

	wg.Wait()

	select {
	case <-ts.srv.ShutdownRequested():
		// ok
	case <-time.After(300 * time.Millisecond):
		t.Fatalf("ShutdownRequested not closed")
	}

	_ = requireRunEnds(t, ts.runErrC, 2*time.Second)
}

type testSrv struct {
	srv     *gracehttp.Server
	sigCh   chan os.Signal
	runErrC chan error
	url     string
	client  *http.Client
	ln      net.Listener
}

func newTestServer(t *testing.T, h http.Handler, grace time.Duration) *testSrv {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	sigCh := make(chan os.Signal, 2)

	mux := http.NewServeMux()
	mux.Handle("/",
		h,
	)
	mux.HandleFunc("/__ready", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	allOpts := []gracehttp.Option{
		gracehttp.WithListener(ln),
		gracehttp.WithHandler(mux),
		gracehttp.WithSignalChan(sigCh),
		gracehttp.WithGrace(grace),
	}

	srv, err := gracehttp.New(allOpts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ts := &testSrv{
		srv:     srv,
		sigCh:   sigCh,
		runErrC: make(chan error, 1),
		url:     "http://" + srv.Addr(),
		client: &http.Client{
			Timeout: testRequestTimeout,
		},
		ln: ln,
	}

	go func() {
		ts.runErrC <- srv.Run()
	}()

	waitForServerReady(t, ts.client, ts.url, testReadyTimeout)

	t.Cleanup(func() {
		// Best-effort shutdown if still running
		_ = srv.Shutdown()
	})

	return ts
}

func waitForServerReady(t *testing.T, client *http.Client, url string, timeout time.Duration) {
	t.Helper()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url+"/__ready", nil)
		if err != nil {
			t.Fatalf("NewRequest: %v", err)
		}

		resp, err := client.Do(req)
		if err == nil {
			_ = resp.Body.Close()

			if resp.StatusCode == http.StatusOK {
				return
			}
		}

		time.Sleep(20 * time.Millisecond)
	}

	t.Fatalf("server not ready on %s within %s", url, timeout)
}

func requireRunEnds(t *testing.T, ch <-chan error, within time.Duration) error {
	t.Helper()

	select {
	case err := <-ch:
		return err
	case <-time.After(within):
		t.Fatalf("Run() did not exit within %s", within)

		return nil
	}
}
