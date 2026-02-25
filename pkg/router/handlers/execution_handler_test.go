package handlers_test

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/dkarczmarski/webcmd/pkg/callgate"
	"github.com/dkarczmarski/webcmd/pkg/config"
	"github.com/dkarczmarski/webcmd/pkg/httpx"
	"github.com/dkarczmarski/webcmd/pkg/router/handlers"
	"github.com/dkarczmarski/webcmd/pkg/router/handlers/internal/mocks"
	"go.uber.org/mock/gomock"
)

type syncedWriter struct {
	io.Writer
	mu *sync.Mutex
}

//nolint:wrapcheck
func (s *syncedWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.Writer.Write(p)
}

func TestExecutionHandler_HappyPath(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockRunner := mocks.NewMockRunner(ctrl)
	mockCmd := mocks.NewMockCommand(ctrl)

	// Setup URLCommand in context
	cmdCfg := &config.URLCommand{
		URL: "POST /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo\n{{.url.name}}\n{{.headers.X_Test}}\n{{.body.text}}",
			OutputType:      "stream",
		},
	}

	// ExecutionHandler expectations
	mockRunner.EXPECT().
		Command("echo", "test-name", "test-header", "test-body").
		Return(mockCmd)

	mockCmd.EXPECT().SetSysProcAttr(gomock.Any())
	mockCmd.EXPECT().SetStdout(gomock.Any()).Do(func(w io.Writer) {
		_, _ = w.Write([]byte("process output"))
	})
	mockCmd.EXPECT().SetStderr(gomock.Any())
	mockCmd.EXPECT().Start().Return(nil)
	mockCmd.EXPECT().Wait().Return(nil)
	mockCmd.EXPECT().ProcessState().Return(nil).AnyTimes()
	mockCmd.EXPECT().Pid().Return(123).AnyTimes()

	handler := handlers.ExecutionHandler(mockRunner, nil)
	// We need to use ErrorSink to get the 200 status code and handle errors
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	req := httptest.NewRequest(http.MethodPost, "/exec?name=test-name", strings.NewReader("test-body"))
	req.Header.Set("X-Test", "test-header")

	// Manually put URLCommand into context as the middleware would do
	ctx := context.WithValue(req.Context(), handlers.URLCommandKey, cmdCfg)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	expectedBody := "process output"
	if rr.Body.String() != expectedBody {
		t.Errorf("expected body %q, got %q", expectedBody, rr.Body.String())
	}

	contentType := rr.Header().Get("Content-Type")
	if !strings.Contains(contentType, "text/plain") {
		t.Errorf("expected Content-Type to contain text/plain, got %q", contentType)
	}
}

func TestExecutionHandler_EmptyBody(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockRunner := mocks.NewMockRunner(ctrl)
	mockCmd := mocks.NewMockCommand(ctrl)

	cmdCfg := &config.URLCommand{
		URL: "POST /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo '{{.body.text}}'",
			OutputType:      "text",
		},
	}

	// For empty body, body.text should be ""
	mockRunner.EXPECT().
		Command("echo ''").
		Return(mockCmd)

	mockCmd.EXPECT().SetSysProcAttr(gomock.Any())
	mockCmd.EXPECT().SetStdout(gomock.Any()).Do(func(w io.Writer) {
		_, _ = w.Write([]byte("empty body output"))
	})
	mockCmd.EXPECT().SetStderr(gomock.Any())
	mockCmd.EXPECT().Start().Return(nil)
	mockCmd.EXPECT().Wait().Return(nil)
	mockCmd.EXPECT().ProcessState().Return(nil).AnyTimes()
	mockCmd.EXPECT().Pid().Return(123).AnyTimes()

	handler := handlers.ExecutionHandler(mockRunner, nil)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	// Body is empty
	req := httptest.NewRequest(http.MethodPost, "/exec", nil)
	ctx := context.WithValue(req.Context(), handlers.URLCommandKey, cmdCfg)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	if rr.Body.String() != "empty body output" {
		t.Errorf("expected output 'empty body output', got %q", rr.Body.String())
	}
}

func TestExecutionHandler_NoCommandInContext(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockRunner := mocks.NewMockRunner(ctrl)

	handler := handlers.ExecutionHandler(mockRunner, nil)
	// Using ErrorSink to translate WebError to HTTP status code
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	// No URLCommand in context
	req := httptest.NewRequest(http.MethodGet, "/exec", nil)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Errorf("expected status 404, got %d", rr.Code)
	}

	if !strings.Contains(rr.Body.String(), "Command not found") {
		t.Errorf("expected body to contain 'Command not found', got %q", rr.Body.String())
	}
}

func TestExecutionHandler_ExtractParams_Query(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockRunner := mocks.NewMockRunner(ctrl)
	mockCmd := mocks.NewMockCommand(ctrl)

	cmdCfg := &config.URLCommand{
		URL: "GET /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo {{.url.a}}",
		},
	}

	// Expect only the first value of 'a' to be used
	mockRunner.EXPECT().
		Command("echo 1").
		Return(mockCmd)

	mockCmd.EXPECT().SetSysProcAttr(gomock.Any())
	mockCmd.EXPECT().SetStdout(gomock.Any())
	mockCmd.EXPECT().SetStderr(gomock.Any())
	mockCmd.EXPECT().Start().Return(nil)
	mockCmd.EXPECT().Wait().Return(nil)
	mockCmd.EXPECT().ProcessState().Return(nil).AnyTimes()
	mockCmd.EXPECT().Pid().Return(123).AnyTimes()

	handler := handlers.ExecutionHandler(mockRunner, nil)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	// URL with multiple values for parameter 'a'
	req := httptest.NewRequest(http.MethodGet, "/exec?a=1&a=2", nil)
	ctx := context.WithValue(req.Context(), handlers.URLCommandKey, cmdCfg)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}

func TestExecutionHandler_ExtractParams_Headers(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockRunner := mocks.NewMockRunner(ctrl)
	mockCmd := mocks.NewMockCommand(ctrl)

	cmdCfg := &config.URLCommand{
		URL: "GET /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo '{{.headers.X_Test_Header}}' '{{.headers.X_Test}}'",
		},
	}

	// Expect:
	// 1. X-Test-Header normalized to X_Test_Header
	// 2. Multiple values for X-Test joined with "; "
	mockRunner.EXPECT().
		Command("echo 'a' 'val1; val2'").
		Return(mockCmd)

	mockCmd.EXPECT().SetSysProcAttr(gomock.Any())
	mockCmd.EXPECT().SetStdout(gomock.Any())
	mockCmd.EXPECT().SetStderr(gomock.Any())
	mockCmd.EXPECT().Start().Return(nil)
	mockCmd.EXPECT().Wait().Return(nil)
	mockCmd.EXPECT().ProcessState().Return(nil).AnyTimes()
	mockCmd.EXPECT().Pid().Return(123).AnyTimes()

	handler := handlers.ExecutionHandler(mockRunner, nil)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	req := httptest.NewRequest(http.MethodGet, "/exec", nil)
	req.Header.Add("X-Test-Header", "a")
	req.Header.Add("X-Test", "val1")
	req.Header.Add("X-Test", "val2")

	ctx := context.WithValue(req.Context(), handlers.URLCommandKey, cmdCfg)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}

