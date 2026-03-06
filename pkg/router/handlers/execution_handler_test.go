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
		onStart: func(c *fakeCommand) {
			c.mu.Lock()
			defer c.mu.Unlock()
			_, _ = c.stdout.Write([]byte("process output"))
		},
	}

	pr := processrunner.New(fr)
	ge := newFakeGateExecutor()

	handler := handlers.ExecutionHandlerWithDeps(pr, ge)
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

	handler := handlers.ExecutionHandlerWithDeps(pr, ge)
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
	if gotCmd != "echo ''" || len(gotArgs) != 0 {
		t.Fatalf("expected Command(%q) with no args, got cmd=%q args=%v", "echo ''", gotCmd, gotArgs)
	}
}

func TestExecutionHandler_NoCommandInContext_404(t *testing.T) {
	t.Parallel()

	fr := &fakeRunner{}
	pr := processrunner.New(fr)
	ge := newFakeGateExecutor()

	handler := handlers.ExecutionHandlerWithDeps(pr, ge)
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

	handler := handlers.ExecutionHandlerWithDeps(pr, ge)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	cmdCfg := &config.URLCommand{
		URL: "GET /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo {{.url.a}}",
			OutputType:      "text",
		},
	}

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
	ge := newFakeGateExecutor()

	handler := handlers.ExecutionHandlerWithDeps(pr, ge)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	cmdCfg := &config.URLCommand{
		URL: "GET /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo '{{.headers.X_Test_Header}}' '{{.headers.X_Test}}'",
			OutputType:      "text",
		},
	}

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
	ge := newFakeGateExecutor()

	handler := handlers.ExecutionHandlerWithDeps(pr, ge)
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
	if !strings.Contains(msg, "failed to read request body") || !strings.Contains(msg, "read error") {
		t.Fatalf("expected X-Error-Message to contain read failure, got %q", msg)
	}
}

func TestExecutionHandler_BodyAsJSON_Invalid_400(t *testing.T) {
	t.Parallel()

	fr := &fakeRunner{}
	pr := processrunner.New(fr)
	ge := newFakeGateExecutor()

	handler := handlers.ExecutionHandlerWithDeps(pr, ge)
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
	ge := newFakeGateExecutor()

	handler := handlers.ExecutionHandlerWithDeps(pr, ge)
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
	ge := newFakeGateExecutor()

	handler := handlers.ExecutionHandlerWithDeps(pr, ge)
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
	ge := newFakeGateExecutor()

	handler := handlers.ExecutionHandlerWithDeps(pr, ge)
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
	ge := newFakeGateExecutor()

	handler := handlers.ExecutionHandlerWithDeps(pr, ge)
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

	block := make(chan struct{})

	fr := &fakeRunner{}
	fr.cmd = &fakeCommand{pid: 123, waitBlock: block}
	pr := processrunner.New(fr)
	ge := newFakeGateExecutor()

	handler := handlers.ExecutionHandlerWithDeps(pr, ge)
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

	done := make(chan struct{})
	go func() {
		h.ServeHTTP(rr, req)
		close(done)
	}()

	select {
	case <-done:
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

	close(block)
}

func TestExecutionHandler_CallGate_InvalidMode_500(t *testing.T) {
	t.Parallel()

	fr := &fakeRunner{}
	pr := processrunner.New(fr)
	ge := newFakeGateExecutor()

	handler := handlers.ExecutionHandlerWithDeps(pr, ge)
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
	ge := newFakeGateExecutor()

	handler := handlers.ExecutionHandlerWithDeps(pr, ge)
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

	release := ge.hold("single", "test-group")
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
	ge := newFakeGateExecutor()

	handler := handlers.ExecutionHandlerWithDeps(pr, ge)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	cmdCfg1 := &config.URLCommand{
		URL: "GET /exec1",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo hello",
			OutputType:      "text",
			CallGate: &config.CallGateConfig{
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

	release := ge.hold("single", "GET /exec1")
	defer release()

	req1 := httptest.NewRequest(http.MethodGet, "/exec1", nil)
	req1 = req1.WithContext(context.WithValue(req1.Context(), handlers.URLCommandKey, cmdCfg1))
	rr1 := httptest.NewRecorder()
	h.ServeHTTP(rr1, req1)

	if rr1.Code != http.StatusTooManyRequests {
		t.Fatalf("expected status 429 for /exec1, got %d body=%q", rr1.Code, rr1.Body.String())
	}

	req2 := httptest.NewRequest(http.MethodGet, "/exec2", nil)
	req2 = req2.WithContext(context.WithValue(req2.Context(), handlers.URLCommandKey, cmdCfg2))
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, req2)

	if rr2.Code != http.StatusOK {
		t.Fatalf("expected status 200 for /exec2, got %d body=%q", rr2.Code, rr2.Body.String())
	}
}

func TestExecutionHandler_CallGate_EmptyGroupName_SharedAcrossURLs(t *testing.T) {
	t.Parallel()

	fr := &fakeRunner{}
	pr := processrunner.New(fr)
	ge := newFakeGateExecutor()

	handler := handlers.ExecutionHandlerWithDeps(pr, ge)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	cmdCfg1 := &config.URLCommand{
		URL: "GET /exec1",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo hello",
			OutputType:      "text",
			CallGate: &config.CallGateConfig{
				GroupName: ptrString(""),
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
				GroupName: ptrString(""),
				Mode:      "single",
			},
		},
	}

	release := ge.hold("single", "")
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

func TestExecutionHandler_CallGate_SharedGroupName_SharedAcrossURLs(t *testing.T) {
	t.Parallel()

	fr := &fakeRunner{}
	pr := processrunner.New(fr)
	ge := newFakeGateExecutor()

	handler := handlers.ExecutionHandlerWithDeps(pr, ge)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

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

	release := ge.hold("single", "shared")
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
	ge := newFakeGateExecutor()

	handler := handlers.ExecutionHandlerWithDeps(pr, ge)
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

	if rr.Header().Get("X-Exit-Code") != "7" {
		t.Fatalf("expected X-Exit-Code=7, got %q", rr.Header().Get("X-Exit-Code"))
	}

	if rr.Header().Get("X-Error-Message") != "" {
		t.Fatalf("expected empty X-Error-Message, got %q", rr.Header().Get("X-Error-Message"))
	}
}
