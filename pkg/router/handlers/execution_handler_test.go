package handlers_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/dkarczmarski/webcmd/pkg/callgate"
	"github.com/dkarczmarski/webcmd/pkg/config"
	"github.com/dkarczmarski/webcmd/pkg/httpx"
	"github.com/dkarczmarski/webcmd/pkg/processrunner"
	"github.com/dkarczmarski/webcmd/pkg/router/handlers"
)

func TestExecutionHandler_HappyPath_Stream(t *testing.T) {
	t.Parallel()

	fr := &fakeRunner{}
	fr.cmd = &fakeCommand{
		pid: 123,
		// Simulate a running process writing to stdout when the command starts.
		// This allows the handler to stream the output to the HTTP response.
		onStart: func(c *fakeCommand) {
			c.mu.Lock()
			defer c.mu.Unlock()
			_, _ = c.stdout.Write([]byte("process output"))
		},
	}

	pr := processrunner.New(fr)
	handler := handlers.ExecutionHandlerWithProcessRunner(pr, nil)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	cmdCfg := &config.URLCommand{
		URL: "POST /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo\n{{.url.name}}\n{{.headers.X_Test}}\n{{.body.text}}",
			OutputType:      "stream",
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/exec?name=test-name", strings.NewReader("test-body"))
	req.Header.Set("X-Test", "test-header")
	req = req.WithContext(context.WithValue(req.Context(), handlers.URLCommandKey, cmdCfg))

	// flusherRecorder lets us verify that streaming handlers call Flush(),
	// which is required to send data incrementally to the client.
	rr := &flusherRecorder{ResponseRecorder: httptest.NewRecorder()}
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%q", rr.Code, rr.Body.String())
	}

	if rr.Body.String() != "process output" {
		t.Fatalf("expected body %q, got %q", "process output", rr.Body.String())
	}

	if ct := rr.Header().Get("Content-Type"); ct != "text/plain; charset=utf-8" {
		t.Fatalf("expected Content-Type %q, got %q", "text/plain; charset=utf-8", ct)
	}

	if !rr.flushed {
		t.Fatalf("expected Flush() to be called")
	}

	// Verify that the template was rendered correctly and split into command + args.
	gotCmd, gotArgs := fr.SnapshotCommand()

	if gotCmd != "echo" {
		t.Fatalf("expected command %q, got %q (args=%v)", "echo", gotCmd, gotArgs)
	}

	if strings.Join(gotArgs, "|") != "test-name|test-header|test-body" {
		t.Fatalf("expected args %v, got %v", []string{"test-name", "test-header", "test-body"}, gotArgs)
	}
}

func TestExecutionHandler_Text_EmptyBody(t *testing.T) {
	t.Parallel()

	fr := &fakeRunner{}
	fr.cmd = &fakeCommand{
		pid: 123,
		onStart: func(c *fakeCommand) {
			c.mu.Lock()
			defer c.mu.Unlock()
			_, _ = c.stdout.Write([]byte("empty body output"))
		},
	}

	pr := processrunner.New(fr)
	handler := handlers.ExecutionHandlerWithProcessRunner(pr, nil)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	cmdCfg := &config.URLCommand{
		URL: "POST /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo '{{.body.text}}'",
			OutputType:      "text",
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/exec", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.URLCommandKey, cmdCfg))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%q", rr.Code, rr.Body.String())
	}

	if rr.Body.String() != "empty body output" {
		t.Fatalf("expected body %q, got %q", "empty body output", rr.Body.String())
	}

	gotCmd, gotArgs := fr.SnapshotCommand()
	// Template is a single line => the whole rendered output becomes the command (no args).
	if gotCmd != "echo ''" || len(gotArgs) != 0 {
		t.Fatalf("expected Command(%q) with no args, got cmd=%q args=%v", "echo ''", gotCmd, gotArgs)
	}
}

func TestExecutionHandler_NoCommandInContext_404(t *testing.T) {
	t.Parallel()

	fr := &fakeRunner{}
	pr := processrunner.New(fr)

	handler := handlers.ExecutionHandlerWithProcessRunner(pr, nil)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	req := httptest.NewRequest(http.MethodGet, "/exec", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected status 404, got %d", rr.Code)
	}

	if msg := rr.Header().Get("X-Error-Message"); !strings.Contains(msg, "Command not found") {
		t.Fatalf("expected X-Error-Message to contain %q, got %q", "Command not found", msg)
	}
}