type errorReader struct{}

func (e *errorReader) Read(_ []byte) (int, error) {
	return 0, errors.New("read error")
}

func TestExecutionHandler_ExtractParams_BodyReadError(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockRunner := mocks.NewMockRunner(ctrl)

	cmdCfg := &config.URLCommand{
		URL: "POST /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo",
		},
	}

	handler := handlers.ExecutionHandler(mockRunner, nil)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	req := httptest.NewRequest(http.MethodPost, "/exec", &errorReader{})
	ctx := context.WithValue(req.Context(), handlers.URLCommandKey, cmdCfg)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", rr.Code)
	}
}

//nolint:dupl
func TestExecutionHandler_BodyAsJSON_Disabled(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockRunner := mocks.NewMockRunner(ctrl)
	mockCmd := mocks.NewMockCommand(ctrl)

	falseVal := false
	cmdCfg := &config.URLCommand{
		URL: "POST /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo '{{.body.text}}' '{{index .body \"json\"}}'",
			Params: config.ParamsConfig{
				BodyAsJSON: &falseVal,
			},
		},
	}

	// Expect: body.json should be empty/nil, and template will render <no value> for it
	mockRunner.EXPECT().
		Command("echo '{\"a\": 1}' '<no value>'").
		Return(mockCmd)

	mockCmd.EXPECT().SetSysProcAttr(gomock.Any())
	mockCmd.EXPECT().SetStdout(gomock.Any())
	mockCmd.EXPECT().SetStderr(gomock.Any())
	mockCmd.EXPECT().Start().Return(nil)
	mockCmd.EXPECT().Wait().Return(nil)
	mockCmd.EXPECT().ProcessState().Return(nil).AnyTimes()
	mockCmd.EXPECT().Pid().Return(123).AnyTimes()

	handler := handlers.ExecutionHandler(mockRunner, nil)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	req := httptest.NewRequest(http.MethodPost, "/exec", strings.NewReader(`{"a": 1}`))
	ctx := context.WithValue(req.Context(), handlers.URLCommandKey, cmdCfg)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}

//nolint:dupl
func TestExecutionHandler_BodyAsJSON_Valid(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockRunner := mocks.NewMockRunner(ctrl)
	mockCmd := mocks.NewMockCommand(ctrl)

	trueVal := true
	cmdCfg := &config.URLCommand{
		URL: "POST /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo '{{.body.text}}' '{{.body.json.a}}'",
			Params: config.ParamsConfig{
				BodyAsJSON: &trueVal,
			},
		},
	}

	// Expect: body.text is raw string, body.json.a is 1
	mockRunner.EXPECT().
		Command("echo '{\"a\": 1}' '1'").
		Return(mockCmd)

	mockCmd.EXPECT().SetSysProcAttr(gomock.Any())
	mockCmd.EXPECT().SetStdout(gomock.Any())
	mockCmd.EXPECT().SetStderr(gomock.Any())
	mockCmd.EXPECT().Start().Return(nil)
	mockCmd.EXPECT().Wait().Return(nil)
	mockCmd.EXPECT().ProcessState().Return(nil).AnyTimes()
	mockCmd.EXPECT().Pid().Return(123).AnyTimes()

	handler := handlers.ExecutionHandler(mockRunner, nil)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	req := httptest.NewRequest(http.MethodPost, "/exec", strings.NewReader(`{"a": 1}`))
	ctx := context.WithValue(req.Context(), handlers.URLCommandKey, cmdCfg)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}

func TestExecutionHandler_BodyAsJSON_Invalid(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockRunner := mocks.NewMockRunner(ctrl)

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

	handler := handlers.ExecutionHandler(mockRunner, nil)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	// Invalid JSON body
	req := httptest.NewRequest(http.MethodPost, "/exec", strings.NewReader(`{invalid json}`))
	ctx := context.WithValue(req.Context(), handlers.URLCommandKey, cmdCfg)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected status 400, got %d", rr.Code)
	}
}

func TestExecutionHandler_BodyAsJSON_NonObject(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockRunner := mocks.NewMockRunner(ctrl)

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

	testCases := []struct {
		name string
		body string
	}{
		{"array", `[1, 2, 3]`},
		{"number", `123`},
		{"string", `"some string"`},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			handler := handlers.ExecutionHandler(mockRunner, nil)
			h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

			req := httptest.NewRequest(http.MethodPost, "/exec", strings.NewReader(tc.body))
			ctx := context.WithValue(req.Context(), handlers.URLCommandKey, cmdCfg)
			req = req.WithContext(ctx)

			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			if rr.Code != http.StatusBadRequest {
				t.Errorf("%s: expected status 400, got %d", tc.name, rr.Code)
			}
		})
	}
}

func TestExecutionHandler_BuildCommand_Success(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockRunner := mocks.NewMockRunner(ctrl)
	mockCmd := mocks.NewMockCommand(ctrl)

	cmdCfg := &config.URLCommand{
		URL: "GET /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "bash\n-c\necho {{.url.name}}",
		},
	}

	// Expect command: bash, args: [-c, echo test]
	mockRunner.EXPECT().
		Command("bash", "-c", "echo test").
		Return(mockCmd)

	mockCmd.EXPECT().SetSysProcAttr(gomock.Any())
	mockCmd.EXPECT().SetStdout(gomock.Any())
	mockCmd.EXPECT().SetStderr(gomock.Any())
	mockCmd.EXPECT().Start().Return(nil)
	mockCmd.EXPECT().Wait().Return(nil)
	mockCmd.EXPECT().ProcessState().Return(nil).AnyTimes()
	mockCmd.EXPECT().Pid().Return(123).AnyTimes()

	handler := handlers.ExecutionHandler(mockRunner, nil)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	req := httptest.NewRequest(http.MethodGet, "/exec?name=test", nil)
	ctx := context.WithValue(req.Context(), handlers.URLCommandKey, cmdCfg)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}

