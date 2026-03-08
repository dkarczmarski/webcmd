package server_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dkarczmarski/webcmd/pkg/config"
	"github.com/dkarczmarski/webcmd/pkg/executor"
	"github.com/dkarczmarski/webcmd/pkg/httpx"
	"github.com/dkarczmarski/webcmd/pkg/processrunner"
	"github.com/dkarczmarski/webcmd/pkg/server"
)

func TestExecutionHandler_HappyPath_Stream(t *testing.T) {
	t.Parallel()

	fr := &fakeRunner{}
	fr.cmd = &fakeCommand{
		pid: 123,
		onStart: func(c *fakeCommand) {
			c.mu.Lock()
			defer c.mu.Unlock()
			_, _ = c.stdout.Write([]byte("process output"))
		},
	}

	pr := processrunner.New(fr)
	ge := newFakeGateExecutor()

	handler := server.ExecutionHandler(executor.New(pr, ge))
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	cmdCfg := &config.URLCommand{
		URL: "POST /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo\n{{.url.name}}\n{{.headers.X_Test}}\n{{.body.text}}",
			ExecutionMode:   "stream",
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/exec?name=test-name", strings.NewReader("test-body"))
	req.Header.Set("X-Test", "test-header")
	req = req.WithContext(server.WithURLCommand(req.Context(), cmdCfg))

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
	ge := newFakeGateExecutor()

	handler := server.ExecutionHandler(executor.New(pr, ge))
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	cmdCfg := &config.URLCommand{
		URL: "POST /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo '{{.body.text}}'",
			ExecutionMode:   "buffered",
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/exec", nil)
	req = req.WithContext(server.WithURLCommand(req.Context(), cmdCfg))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%q", rr.Code, rr.Body.String())
	}

	if rr.Body.String() != "empty body output" {
		t.Fatalf("expected body %q, got %q", "empty body output", rr.Body.String())
	}

	gotCmd, gotArgs := fr.SnapshotCommand()
	if gotCmd != "echo ''" || len(gotArgs) != 0 {
		t.Fatalf("expected Command(%q) with no args, got cmd=%q args=%v", "echo ''", gotCmd, gotArgs)
	}
}

func TestExecutionHandler_NoCommandInContext_404(t *testing.T) {
	t.Parallel()

	fr := &fakeRunner{}
	pr := processrunner.New(fr)
	ge := newFakeGateExecutor()

	handler := server.ExecutionHandler(executor.New(pr, ge))
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
	ge := newFakeGateExecutor()

	handler := server.ExecutionHandler(executor.New(pr, ge))
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	cmdCfg := &config.URLCommand{
		URL: "GET /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo {{.url.a}}",
			ExecutionMode:   "buffered",
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/exec?a=1&a=2", nil)
	req = req.WithContext(server.WithURLCommand(req.Context(), cmdCfg))

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
	ge := newFakeGateExecutor()

	handler := server.ExecutionHandler(executor.New(pr, ge))
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	cmdCfg := &config.URLCommand{
		URL: "GET /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo '{{.headers.X_Test_Header}}' '{{.headers.X_Test}}'",
			ExecutionMode:   "buffered",
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/exec", nil)
	req.Header.Add("X-Test-Header", "a")
	req.Header.Add("X-Test", "val1")
	req.Header.Add("X-Test", "val2")
	req = req.WithContext(server.WithURLCommand(req.Context(), cmdCfg))

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
	ge := newFakeGateExecutor()

	handler := server.ExecutionHandler(executor.New(pr, ge))
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	cmdCfg := &config.URLCommand{
		URL: "POST /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo",
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/exec", io.NopCloser(&errorReader{}))
	req = req.WithContext(server.WithURLCommand(req.Context(), cmdCfg))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d body=%q", rr.Code, rr.Body.String())
	}

	msg := rr.Header().Get("X-Error-Message")
	if !strings.Contains(msg, "failed to read request body") || !strings.Contains(msg, "read error") {
		t.Fatalf("expected X-Error-Message to contain read failure, got %q", msg)
	}
}

func TestExecutionHandler_BodyAsJSON_Invalid_400(t *testing.T) {
	t.Parallel()

	fr := &fakeRunner{}
	pr := processrunner.New(fr)
	ge := newFakeGateExecutor()

	handler := server.ExecutionHandler(executor.New(pr, ge))
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	trueVal := true
	cmdCfg := &config.URLCommand{
		URL: "POST /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo",
			Params: config.ParamsConfig{
				BodyAsJSON: &trueVal,
			},
		},
	}

	req := httptest.NewRequest(http.MethodPost, "/exec", strings.NewReader(`{invalid json}`))
	req = req.WithContext(server.WithURLCommand(req.Context(), cmdCfg))

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
	ge := newFakeGateExecutor()

	handler := server.ExecutionHandler(executor.New(pr, ge))
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	trueVal := true
	cmdCfg := &config.URLCommand{
		URL: "POST /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo",
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
			req = req.WithContext(server.WithURLCommand(req.Context(), cmdCfg))

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
	ge := newFakeGateExecutor()

	handler := server.ExecutionHandler(executor.New(pr, ge))
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

			cmdCfg := &config.URLCommand{
				URL: "GET /exec",
				CommandConfig: config.CommandConfig{
					CommandTemplate: tc.template,
				},
			}

			req := httptest.NewRequest(http.MethodGet, "/exec?name=test", nil)
			req = req.WithContext(server.WithURLCommand(req.Context(), cmdCfg))

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
	ge := newFakeGateExecutor()

	handler := server.ExecutionHandler(executor.New(pr, ge))
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	cmdCfg := &config.URLCommand{
		URL: "GET /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo hello",
			ExecutionMode:   "stream",
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/exec", nil)
	req = req.WithContext(server.WithURLCommand(req.Context(), cmdCfg))

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

func TestExecutionHandler_UnknownExecutionMode_500(t *testing.T) {
	t.Parallel()

	fr := &fakeRunner{}
	pr := processrunner.New(fr)
	ge := newFakeGateExecutor()

	handler := server.ExecutionHandler(executor.New(pr, ge))
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	cmdCfg := &config.URLCommand{
		URL: "GET /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo hello",
			ExecutionMode:   "invalid",
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/exec", nil)
	req = req.WithContext(server.WithURLCommand(req.Context(), cmdCfg))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("expected status 500, got %d body=%q", rr.Code, rr.Body.String())
	}

	msg := rr.Header().Get("X-Error-Message")
	if !strings.Contains(msg, "unknown execution mode") {
		t.Fatalf("expected X-Error-Message to contain %q, got %q", "unknown execution mode", msg)
	}
}
