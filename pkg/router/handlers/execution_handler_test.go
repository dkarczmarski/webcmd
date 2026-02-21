package handlers_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dkarczmarski/webcmd/pkg/callgate"
	"github.com/dkarczmarski/webcmd/pkg/config"
	"github.com/dkarczmarski/webcmd/pkg/httpx"
	"github.com/dkarczmarski/webcmd/pkg/router/handlers"
	"github.com/dkarczmarski/webcmd/pkg/router/handlers/internal/mocks"
	"go.uber.org/mock/gomock"
)

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
	h := httpx.ToHandler(httpx.ErrorSink(nil), handler)

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
	h := httpx.ToHandler(httpx.ErrorSink(nil), handler)

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
	h := httpx.ToHandler(httpx.ErrorSink(nil), handler)

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
	h := httpx.ToHandler(httpx.ErrorSink(nil), handler)

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
	h := httpx.ToHandler(httpx.ErrorSink(nil), handler)

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
	h := httpx.ToHandler(httpx.ErrorSink(nil), handler)

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
	h := httpx.ToHandler(httpx.ErrorSink(nil), handler)

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
	h := httpx.ToHandler(httpx.ErrorSink(nil), handler)

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
	h := httpx.ToHandler(httpx.ErrorSink(nil), handler)

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
			h := httpx.ToHandler(httpx.ErrorSink(nil), handler)

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
	h := httpx.ToHandler(httpx.ErrorSink(nil), handler)

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
			h := httpx.ToHandler(httpx.ErrorSink(nil), handler)

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
			h := httpx.ToHandler(httpx.ErrorSink(nil), handler)

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
	h := httpx.ToHandler(httpx.ErrorSink(nil), handler)

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
	h := httpx.ToHandler(httpx.ErrorSink(nil), handler)

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
	h := httpx.ToHandler(httpx.ErrorSink(nil), handler)

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
	h := httpx.ToHandler(httpx.ErrorSink(nil), handler)

	urlCmd := &config.URLCommand{
		URL: "GET /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo hello",
			CallGate: &config.CallGateConfig{
				GroupName: "test-group",
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
	h := httpx.ToHandler(httpx.ErrorSink(nil), handler)

	urlCmd := &config.URLCommand{
		URL: "GET /exec",
		CommandConfig: config.CommandConfig{
			CommandTemplate: "echo hello",
			OutputType:      "text",
			CallGate: &config.CallGateConfig{
				GroupName: "test-group",
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
	h := httpx.ToHandler(httpx.ErrorSink(nil), handler)

	req := httptest.NewRequest(http.MethodGet, "/exec", nil)
	ctx := context.WithValue(req.Context(), handlers.URLCommandKey, cmdCfg)
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("expected status 500, got %d", rr.Code)
	}
}