func TestExecutionHandler_BuildCommand_Error(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockRunner := mocks.NewMockRunner(ctrl)

	testCases := []struct {
		name     string
		template string
		url      string
	}{
		{"syntax_error", "echo {{.url.name", "/exec?name=test"},
		// Template execution error: in non-strict mode missing variables just render as <no value>
		// To force an execution error, we can use a non-existent function or other template error.
		{"execution_error", "echo {{.url.name | nonExistentFunc}}", "/exec?name=test"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cmdCfg := &config.URLCommand{
				URL: "GET /exec",
				CommandConfig: config.CommandConfig{
					CommandTemplate: tc.template,
				},
			}

			handler := handlers.ExecutionHandler(mockRunner, nil)
			h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

			req := httptest.NewRequest(http.MethodGet, tc.url, nil)
			ctx := context.WithValue(req.Context(), handlers.URLCommandKey, cmdCfg)
			req = req.WithContext(ctx)

			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			// Both syntax error and execution error should result in 500 (default for unknown errors in ErrorSink)
			// with message wrapped in "error building command"
			if rr.Code != http.StatusInternalServerError {
				t.Errorf("%s: expected status 500, got %d", tc.name, rr.Code)
			}
		})
	}
}

func TestExecutionHandler_PrepareOutput_Text(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockRunner := mocks.NewMockRunner(ctrl)
	mockCmd := mocks.NewMockCommand(ctrl)

	testCases := []struct {
		name       string
		outputType string
	}{
		{"default", ""},
		{"explicit_text", "text"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cmdCfg := &config.URLCommand{
				URL: "GET /exec",
				CommandConfig: config.CommandConfig{
					CommandTemplate: "echo hello",
					OutputType:      tc.outputType,
				},
			}

			mockRunner.EXPECT().Command("echo hello").Return(mockCmd)
			mockCmd.EXPECT().SetSysProcAttr(gomock.Any())
			mockCmd.EXPECT().SetStdout(gomock.Any())
			mockCmd.EXPECT().SetStderr(gomock.Any())
			mockCmd.EXPECT().Start().Return(nil)
			mockCmd.EXPECT().Wait().Return(nil)
			mockCmd.EXPECT().ProcessState().Return(nil).AnyTimes()
			mockCmd.EXPECT().Pid().Return(123).AnyTimes()

			handler := handlers.ExecutionHandler(mockRunner, nil)
			h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

			req := httptest.NewRequest(http.MethodGet, "/exec", nil)
			ctx := context.WithValue(req.Context(), handlers.URLCommandKey, cmdCfg)
			req = req.WithContext(ctx)

			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			if rr.Code != http.StatusOK {
				t.Errorf("expected status 200, got %d", rr.Code)
			}

			contentType := rr.Header().Get("Content-Type")
			if contentType != "text/plain; charset=utf-8" {
				t.Errorf("expected Content-Type 'text/plain; charset=utf-8', got %q", contentType)
			}
		})
	}
}

type flusherRecorder struct {
	*httptest.ResponseRecorder
	flushed bool
}

func (f *flusherRecorder) Flush() {
	f.flushed = true
}

func TestExecutionHandler_PrepareOutput_Stream_Success(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockRunner := mocks.NewMockRunner(ctrl)
	mockCmd := mocks.NewMockCommand(ctrl)

	cmdCfg := &config.URLCommand{
		URL: "GET /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "test-command",
			OutputType:      "stream",
		},
	}

	mockRunner.EXPECT().Command("test-command").Return(mockCmd)
	mockCmd.EXPECT().SetSysProcAttr(gomock.Any())
	mockCmd.EXPECT().SetStdout(gomock.Any()).Do(func(w io.Writer) {
		_, _ = w.Write([]byte("stream data"))
	})
	mockCmd.EXPECT().SetStderr(gomock.Any())
	mockCmd.EXPECT().Start().Return(nil)
	mockCmd.EXPECT().Wait().Return(nil)
	mockCmd.EXPECT().ProcessState().Return(nil).AnyTimes()
	mockCmd.EXPECT().Pid().Return(123).AnyTimes()

	handler := handlers.ExecutionHandler(mockRunner, nil)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	req := httptest.NewRequest(http.MethodGet, "/exec", nil)
	ctx := context.WithValue(req.Context(), handlers.URLCommandKey, cmdCfg)
	req = req.WithContext(ctx)

	fr := &flusherRecorder{ResponseRecorder: httptest.NewRecorder(), flushed: false}
	h.ServeHTTP(fr, req)

	if fr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", fr.Code)
	}

	headers := fr.Header()

	if headers.Get("Content-Type") != "text/plain; charset=utf-8" {
		t.Errorf("expected Content-Type 'text/plain; charset=utf-8', got %q", headers.Get("Content-Type"))
	}

	if headers.Get("Cache-Control") != "no-cache" {
		t.Errorf("expected Cache-Control 'no-cache', got %q", headers.Get("Cache-Control"))
	}

	if headers.Get("X-Accel-Buffering") != "no" {
		t.Errorf("expected X-Accel-Buffering 'no', got %q", headers.Get("X-Accel-Buffering"))
	}

	if !fr.flushed {
		t.Errorf("expected Flush() to be called")
	}

	if fr.Body.String() != "stream data" {
		t.Errorf("expected body 'stream data', got %q", fr.Body.String())
	}
}

func TestExecutionHandler_PrepareOutput_Stream_Failure(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockRunner := mocks.NewMockRunner(ctrl)
	// We do NOT expect mockRunner.Command to be called because prepareOutput should return an error first.

	cmdCfg := &config.URLCommand{
		URL: "GET /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "test-command",
			OutputType:      "stream",
		},
	}

	handler := handlers.ExecutionHandler(mockRunner, nil)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	req := httptest.NewRequest(http.MethodGet, "/exec", nil)
	ctx := context.WithValue(req.Context(), handlers.URLCommandKey, cmdCfg)
	req = req.WithContext(ctx)

	// Custom response writer that does NOT implement http.Flusher
	type nonFlusherResponseWriter struct {
		http.ResponseWriter
	}

	rr := httptest.NewRecorder()
	h.ServeHTTP(nonFlusherResponseWriter{ResponseWriter: rr}, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", rr.Code)
	}
}

func TestExecutionHandler_PrepareOutput_None(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	// We do NOT use Cleanup(ctrl.Finish) here because we can't reliably wait for the async goroutine
	// to call Wait() before the test finishes, which would cause a "missing call" error if Finish()
	// is called too early.

	mockRunner := mocks.NewMockRunner(ctrl)
	mockCmd := mocks.NewMockCommand(ctrl)

	cmdCfg := &config.URLCommand{
		URL: "GET /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo hello",
			OutputType:      "none",
		},
	}

	mockRunner.EXPECT().Command("echo hello").Return(mockCmd)
	mockCmd.EXPECT().SetSysProcAttr(gomock.Any())
	mockCmd.EXPECT().SetStdout(io.Discard)
	mockCmd.EXPECT().SetStderr(io.Discard)
	mockCmd.EXPECT().Start().Return(nil)
	mockCmd.EXPECT().Wait().Return(nil).AnyTimes() // Use AnyTimes to avoid missing call if it finishes late
	mockCmd.EXPECT().ProcessState().Return(nil).AnyTimes()
	mockCmd.EXPECT().Pid().Return(123).AnyTimes()

	handler := handlers.ExecutionHandler(mockRunner, nil)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	req := httptest.NewRequest(http.MethodGet, "/exec", nil)
	ctx := context.WithValue(req.Context(), handlers.URLCommandKey, cmdCfg)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	if rr.Body.Len() > 0 {
		t.Errorf("expected empty body for outputType 'none', got %q", rr.Body.String())
	}
}