func TestExecutionHandler_ExtractParams_Query_FirstValueOnly(t *testing.T) {
	t.Parallel()

	fr := &fakeRunner{}
	pr := processrunner.New(fr)

	handler := handlers.ExecutionHandlerWithProcessRunner(pr, nil)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	cmdCfg := &config.URLCommand{
		URL: "GET /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo {{.url.a}}",
			OutputType:      "text",
		},
	}

	// When multiple query values exist, the handler should use only the first one.
	req := httptest.NewRequest(http.MethodGet, "/exec?a=1&a=2", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.URLCommandKey, cmdCfg))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%q", rr.Code, rr.Body.String())
	}

	gotCmd, gotArgs := fr.SnapshotCommand()
	if gotCmd != "echo 1" || len(gotArgs) != 0 {
		t.Fatalf("expected cmd=%q args=[], got cmd=%q args=%v", "echo 1", gotCmd, gotArgs)
	}
}

func TestExecutionHandler_ExtractParams_Headers_NormalizeAndJoin(t *testing.T) {
	t.Parallel()

	fr := &fakeRunner{}
	pr := processrunner.New(fr)

	handler := handlers.ExecutionHandlerWithProcessRunner(pr, nil)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	cmdCfg := &config.URLCommand{
		URL: "GET /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo '{{.headers.X_Test_Header}}' '{{.headers.X_Test}}'",
			OutputType:      "text",
		},
	}

	// Header names are normalized for templates:
	//   X-Test-Header -> X_Test_Header
	// Multiple values are joined with "; ".
	req := httptest.NewRequest(http.MethodGet, "/exec", nil)
	req.Header.Add("X-Test-Header", "a")
	req.Header.Add("X-Test", "val1")
	req.Header.Add("X-Test", "val2")
	req = req.WithContext(context.WithValue(req.Context(), handlers.URLCommandKey, cmdCfg))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%q", rr.Code, rr.Body.String())
	}

	gotCmd, gotArgs := fr.SnapshotCommand()
	if gotCmd != "echo 'a' 'val1; val2'" || len(gotArgs) != 0 {
		t.Fatalf("expected cmd=%q args=[], got cmd=%q args=%v", "echo 'a' 'val1; val2'", gotCmd, gotArgs)
	}
}

func TestExecutionHandler_BodyReadError_500(t *testing.T) {
	t.Parallel()

	fr := &fakeRunner{}
	pr := processrunner.New(fr)

	handler := handlers.ExecutionHandlerWithProcessRunner(pr, nil)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	cmdCfg := &config.URLCommand{
		URL: "POST /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo",
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/exec", io.NopCloser(&errorReader{}))
	req = req.WithContext(context.WithValue(req.Context(), handlers.URLCommandKey, cmdCfg))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d body=%q", rr.Code, rr.Body.String())
	}

	msg := rr.Header().Get("X-Error-Message")

	if !strings.Contains(msg, "failed to read request body") {
		t.Fatalf("expected X-Error-Message to contain %q, got %q", "failed to read request body", msg)
	}

	if !strings.Contains(msg, "read error") {
		t.Fatalf("expected X-Error-Message to contain %q, got %q", "read error", msg)
	}
}

func TestExecutionHandler_BodyAsJSON_Invalid_400(t *testing.T) {
	t.Parallel()

	fr := &fakeRunner{}
	pr := processrunner.New(fr)

	handler := handlers.ExecutionHandlerWithProcessRunner(pr, nil)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	trueVal := true
	cmdCfg := &config.URLCommand{
		URL: "POST /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo",
			// BodyAsJSON=true means the handler attempts to parse the body as JSON
			// and expose it under .body.json in the template context.
			Params: config.ParamsConfig{
				BodyAsJSON: &trueVal,
			},
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/exec", strings.NewReader(`{invalid json}`))
	req = req.WithContext(context.WithValue(req.Context(), handlers.URLCommandKey, cmdCfg))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d body=%q", rr.Code, rr.Body.String())
	}

	errMsg := rr.Header().Get("X-Error-Message")
	if !strings.Contains(errMsg, "must be a JSON object") {
		t.Fatalf("expected X-Error-Message to contain %q, got %q", "must be a JSON object", errMsg)
	}
}