func TestExecutionHandler_CallGate(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockRunner := mocks.NewMockRunner(ctrl)
	mockCmd := mocks.NewMockCommand(ctrl)

	mockRunner.EXPECT().
		Command("echo hello").
		Return(mockCmd).
		AnyTimes()

	mockCmd.EXPECT().SetSysProcAttr(gomock.Any()).AnyTimes()
	mockCmd.EXPECT().SetStdout(gomock.Any()).AnyTimes()
	mockCmd.EXPECT().SetStderr(gomock.Any()).AnyTimes()
	mockCmd.EXPECT().Start().Return(nil).AnyTimes()
	mockCmd.EXPECT().Wait().Return(nil).AnyTimes()
	mockCmd.EXPECT().ProcessState().Return(nil).AnyTimes()
	mockCmd.EXPECT().Pid().Return(1234).AnyTimes()

	registry := callgate.NewRegistry(callgate.WithDefaults())

	handler := handlers.ExecutionHandler(mockRunner, registry)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	urlCmd := &config.URLCommand{
		URL: "GET /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo hello",
			CallGate: &config.CallGateConfig{
				GroupName: ptrString("test-group"),
				Mode:      "single",
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/exec", nil)
	ctx := context.WithValue(req.Context(), handlers.URLCommandKey, urlCmd)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status OK, got %v", rr.Code)
	}
}

func TestExecutionHandler_UnknownCallGateMode(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockRunner := mocks.NewMockRunner(ctrl)
	registry := callgate.NewRegistry(callgate.WithDefaults())

	handler := handlers.ExecutionHandler(mockRunner, registry)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	urlCmd := &config.URLCommand{
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
	req = req.WithContext(context.WithValue(req.Context(), handlers.URLCommandKey, urlCmd))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code == http.StatusOK {
		t.Fatalf("expected non-200 status for invalid callgate mode, got %d, body=%q", rr.Code, rr.Body.String())
	}

	body := rr.Body.String()
	if !strings.Contains(body, "callgate registry") {
		t.Errorf("expected response body to contain %q, got %q", "callgate registry", body)
	}
}

func TestExecutionHandler_PrepareOutput_Unknown(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockRunner := mocks.NewMockRunner(ctrl)

	cmdCfg := &config.URLCommand{
		URL: "GET /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo hello",
			OutputType:      "invalid",
		},
	}

	handler := handlers.ExecutionHandler(mockRunner, nil)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	req := httptest.NewRequest(http.MethodGet, "/exec", nil)
	ctx := context.WithValue(req.Context(), handlers.URLCommandKey, cmdCfg)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", rr.Code)
	}
}

func TestExecutionHandler_ExecuteCommand_StartError_WritesFailedToStart(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockRunner := mocks.NewMockRunner(ctrl)
	mockCmd := mocks.NewMockCommand(ctrl)

	cmdCfg := &config.URLCommand{
		URL: "GET /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo hello",
			OutputType:      "text",
		},
	}

	mockRunner.EXPECT().
		Command("echo hello").
		Return(mockCmd)

	mockCmd.EXPECT().SetSysProcAttr(gomock.Any())
	mockCmd.EXPECT().SetStdout(gomock.Any())
	mockCmd.EXPECT().SetStderr(gomock.Any())

	mockCmd.EXPECT().Start().Return(errors.New("start boom"))
	mockCmd.EXPECT().Wait().Times(0)

	handler := handlers.ExecutionHandler(mockRunner, nil)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	req := httptest.NewRequest(http.MethodGet, "/exec", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.URLCommandKey, cmdCfg))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	// runCommand returns nil and only writes the error to the response body, so the status is typically 200.
	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d, body=%q", rr.Code, rr.Body.String())
	}

	body := rr.Body.String()
	if !strings.Contains(body, "failed to start command") {
		t.Errorf("expected body to contain %q, got %q", "failed to start command", body)
	}
}

func TestExecutionHandler_ExecuteCommand_StdoutAndStderr_WriteToSameResponseWriter(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockRunner := mocks.NewMockRunner(ctrl)
	mockCmd := mocks.NewMockCommand(ctrl)

	cmdCfg := &config.URLCommand{
		URL: "GET /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo hello",
			OutputType:      "text",
		},
	}

	var (
		gotStdout io.Writer
		gotStderr io.Writer
	)

	mockRunner.EXPECT().
		Command("echo hello").
		Return(mockCmd)

	mockCmd.EXPECT().SetSysProcAttr(gomock.Any())

	mockCmd.EXPECT().SetStdout(gomock.Any()).Do(func(w io.Writer) {
		gotStdout = w
		_, _ = w.Write([]byte("OUT\n"))
	})

	mockCmd.EXPECT().SetStderr(gomock.Any()).Do(func(w io.Writer) {
		gotStderr = w
		_, _ = w.Write([]byte("ERR\n"))
	})

	mockCmd.EXPECT().Start().Return(nil)
	mockCmd.EXPECT().Wait().Return(nil)
	mockCmd.EXPECT().ProcessState().Return(nil).AnyTimes()
	mockCmd.EXPECT().Pid().Return(123).AnyTimes()

	handler := handlers.ExecutionHandler(mockRunner, nil)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	req := httptest.NewRequest(http.MethodGet, "/exec", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.URLCommandKey, cmdCfg))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	if gotStdout == nil || gotStderr == nil {
		t.Fatalf("expected stdout and stderr writers to be captured, got stdout=%v stderr=%v", gotStdout, gotStderr)
	}

	// Both should reference the same writer instance.
	if gotStdout != gotStderr {
		t.Errorf("expected stdout and stderr to be the same writer, got stdout=%T(%p) stderr=%T(%p)",
			gotStdout, gotStdout, gotStderr, gotStderr)
	}

	body := rr.Body.String()
	if !strings.Contains(body, "OUT") || !strings.Contains(body, "ERR") {
		t.Errorf("expected response body to contain both OUT and ERR, got %q", body)
	}
}

func TestExecutionHandler_ExecuteCommand_SetsSetpgidTrue(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockRunner := mocks.NewMockRunner(ctrl)
	mockCmd := mocks.NewMockCommand(ctrl)

	cmdCfg := &config.URLCommand{
		URL: "GET /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo hello",
			OutputType:      "text",
		},
	}

	mockRunner.EXPECT().
		Command("echo hello").
		Return(mockCmd)

	mockCmd.EXPECT().
		SetSysProcAttr(gomock.Any()).
		Do(func(attr *syscall.SysProcAttr) {
			if attr == nil {
				t.Errorf("expected non-nil SysProcAttr")

				return
			}

			if attr.Setpgid != true {
				t.Errorf("expected Setpgid=true, got %v", attr.Setpgid)
			}
		})

	mockCmd.EXPECT().SetStdout(gomock.Any())
	mockCmd.EXPECT().SetStderr(gomock.Any())
	mockCmd.EXPECT().Start().Return(nil)
	mockCmd.EXPECT().Wait().Return(nil)
	mockCmd.EXPECT().ProcessState().Return(nil).AnyTimes()
	mockCmd.EXPECT().Pid().Return(123).AnyTimes()

	handler := handlers.ExecutionHandler(mockRunner, nil)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	req := httptest.NewRequest(http.MethodGet, "/exec", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.URLCommandKey, cmdCfg))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}

func TestExecutionHandler_SyncWait_ExitError_NonZeroExit_WritesFailureMessage(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockRunner := mocks.NewMockRunner(ctrl)
	mockCmd := mocks.NewMockCommand(ctrl)

	// Create a real *exec.ExitError to exercise errors.As(..., *exec.ExitError).
	runErr := exec.Command("sh", "-c", "exit 7").Run()

	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) {
		t.Fatalf("expected *exec.ExitError, got %T: %v", runErr, runErr)
	}

	cmdCfg := &config.URLCommand{
		URL: "GET /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "test-command",
			OutputType:      "text",
		},
	}

	mockRunner.EXPECT().Command("test-command").Return(mockCmd)

	mockCmd.EXPECT().SetSysProcAttr(gomock.Any())
	mockCmd.EXPECT().SetStdout(gomock.Any())
	mockCmd.EXPECT().SetStderr(gomock.Any())
	mockCmd.EXPECT().Start().Return(nil)

	mockCmd.EXPECT().Wait().Return(exitErr)
	mockCmd.EXPECT().ProcessState().Return(nil).AnyTimes()

	handler := handlers.ExecutionHandler(mockRunner, nil)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	req := httptest.NewRequest(http.MethodGet, "/exec", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.URLCommandKey, cmdCfg))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	body := rr.Body.String()
	if !strings.Contains(body, "Command failed with exit code: 7") {
		t.Errorf("expected body to contain exit code 7, got %q", body)
	}
}

func TestExecutionHandler_SyncWait_WaitReturnsNonExitError_WritesFailureMessage(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockRunner := mocks.NewMockRunner(ctrl)
	mockCmd := mocks.NewMockCommand(ctrl)

	cmdCfg := &config.URLCommand{
		URL: "GET /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "test-command",
			OutputType:      "text",
		},
	}

	mockRunner.EXPECT().Command("test-command").Return(mockCmd)

	mockCmd.EXPECT().SetSysProcAttr(gomock.Any())
	mockCmd.EXPECT().SetStdout(gomock.Any())
	mockCmd.EXPECT().SetStderr(gomock.Any())
	mockCmd.EXPECT().Start().Return(nil)

	waitErr := errors.New("wait boom")
	mockCmd.EXPECT().Wait().Return(waitErr)
	mockCmd.EXPECT().ProcessState().Return(nil).AnyTimes()

	handler := handlers.ExecutionHandler(mockRunner, nil)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	req := httptest.NewRequest(http.MethodGet, "/exec", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.URLCommandKey, cmdCfg))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	body := rr.Body.String()

	if !strings.Contains(body, "Command failed with exit code: -1") {
		t.Errorf("expected body to contain exit code -1, got %q", body)
	}

	if !strings.Contains(body, "wait boom") {
		t.Errorf("expected body to contain %q, got %q", "wait boom", body)
	}
}

func TestExecutionHandler_SyncWait_NoError_ProcessStateNil_DoesNotWriteFailureMessage(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockRunner := mocks.NewMockRunner(ctrl)
	mockCmd := mocks.NewMockCommand(ctrl)

	cmdCfg := &config.URLCommand{
		URL: "GET /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "test-command",
			OutputType:      "text",
		},
	}

	mockRunner.EXPECT().Command("test-command").Return(mockCmd)

	mockCmd.EXPECT().SetSysProcAttr(gomock.Any())
	mockCmd.EXPECT().SetStdout(gomock.Any())
	mockCmd.EXPECT().SetStderr(gomock.Any())
	mockCmd.EXPECT().Start().Return(nil)

	mockCmd.EXPECT().Wait().Return(nil)
	mockCmd.EXPECT().ProcessState().Return(nil).AnyTimes()

	handler := handlers.ExecutionHandler(mockRunner, nil)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	req := httptest.NewRequest(http.MethodGet, "/exec", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.URLCommandKey, cmdCfg))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	if strings.Contains(rr.Body.String(), "Command failed with exit code:") {
		t.Errorf("expected no failure message, got %q", rr.Body.String())
	}
}

func TestExecutionHandler_TerminateOnCancel_NoGrace_SendsSIGKILL(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockRunner := mocks.NewMockRunner(ctrl)
	mockCmd := mocks.NewMockCommand(ctrl)

	cmdCfg := &config.URLCommand{
		URL: "GET /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate:         "echo hello",
			OutputType:              "text",
			GraceTerminationTimeout: nil,
		},
	}

	mockRunner.EXPECT().Command("echo hello").Return(mockCmd)

	mockCmd.EXPECT().SetSysProcAttr(gomock.Any())
	mockCmd.EXPECT().SetStdout(gomock.Any())
	mockCmd.EXPECT().SetStderr(gomock.Any())
	mockCmd.EXPECT().Start().Return(nil)

	mockCmd.EXPECT().Pid().Return(123).AnyTimes()

	waitUnblock := make(chan struct{})

	mockCmd.EXPECT().Wait().DoAndReturn(func() error {
		<-waitUnblock

		return errors.New("wait error")
	})

	killed := make(chan struct{})

	mockRunner.EXPECT().
		Kill(-123, syscall.SIGKILL).
		DoAndReturn(func(_ int, _ syscall.Signal) error {
			close(killed)
			close(waitUnblock)

			return nil
		})

	handler := handlers.ExecutionHandler(mockRunner, nil)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	req := httptest.NewRequest(http.MethodGet, "/exec", nil)
	ctx, cancel := context.WithCancel(req.Context())

	cancel() // cancel before handler runs

	req = req.WithContext(context.WithValue(ctx, handlers.URLCommandKey, cmdCfg))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	select {
	case <-killed:
	default:
		t.Fatalf("expected SIGKILL to be sent")
	}

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	if !strings.Contains(rr.Body.String(), "context canceled") {
		t.Errorf("expected body to contain %q, got %q", "context canceled", rr.Body.String())
	}
}