func TestExecutionHandler_BodyAsJSON_NonObject_400(t *testing.T) {
	t.Parallel()

	fr := &fakeRunner{}
	pr := processrunner.New(fr)

	handler := handlers.ExecutionHandlerWithProcessRunner(pr, nil)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	trueVal := true
	cmdCfg := &config.URLCommand{
		URL: "POST /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo",
			// Non-object JSON values are rejected because templates expect a JSON object.
			Params: config.ParamsConfig{
				BodyAsJSON: &trueVal,
			},
		},
	}

	for _, tc := range []struct {
		name string
		body string
	}{
		{"array", `[1, 2, 3]`},
		{"number", `123`},
		{"string", `"some string"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodPost, "/exec", strings.NewReader(tc.body))
			req = req.WithContext(context.WithValue(req.Context(), handlers.URLCommandKey, cmdCfg))

			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Fatalf("expected status 400, got %d body=%q", rr.Code, rr.Body.String())
			}
		})
	}
}

func TestExecutionHandler_BuildCommand_Error_500(t *testing.T) {
	t.Parallel()

	fr := &fakeRunner{}
	pr := processrunner.New(fr)

	handler := handlers.ExecutionHandlerWithProcessRunner(pr, nil)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	for _, tc := range []struct {
		name     string
		template string
	}{
		{"syntax_error", "echo {{.url.name"},
		{"execution_error", "echo {{.url.name | nonExistentFunc}}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			// Both syntax errors and execution errors should be surfaced as
			// "error building command".
			cmdCfg := &config.URLCommand{
				URL: "GET /exec",
				CommandConfig: config.CommandConfig{
					CommandTemplate: tc.template,
				},
			}

			req := httptest.NewRequest(http.MethodGet, "/exec?name=test", nil)
			req = req.WithContext(context.WithValue(req.Context(), handlers.URLCommandKey, cmdCfg))

			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			if rr.Code != http.StatusInternalServerError {
				t.Fatalf("expected status 500, got %d body=%q", rr.Code, rr.Body.String())
			}

			msg := rr.Header().Get("X-Error-Message")
			if !strings.Contains(msg, "error building command") {
				t.Fatalf("expected X-Error-Message to contain %q, got %q", "error building command", msg)
			}
		})
	}
}

func TestExecutionHandler_Stream_RequiresFlusher_500(t *testing.T) {
	t.Parallel()

	fr := &fakeRunner{}
	pr := processrunner.New(fr)

	handler := handlers.ExecutionHandlerWithProcessRunner(pr, nil)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	cmdCfg := &config.URLCommand{
		URL: "GET /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo hello",
			OutputType:      "stream",
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/exec", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.URLCommandKey, cmdCfg))

	// Streaming responses require http.Flusher.
	// This custom wrapper intentionally removes the Flusher interface
	// to verify that the handler returns an error in this situation.
	type nonFlusher struct{ http.ResponseWriter }

	rr := httptest.NewRecorder()
	h.ServeHTTP(nonFlusher{ResponseWriter: rr}, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d body=%q", rr.Code, rr.Body.String())
	}

	msg := rr.Header().Get("X-Error-Message")
	if !strings.Contains(msg, "streaming not supported") {
		t.Fatalf("expected X-Error-Message to contain %q, got %q", "streaming not supported", msg)
	}
}

func TestExecutionHandler_UnknownOutputType_500(t *testing.T) {
	t.Parallel()

	fr := &fakeRunner{}
	pr := processrunner.New(fr)

	handler := handlers.ExecutionHandlerWithProcessRunner(pr, nil)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	cmdCfg := &config.URLCommand{
		URL: "GET /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo hello",
			OutputType:      "invalid",
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/exec", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.URLCommandKey, cmdCfg))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d body=%q", rr.Code, rr.Body.String())
	}

	msg := rr.Header().Get("X-Error-Message")
	if !strings.Contains(msg, "unknown output type") {
		t.Fatalf("expected X-Error-Message to contain %q, got %q", "unknown output type", msg)
	}
}

func TestExecutionHandler_OutputNone_ReturnsBeforeWait(t *testing.T) {
	t.Parallel()

	// Block Wait() so we can verify that the handler returns immediately
	// when outputType=none (asynchronous execution).
	block := make(chan struct{})

	fr := &fakeRunner{}
	fr.cmd = &fakeCommand{
		pid:       123,
		waitBlock: block,
	}
	pr := processrunner.New(fr)

	handler := handlers.ExecutionHandlerWithProcessRunner(pr, nil)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	cmdCfg := &config.URLCommand{
		URL: "GET /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo hello",
			OutputType:      "none",
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/exec", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.URLCommandKey, cmdCfg))

	rr := httptest.NewRecorder()

	// Run handler in a goroutine because we expect it to return
	// before the command finishes.
	done := make(chan struct{})
	go func() {
		h.ServeHTTP(rr, req)
		close(done)
	}()

	// Handler should return quickly without waiting for Wait().
	// If it blocks, asynchronous execution is broken.
	select {
	case <-done:
		// ok
	case <-time.After(80 * time.Millisecond):
		close(block)
		t.Fatalf("handler did not return quickly for outputType=none")
	}

	if rr.Code != http.StatusOK {
		close(block)
		t.Fatalf("expected status 200, got %d body=%q", rr.Code, rr.Body.String())
	}

	if rr.Body.Len() != 0 {
		close(block)
		t.Fatalf("expected empty body, got %q", rr.Body.String())
	}

	// Cleanup: unblock Wait() so the fake command can finish.
	close(block)
}

func TestExecutionHandler_CallGate_InvalidMode_500(t *testing.T) {
	t.Parallel()

	fr := &fakeRunner{}
	pr := processrunner.New(fr)

	registry := callgate.NewRegistry(callgate.WithDefaults())
	handler := handlers.ExecutionHandlerWithProcessRunner(pr, registry)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	cmdCfg := &config.URLCommand{
		URL: "GET /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo hello",
			OutputType:      "text",
			CallGate: &config.CallGateConfig{
				GroupName: ptrString("test-group"),
				Mode:      "invalid-mode",
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/exec", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.URLCommandKey, cmdCfg))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d body=%q", rr.Code, rr.Body.String())
	}

	msg := rr.Header().Get("X-Error-Message")
	if !strings.Contains(msg, "invalid callgate mode") {
		t.Fatalf("expected X-Error-Message to contain %q, got %q", "invalid callgate mode", msg)
	}
}

func TestExecutionHandler_CallGate_Busy_429(t *testing.T) {
	t.Parallel()

	fr := &fakeRunner{}
	pr := processrunner.New(fr)

	registry := callgate.NewRegistry(callgate.WithDefaults())
	handler := handlers.ExecutionHandlerWithProcessRunner(pr, registry)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	cmdCfg := &config.URLCommand{
		URL: "GET /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo hello",
			OutputType:      "text",
			CallGate: &config.CallGateConfig{
				GroupName: ptrString("test-group"),
				Mode:      "single",
			},
		},
	}

	// Acquire the gate manually to simulate another request already running.
	// The handler should detect this and return HTTP 429.
	gate, _ := registry.GetOrCreate("test-group", "single")
	release, _ := gate.Acquire(t.Context())

	defer release()

	req := httptest.NewRequest(http.MethodGet, "/exec", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.URLCommandKey, cmdCfg))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusTooManyRequests {
		t.Fatalf("expected status 429, got %d body=%q", rr.Code, rr.Body.String())
	}
}

func TestExecutionHandler_CallGate_ImplicitGroupName_IsolatesDifferentURLs(t *testing.T) {
	t.Parallel()

	fr := &fakeRunner{}
	fr.cmd = &fakeCommand{
		pid: 123,
		onStart: func(c *fakeCommand) {
			c.mu.Lock()
			defer c.mu.Unlock()
			_, _ = c.stdout.Write([]byte("ok"))
		},
	}
	pr := processrunner.New(fr)

	registry := callgate.NewRegistry(callgate.WithDefaults())
	handler := handlers.ExecutionHandlerWithProcessRunner(pr, registry)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	cmdCfg1 := &config.URLCommand{
		URL: "GET /exec1",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo hello",
			OutputType:      "text",
			CallGate: &config.CallGateConfig{
				// If GroupName is nil, the handler uses the URL definition (e.g. "GET /exec1")
				// as the implicit callgate group name.
				GroupName: nil,
				Mode:      "single",
			},
		},
	}
	cmdCfg2 := &config.URLCommand{
		URL: "GET /exec2",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo hello",
			OutputType:      "text",
			CallGate: &config.CallGateConfig{
				GroupName: nil,
				Mode:      "single",
			},
		},
	}

	// Block the implicit group for /exec1 only.
	g1, _ := registry.GetOrCreate("GET /exec1", "single")
	release, _ := g1.Acquire(t.Context())

	defer release()

	req1 := httptest.NewRequest(http.MethodGet, "/exec1", nil)
	req1 = req1.WithContext(context.WithValue(req1.Context(), handlers.URLCommandKey, cmdCfg1))
	rr1 := httptest.NewRecorder()
	h.ServeHTTP(rr1, req1)

	if rr1.Code != http.StatusTooManyRequests {
		t.Fatalf("expected status 429 for /exec1, got %d body=%q", rr1.Code, rr1.Body.String())
	}

	// /exec2 uses a different implicit group name and should not be blocked.
	req2 := httptest.NewRequest(http.MethodGet, "/exec2", nil)
	req2 = req2.WithContext(context.WithValue(req2.Context(), handlers.URLCommandKey, cmdCfg2))
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, req2)

	if rr2.Code != http.StatusOK {
		t.Fatalf("expected status 200 for /exec2, got %d body=%q", rr2.Code, rr2.Body.String())
	}
}

func TestExecutionHandler_CallGate_SharedGroupName_SharedAcrossURLs(t *testing.T) {
	t.Parallel()

	fr := &fakeRunner{}
	pr := processrunner.New(fr)

	registry := callgate.NewRegistry(callgate.WithDefaults())
	handler := handlers.ExecutionHandlerWithProcessRunner(pr, registry)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	// When an explicit GroupName is provided, multiple URLs share the same gate.
	shared := "shared"

	cmdCfg1 := &config.URLCommand{
		URL: "GET /exec1",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo hello",
			OutputType:      "text",
			CallGate: &config.CallGateConfig{
				GroupName: &shared,
				Mode:      "single",
			},
		},
	}
	cmdCfg2 := &config.URLCommand{
		URL: "GET /exec2",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo hello",
			OutputType:      "text",
			CallGate: &config.CallGateConfig{
				GroupName: &shared,
				Mode:      "single",
			},
		},
	}

	// Hold the shared gate so both endpoints should be rejected.
	g, _ := registry.GetOrCreate("shared", "single")
	release, _ := g.Acquire(t.Context())

	defer release()

	for _, tc := range []struct {
		path   string
		cmdCfg *config.URLCommand
	}{
		{"/exec1", cmdCfg1},
		{"/exec2", cmdCfg2},
	} {
		req := httptest.NewRequest(http.MethodGet, tc.path, nil)
		req = req.WithContext(context.WithValue(req.Context(), handlers.URLCommandKey, tc.cmdCfg))

		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)

		if rr.Code != http.StatusTooManyRequests {
			t.Fatalf("expected status 429 for %s, got %d body=%q", tc.path, rr.Code, rr.Body.String())
		}
	}
}

func TestExecutionHandler_NonZeroExit_SetsExitCodeHeader(t *testing.T) {
	t.Parallel()

	// Create a real *exec.ExitError to simulate a command exiting with code 7.
	// This ensures the handler correctly extracts exit codes from errors.
	runErr := exec.Command("sh", "-c", "exit 7").Run()

	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) {
		t.Fatalf("expected *exec.ExitError, got %T: %v", runErr, runErr)
	}

	fr := &fakeRunner{}
	fr.cmd = &fakeCommand{
		pid:     123,
		waitErr: exitErr,
		onStart: func(c *fakeCommand) {
			c.mu.Lock()
			defer c.mu.Unlock()
			_, _ = c.stdout.Write([]byte("OUT\n"))
		},
	}
	pr := processrunner.New(fr)

	handler := handlers.ExecutionHandlerWithProcessRunner(pr, nil)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	cmdCfg := &config.URLCommand{
		URL: "GET /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo hello",
			OutputType:      "text",
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/exec", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.URLCommandKey, cmdCfg))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%q", rr.Code, rr.Body.String())
	}

	// Non-zero exit codes are treated as valid results,
	// so the handler returns HTTP 200 and exposes the exit code via header.
	if rr.Header().Get("X-Exit-Code") != "7" {
		t.Fatalf("expected X-Exit-Code=7, got %q", rr.Header().Get("X-Exit-Code"))
	}

	// Non-zero exit is treated as "normal" in this API => no error message.
	if rr.Header().Get("X-Error-Message") != "" {
		t.Fatalf("expected empty X-Error-Message, got %q", rr.Header().Get("X-Error-Message"))
	}
}