func TestExecutionHandler_TerminateOnCancel_WithGrace_Timeout_SendsSIGTERMThenSIGKILL(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockRunner := mocks.NewMockRunner(ctrl)
	mockCmd := mocks.NewMockCommand(ctrl)

	grace := 10 * time.Millisecond

	cmdCfg := &config.URLCommand{
		URL: "GET /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate:         "echo hello",
			OutputType:              "text",
			GraceTerminationTimeout: &grace,
		},
	}

	mockRunner.EXPECT().Command("echo hello").Return(mockCmd)

	mockCmd.EXPECT().SetSysProcAttr(gomock.Any())
	mockCmd.EXPECT().SetStdout(gomock.Any())
	mockCmd.EXPECT().SetStderr(gomock.Any())
	mockCmd.EXPECT().Start().Return(nil)

	mockCmd.EXPECT().Pid().Return(123).AnyTimes()
	mockCmd.EXPECT().ProcessState().Return(nil).AnyTimes()

	// Block long enough so grace timer fires.
	mockCmd.EXPECT().Wait().DoAndReturn(func() error {
		time.Sleep(50 * time.Millisecond)

		return errors.New("wait error")
	})

	gomock.InOrder(
		mockRunner.EXPECT().Kill(-123, syscall.SIGTERM).Return(nil),
		mockRunner.EXPECT().Kill(-123, syscall.SIGKILL).Return(nil),
	)

	handler := handlers.ExecutionHandler(mockRunner, nil)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	req := httptest.NewRequest(http.MethodGet, "/exec", nil)
	ctx, cancel := context.WithCancel(req.Context())

	cancel()

	req = req.WithContext(context.WithValue(ctx, handlers.URLCommandKey, cmdCfg))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}

func TestExecutionHandler_TerminateOnCancel_WithGrace_ProcessEndsBeforeTimer_SendsOnlySIGTERM(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockRunner := mocks.NewMockRunner(ctrl)
	mockCmd := mocks.NewMockCommand(ctrl)

	grace := 100 * time.Millisecond

	cmdCfg := &config.URLCommand{
		URL: "GET /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate:         "echo hello",
			OutputType:              "text",
			GraceTerminationTimeout: &grace,
		},
	}

	mockRunner.EXPECT().Command("echo hello").Return(mockCmd)

	mockCmd.EXPECT().SetSysProcAttr(gomock.Any())
	mockCmd.EXPECT().SetStdout(gomock.Any())
	mockCmd.EXPECT().SetStderr(gomock.Any())
	mockCmd.EXPECT().Start().Return(nil)

	mockCmd.EXPECT().Pid().Return(123).AnyTimes()
	mockCmd.EXPECT().ProcessState().Return(nil).AnyTimes()

	// Finish quickly (before grace expires).
	mockCmd.EXPECT().Wait().DoAndReturn(func() error {
		time.Sleep(5 * time.Millisecond)

		return errors.New("wait error")
	})

	mockRunner.EXPECT().Kill(-123, syscall.SIGTERM).Return(nil)
	mockRunner.EXPECT().Kill(-123, syscall.SIGKILL).Times(0)

	handler := handlers.ExecutionHandler(mockRunner, nil)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	req := httptest.NewRequest(http.MethodGet, "/exec", nil)
	ctx, cancel := context.WithCancel(req.Context())

	cancel()

	req = req.WithContext(context.WithValue(ctx, handlers.URLCommandKey, cmdCfg))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}
}

func TestExecutionHandler_DeadlineExceeded_PrioritizesCtxErrOverExitError(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockRunner := mocks.NewMockRunner(ctrl)
	mockCmd := mocks.NewMockCommand(ctrl)

	// Create a real *exec.ExitError with a non-zero exit code.
	runErr := exec.Command("sh", "-c", "exit 7").Run()

	var exitErr *exec.ExitError

	if !errors.As(runErr, &exitErr) {
		t.Fatalf("expected *exec.ExitError, got %T: %v", runErr, runErr)
	}

	cmdCfg := &config.URLCommand{
		URL: "GET /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo hello",
			OutputType:      "text",
		},
	}

	mockRunner.EXPECT().Command("echo hello").Return(mockCmd)

	mockCmd.EXPECT().SetSysProcAttr(gomock.Any())
	mockCmd.EXPECT().SetStdout(gomock.Any())
	mockCmd.EXPECT().SetStderr(gomock.Any())
	mockCmd.EXPECT().Start().Return(nil)

	// Prevent signalProcessGroup from calling runner.Kill.
	mockCmd.EXPECT().Pid().Return(0).AnyTimes()

	mockCmd.EXPECT().Wait().Return(exitErr)
	mockCmd.EXPECT().ProcessState().Return(nil).AnyTimes()

	handler := handlers.ExecutionHandler(mockRunner, nil)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	req := httptest.NewRequest(http.MethodGet, "/exec", nil)

	// Deadline already exceeded.
	ctx, cancel := context.WithDeadline(req.Context(), time.Now().Add(-1*time.Second))
	defer cancel()

	req = req.WithContext(context.WithValue(ctx, handlers.URLCommandKey, cmdCfg))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	body := rr.Body.String()

	if !strings.Contains(body, "context deadline exceeded") {
		t.Errorf("expected body to contain %q, got %q", "context deadline exceeded", body)
	}

	if !strings.Contains(body, "Command failed with exit code: -1") {
		t.Errorf("expected body to contain exit code -1, got %q", body)
	}

	// Make sure it did not report the process exit code (7) as primary.
	if strings.Contains(body, "Command failed with exit code: 7") {
		t.Errorf("did not expect exit code 7 to be reported, got %q", body)
	}
}

func TestExecutionHandler_AsyncNone_ReturnsBeforeWait(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockRunner := mocks.NewMockRunner(ctrl)
	mockCmd := mocks.NewMockCommand(ctrl)

	cmdCfg := &config.URLCommand{
		URL: "GET /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo hello",
			OutputType:      "none", // async
		},
	}

	mockRunner.EXPECT().Command("echo hello").Return(mockCmd)

	mockCmd.EXPECT().SetSysProcAttr(gomock.Any())
	mockCmd.EXPECT().SetStdout(io.Discard)
	mockCmd.EXPECT().SetStderr(io.Discard)
	mockCmd.EXPECT().Start().Return(nil)
	mockCmd.EXPECT().Pid().Return(123).AnyTimes()
	mockCmd.EXPECT().ProcessState().Return(nil).AnyTimes()

	waitStarted := make(chan struct{})
	unblockWait := make(chan struct{})

	mockCmd.EXPECT().Wait().DoAndReturn(func() error {
		close(waitStarted) // signal: goroutine reached Wait()
		<-unblockWait      // block until test allows it

		return nil
	})

	handler := handlers.ExecutionHandler(mockRunner, nil)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	req := httptest.NewRequest(http.MethodGet, "/exec", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.URLCommandKey, cmdCfg))

	rr := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		h.ServeHTTP(rr, req)
		close(done)
	}()

	// Handler should finish quickly without waiting for Wait()
	select {
	case <-done:
		// ok
	case <-time.After(50 * time.Millisecond):
		t.Fatalf("handler did not return quickly; it may be waiting for Wait()")
	}

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	if rr.Body.Len() != 0 {
		t.Errorf("expected empty body for outputType 'none', got %q", rr.Body.String())
	}

	// Ensure Wait() actually ran in a goroutine
	select {
	case <-waitStarted:
		// ok
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected Wait() to be called asynchronously")
	}

	// cleanup: allow Wait() to finish so gomock expectations can be satisfied
	close(unblockWait)
}

//nolint:paralleltest
func TestExecutionHandler_AsyncNone_WaitError_LogsButDoesNotAffectResponse(t *testing.T) {
	// Do NOT run in parallel: log.SetOutput is global.
	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockRunner := mocks.NewMockRunner(ctrl)
	mockCmd := mocks.NewMockCommand(ctrl)

	cmdCfg := &config.URLCommand{
		URL: "GET /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo hello",
			OutputType:      "none",
		},
	}

	mockRunner.EXPECT().Command("echo hello").Return(mockCmd)

	mockCmd.EXPECT().SetSysProcAttr(gomock.Any())
	mockCmd.EXPECT().SetStdout(io.Discard)
	mockCmd.EXPECT().SetStderr(io.Discard)
	mockCmd.EXPECT().Start().Return(nil)
	mockCmd.EXPECT().Pid().Return(123).AnyTimes()
	mockCmd.EXPECT().ProcessState().Return(nil).AnyTimes()

	waitDone := make(chan struct{})

	mockCmd.EXPECT().Wait().DoAndReturn(func() error {
		defer close(waitDone)

		return errors.New("wait boom")
	})

	var (
		buf strings.Builder
		mu  sync.Mutex
	)

	origOut := log.Writer()

	log.SetOutput(&syncedWriter{Writer: &buf, mu: &mu})
	t.Cleanup(func() { log.SetOutput(origOut) })

	handler := handlers.ExecutionHandler(mockRunner, nil)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	req := httptest.NewRequest(http.MethodGet, "/exec", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.URLCommandKey, cmdCfg))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	if rr.Body.Len() != 0 {
		t.Errorf("expected empty body, got %q", rr.Body.String())
	}

	// Wait() returned, but the goroutine may not have logged yet.
	select {
	case <-waitDone:
	case <-time.After(200 * time.Millisecond):
		t.Fatalf("expected async Wait() to finish")
	}

	// Poll logs until the error line appears (or timeout).
	deadline := time.Now().Add(300 * time.Millisecond)

	for {
		mu.Lock()
		logs := buf.String()
		mu.Unlock()

		if strings.Contains(logs, "Asynchronous command failed") && strings.Contains(logs, "wait boom") {
			break
		}

		if time.Now().After(deadline) {
			t.Fatalf(
				"expected logs to contain %q and %q, got %q",
				"Asynchronous command failed", "wait boom", logs,
			)
		}

		time.Sleep(5 * time.Millisecond)
	}
}

func TestExecutionHandler_RunCommand_AppendsErrorMessageToBody_OnNonZeroExit(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	t.Cleanup(ctrl.Finish)

	mockRunner := mocks.NewMockRunner(ctrl)
	mockCmd := mocks.NewMockCommand(ctrl)

	// real *exec.ExitError with exit code 7
	runErr := exec.Command("sh", "-c", "exit 7").Run()

	var exitErr *exec.ExitError

	if !errors.As(runErr, &exitErr) {
		t.Fatalf("expected *exec.ExitError, got %T: %v", runErr, runErr)
	}

	cmdCfg := &config.URLCommand{
		URL: "GET /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo hello",
			OutputType:      "text",
		},
	}

	mockRunner.EXPECT().Command("echo hello").Return(mockCmd)

	mockCmd.EXPECT().SetSysProcAttr(gomock.Any())
	mockCmd.EXPECT().SetStdout(gomock.Any()).Do(func(w io.Writer) {
		// simulate process output written before failure is appended
		_, _ = w.Write([]byte("PROC_OUT\n"))
	})
	mockCmd.EXPECT().SetStderr(gomock.Any())
	mockCmd.EXPECT().Start().Return(nil)
	mockCmd.EXPECT().Wait().Return(exitErr)
	mockCmd.EXPECT().ProcessState().Return(nil).AnyTimes()
	mockCmd.EXPECT().Pid().Return(123).AnyTimes()

	handler := handlers.ExecutionHandler(mockRunner, nil)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	req := httptest.NewRequest(http.MethodGet, "/exec", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.URLCommandKey, cmdCfg))

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rr.Code)
	}

	body := rr.Body.String()

	// may be mixed with earlier output; we only assert that the error message is appended somewhere
	if !strings.Contains(body, "PROC_OUT") {
		t.Errorf("expected body to contain process output, got %q", body)
	}

	if !strings.Contains(body, "Command failed with exit code: 7") {
		t.Errorf("expected body to contain exit code 7 failure message, got %q", body)
	}

	if !strings.Contains(body, "error:") {
		t.Errorf("expected body to contain 'error:' part, got %q", body)
	}
}

type erroringResponseWriter struct {
	*httptest.ResponseRecorder
	writeErr error
}

func (e *erroringResponseWriter) Write(p []byte) (int, error) {
	_, _ = e.ResponseRecorder.Write(p)

	return 0, e.writeErr
}

//nolint:paralleltest
func TestExecutionHandler_RunCommand_WriteErrorMessageWriteFails_LogsError(t *testing.T) {
	// Do NOT run in parallel: log.SetOutput is global.
	ctrl := gomock.NewController(t)

	t.Cleanup(ctrl.Finish)

	mockRunner := mocks.NewMockRunner(ctrl)
	mockCmd := mocks.NewMockCommand(ctrl)

	// real *exec.ExitError with exit code 7 -> triggers errorMessage write
	runErr := exec.Command("sh", "-c", "exit 7").Run()

	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) {
		t.Fatalf("expected *exec.ExitError, got %T: %v", runErr, runErr)
	}

	cmdCfg := &config.URLCommand{
		URL: "GET /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo hello",
			OutputType:      "text",
		},
	}

	mockRunner.EXPECT().Command("echo hello").Return(mockCmd)

	mockCmd.EXPECT().SetSysProcAttr(gomock.Any())
	mockCmd.EXPECT().SetStdout(gomock.Any())
	mockCmd.EXPECT().SetStderr(gomock.Any())
	mockCmd.EXPECT().Start().Return(nil)
	mockCmd.EXPECT().Wait().Return(exitErr)
	mockCmd.EXPECT().ProcessState().Return(nil).AnyTimes()
	mockCmd.EXPECT().Pid().Return(123).AnyTimes()

	// capture logs
	var (
		buf strings.Builder
		mu  sync.Mutex
	)

	origOut := log.Writer()

	log.SetOutput(&syncedWriter{Writer: &buf, mu: &mu})
	t.Cleanup(func() { log.SetOutput(origOut) })

	handler := handlers.ExecutionHandler(mockRunner, nil)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	req := httptest.NewRequest(http.MethodGet, "/exec", nil)
	req = req.WithContext(context.WithValue(req.Context(), handlers.URLCommandKey, cmdCfg))

	rw := &erroringResponseWriter{
		ResponseRecorder: httptest.NewRecorder(),
		writeErr:         errors.New("write failed"),
	}

	h.ServeHTTP(rw, req)

	// We only care that it logged the failure to write the error message.
	// Poll a bit because logging happens synchronously here, but polling makes it robust.
	deadline := time.Now().Add(200 * time.Millisecond)

	for {
		mu.Lock()
		logs := buf.String()
		mu.Unlock()

		if strings.Contains(logs, "Failed to write error message") {
			if !strings.Contains(logs, "write failed") {
				t.Fatalf("expected logs to contain %q, got %q", "write failed", logs)
			}

			break
		}

		if time.Now().After(deadline) {
			t.Fatalf("expected logs to contain %q, got %q", "Failed to write error message", logs)
		}

		time.Sleep(5 * time.Millisecond)
	}
}

func ptrString(s string) *string {
	return &s
}

func TestExecutionHandler_CallGate_ImplicitGroupName(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockRunner := mocks.NewMockRunner(ctrl)
	mockCmd := mocks.NewMockCommand(ctrl)

	mockRunner.EXPECT().Command("echo hello").Return(mockCmd).Times(1)
	mockCmd.EXPECT().SetSysProcAttr(gomock.Any()).AnyTimes()
	mockCmd.EXPECT().SetStdout(gomock.Any()).AnyTimes()
	mockCmd.EXPECT().SetStderr(gomock.Any()).AnyTimes()
	mockCmd.EXPECT().Start().Return(nil).AnyTimes()
	mockCmd.EXPECT().Wait().Return(nil).AnyTimes()
	mockCmd.EXPECT().ProcessState().Return(nil).AnyTimes()
	mockCmd.EXPECT().Pid().Return(1234).AnyTimes()

	registry := callgate.NewRegistry(callgate.WithDefaults())

	handler := handlers.ExecutionHandler(mockRunner, registry)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	// URL 1
	urlCmd1 := &config.URLCommand{
		URL: "GET /exec1",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo hello",
			CallGate: &config.CallGateConfig{
				GroupName: nil, // Implicitly "GET /exec1"
				Mode:      "single",
			},
		},
	}

	// URL 2 - same template, different URL, no GroupName -> should have DIFFERENT implicit group names
	urlCmd2 := &config.URLCommand{
		URL: "GET /exec2",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo hello",
			CallGate: &config.CallGateConfig{
				GroupName: nil, // Implicitly "GET /exec2"
				Mode:      "single",
			},
		},
	}

	// 1. Run URL 1
	req1 := httptest.NewRequest(http.MethodGet, "/exec1", nil)
	req1 = req1.WithContext(context.WithValue(req1.Context(), handlers.URLCommandKey, urlCmd1))
	rr1 := httptest.NewRecorder()

	// Simulating a long running command for URL 1 would be better, but here we just check if they don't interfere.
	// Actually, let's use a shared group name to see it blocks, and then use nil to see it doesn't.

	// Test isolation:
	// We'll use a gate to block URL 1, and see if URL 2 is still allowed.

	gate1, _ := registry.GetOrCreate("GET /exec1", "single")
	release, _ := gate1.Acquire(t.Context())

	defer release()

	// Now GET /exec1 is busy.
	h.ServeHTTP(rr1, req1)

	if rr1.Code != http.StatusTooManyRequests {
		t.Errorf("expected status 429 for /exec1, got %v", rr1.Code)
	}

	// But GET /exec2 should be FREE because it has a different implicit group name.
	req2 := httptest.NewRequest(http.MethodGet, "/exec2", nil)
	req2 = req2.WithContext(context.WithValue(req2.Context(), handlers.URLCommandKey, urlCmd2))
	rr2 := httptest.NewRecorder()
	h.ServeHTTP(rr2, req2)

	if rr2.Code != http.StatusOK {
		t.Errorf("expected status 200 for /exec2, got %v", rr2.Code)
	}
}

func TestExecutionHandler_CallGate_EmptyGroupName(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockRunner := mocks.NewMockRunner(ctrl)
	mockCmd := mocks.NewMockCommand(ctrl)

	mockRunner.EXPECT().Command("echo hello").Return(mockCmd).AnyTimes()
	mockCmd.EXPECT().SetSysProcAttr(gomock.Any()).AnyTimes()
	mockCmd.EXPECT().SetStdout(gomock.Any()).AnyTimes()
	mockCmd.EXPECT().SetStderr(gomock.Any()).AnyTimes()
	mockCmd.EXPECT().Start().Return(nil).AnyTimes()
	mockCmd.EXPECT().Wait().Return(nil).AnyTimes()
	mockCmd.EXPECT().ProcessState().Return(nil).AnyTimes()
	mockCmd.EXPECT().Pid().Return(1234).AnyTimes()

	registry := callgate.NewRegistry(callgate.WithDefaults())
	handler := handlers.ExecutionHandler(mockRunner, registry)
	h := httpx.ToHandler(httpx.ErrorSink(nil, true), handler)

	// URL 1 with empty groupName
	urlCmd1 := &config.URLCommand{
		URL: "GET /exec1",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo hello",
			CallGate: &config.CallGateConfig{
				GroupName: ptrString(""),
				Mode:      "single",
			},
		},
	}

	// URL 2 with empty groupName -> should SHARE the same "" group
	urlCmd2 := &config.URLCommand{
		URL: "GET /exec2",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo hello",
			CallGate: &config.CallGateConfig{
				GroupName: ptrString(""),
				Mode:      "single",
			},
		},
	}

	// Block the "" group
	gate, _ := registry.GetOrCreate("", "single")
	release, _ := gate.Acquire(t.Context())

	defer release()

	// Both should be blocked
	for _, cmd := range []*config.URLCommand{urlCmd1, urlCmd2} {
		req := httptest.NewRequest(http.MethodGet, strings.Split(cmd.URL, " ")[1], nil)
		req = req.WithContext(context.WithValue(req.Context(), handlers.URLCommandKey, cmd))
		rr := httptest.NewRecorder()

		h.ServeHTTP(rr, req)

		if rr.Code != http.StatusTooManyRequests {
			t.Errorf("expected status 429 for %s, got %v", cmd.URL, rr.Code)
		}
	}
}
