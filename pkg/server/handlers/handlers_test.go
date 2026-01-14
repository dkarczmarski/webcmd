package handlers_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/dkarczmarski/webcmd/pkg/config"
	"github.com/dkarczmarski/webcmd/pkg/httpx"
	"github.com/dkarczmarski/webcmd/pkg/server/handlers"
	"github.com/dkarczmarski/webcmd/pkg/server/handlers/internal/mocks"
	"go.uber.org/mock/gomock"
)

func TestExecutionHandler(t *testing.T) {
	t.Parallel()

	t.Run("Success", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockRunner := mocks.NewMockRunner(ctrl)
		mockCommand := mocks.NewMockCommand(ctrl)

		mockRunner.EXPECT().
			Command("echo", []string{"hello"}).
			Return(mockCommand)

		mockCommand.EXPECT().SetStdout(gomock.Any()).Do(func(w io.Writer) {
			_, _ = w.Write([]byte("hello\n"))
		})
		mockCommand.EXPECT().SetStderr(gomock.Any())
		mockCommand.EXPECT().SetSysProcAttr(gomock.Any())
		mockCommand.EXPECT().Start().Return(nil)
		mockCommand.EXPECT().Wait().Return(nil)
		mockCommand.EXPECT().ProcessState().Return(nil).AnyTimes()

		handler := handlers.ExecutionHandler(mockRunner)

		cmd := &config.URLCommand{
			URL: "GET /test",
			CommandConfig: config.CommandConfig{
				CommandTemplate: "echo\nhello",
			},
		}

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		ctx := context.WithValue(req.Context(), handlers.URLCommandKey, cmd)
		req = req.WithContext(ctx)

		recorder := httptest.NewRecorder()

		err := handler.ServeHTTP(recorder, req)
		if err != nil {
			t.Errorf("ExecutionHandler returned error: %v", err)
		}

		if recorder.Code != http.StatusOK {
			t.Errorf("expected status OK, got %v", recorder.Code)
		}

		if recorder.Body.String() != "hello\n" {
			t.Errorf("expected output 'hello\\n', got %q", recorder.Body.String())
		}
	})

	t.Run("WithHeaders", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockRunner := mocks.NewMockRunner(ctrl)
		mockCommand := mocks.NewMockCommand(ctrl)

		mockRunner.EXPECT().
			Command("echo", []string{"X-Test-Value", "X-Test-Value"}).
			Return(mockCommand)

		mockCommand.EXPECT().SetStdout(gomock.Any())
		mockCommand.EXPECT().SetStderr(gomock.Any())
		mockCommand.EXPECT().SetSysProcAttr(gomock.Any())
		mockCommand.EXPECT().Start().Return(nil)
		mockCommand.EXPECT().Wait().Return(nil)
		mockCommand.EXPECT().ProcessState().Return(nil).AnyTimes()

		handler := handlers.ExecutionHandler(mockRunner)

		cmd := &config.URLCommand{
			URL: "GET /test",
			CommandConfig: config.CommandConfig{
				CommandTemplate: "echo\n{{ .headers.X_Test_Header }}\n{{ index .headers \"X_Test_Header\" }}",
			},
		}

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Set("X-Test-Header", "X-Test-Value")
		ctx := context.WithValue(req.Context(), handlers.URLCommandKey, cmd)
		req = req.WithContext(ctx)

		recorder := httptest.NewRecorder()

		err := handler.ServeHTTP(recorder, req)
		if err != nil {
			t.Errorf("ExecutionHandler returned error: %v", err)
		}

		if recorder.Code != http.StatusOK {
			t.Errorf("expected status OK, got %v", recorder.Code)
		}
	})

	t.Run("WithMultipleHeaders", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockRunner := mocks.NewMockRunner(ctrl)
		mockCommand := mocks.NewMockCommand(ctrl)

		mockRunner.EXPECT().
			Command("echo", []string{"value1; value2"}).
			Return(mockCommand)

		mockCommand.EXPECT().SetStdout(gomock.Any())
		mockCommand.EXPECT().SetStderr(gomock.Any())
		mockCommand.EXPECT().SetSysProcAttr(gomock.Any())
		mockCommand.EXPECT().Start().Return(nil)
		mockCommand.EXPECT().Wait().Return(nil)
		mockCommand.EXPECT().ProcessState().Return(nil).AnyTimes()

		handler := handlers.ExecutionHandler(mockRunner)

		cmd := &config.URLCommand{
			URL: "GET /test",
			CommandConfig: config.CommandConfig{
				CommandTemplate: "echo\n{{ .headers.X_Multi_Header }}",
			},
		}

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		req.Header.Add("X-Multi-Header", "value1")
		req.Header.Add("X-Multi-Header", "value2")
		ctx := context.WithValue(req.Context(), handlers.URLCommandKey, cmd)
		req = req.WithContext(ctx)

		recorder := httptest.NewRecorder()

		err := handler.ServeHTTP(recorder, req)
		if err != nil {
			t.Errorf("ExecutionHandler returned error: %v", err)
		}

		if recorder.Code != http.StatusOK {
			t.Errorf("expected status OK, got %v", recorder.Code)
		}
	})

	t.Run("CommandNotFound", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		handler := handlers.ExecutionHandler(nil)

		req := httptest.NewRequest(http.MethodGet, "/unknown", nil)
		// No URLCommand in context

		recorder := httptest.NewRecorder()

		err := handler.ServeHTTP(recorder, req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		var webErr *httpx.WebError
		if !errors.As(err, &webErr) {
			t.Fatalf("expected *httpx.WebError, got %T", err)
		}

		if webErr.HTTPStatus() != http.StatusNotFound {
			t.Errorf("expected status 404, got %v", webErr.HTTPStatus())
		}
	})

	t.Run("GracefulTermination", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockRunner := mocks.NewMockRunner(ctrl)
		mockCommand := mocks.NewMockCommand(ctrl)

		graceTimeout := 100 * time.Millisecond
		done := make(chan struct{})

		mockRunner.EXPECT().
			Command("long-running", gomock.Any()).
			Return(mockCommand)

		mockCommand.EXPECT().SetStdout(gomock.Any())
		mockCommand.EXPECT().SetStderr(gomock.Any())
		mockCommand.EXPECT().SetSysProcAttr(gomock.Any())
		mockCommand.EXPECT().Start().Return(nil)
		mockCommand.EXPECT().Pid().Return(1234).AnyTimes()

		mockRunner.EXPECT().Kill(1234, syscall.SIGTERM).Return(nil)

		mockRunner.EXPECT().Kill(1234, syscall.SIGKILL).Return(nil)

		mockCommand.EXPECT().Wait().DoAndReturn(func() error {
			<-done

			return errors.New("signal: killed")
		})
		mockCommand.EXPECT().ProcessState().Return(nil).AnyTimes()

		handler := handlers.ExecutionHandler(mockRunner)

		cmd := &config.URLCommand{
			URL: "GET /long",
			CommandConfig: config.CommandConfig{
				CommandTemplate:         "long-running",
				GraceTerminationTimeout: &graceTimeout,
			},
		}

		ctx, cancel := context.WithCancel(t.Context())
		req := httptest.NewRequest(http.MethodGet, "/long", nil)
		ctx = context.WithValue(ctx, handlers.URLCommandKey, cmd)
		req = req.WithContext(ctx)

		recorder := httptest.NewRecorder()

		go func() {
			_ = handler.ServeHTTP(recorder, req)
		}()

		// Trigger context cancellation to initiate termination
		time.Sleep(20 * time.Millisecond)
		cancel()

		// Wait for more than graceTimeout to ensure SIGKILL is sent
		time.Sleep(150 * time.Millisecond)
		close(done)
	})

	t.Run("CommandStartFailure", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockRunner := mocks.NewMockRunner(ctrl)
		mockCommand := mocks.NewMockCommand(ctrl)

		mockRunner.EXPECT().
			Command("invalid", gomock.Any()).
			Return(mockCommand)

		mockCommand.EXPECT().SetStdout(gomock.Any())
		mockCommand.EXPECT().SetStderr(gomock.Any())
		mockCommand.EXPECT().SetSysProcAttr(gomock.Any())
		mockCommand.EXPECT().Start().Return(errors.New("failed to start"))

		handler := handlers.ExecutionHandler(mockRunner)

		cmd := &config.URLCommand{
			URL: "GET /invalid",
			CommandConfig: config.CommandConfig{
				CommandTemplate: "invalid",
			},
		}

		req := httptest.NewRequest(http.MethodGet, "/invalid", nil)
		ctx := context.WithValue(req.Context(), handlers.URLCommandKey, cmd)
		req = req.WithContext(ctx)

		recorder := httptest.NewRecorder()

		err := handler.ServeHTTP(recorder, req)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if !strings.Contains(recorder.Body.String(), "failed to start") {
			t.Errorf("expected error message 'failed to start' in body, got %q", recorder.Body.String())
		}
	})

	t.Run("CommandExecutionFailure", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockRunner := mocks.NewMockRunner(ctrl)
		mockCommand := mocks.NewMockCommand(ctrl)

		mockRunner.EXPECT().
			Command("exit", []string{"1"}).
			Return(mockCommand)

		mockCommand.EXPECT().SetStdout(gomock.Any()).Do(func(w io.Writer) {
			_, _ = w.Write([]byte("error message"))
		})
		mockCommand.EXPECT().SetStderr(gomock.Any())
		mockCommand.EXPECT().SetSysProcAttr(gomock.Any())
		mockCommand.EXPECT().Start().Return(nil)
		mockCommand.EXPECT().Wait().Return(errors.New("exit status 1"))
		mockCommand.EXPECT().ProcessState().Return(nil).AnyTimes()

		handler := handlers.ExecutionHandler(mockRunner)

		cmd := &config.URLCommand{
			URL: "GET /fail",
			CommandConfig: config.CommandConfig{
				CommandTemplate: "exit\n1",
			},
		}

		req := httptest.NewRequest(http.MethodGet, "/fail", nil)
		ctx := context.WithValue(req.Context(), handlers.URLCommandKey, cmd)
		req = req.WithContext(ctx)

		recorder := httptest.NewRecorder()

		err := handler.ServeHTTP(recorder, req)
		if err != nil {
			t.Fatalf("expected nil error, got %v", err)
		}

		if !strings.Contains(recorder.Body.String(), "Command failed with exit code: -1") {
			t.Errorf("expected failure message in body, got %q", recorder.Body.String())
		}

		if !strings.Contains(recorder.Body.String(), "exit status 1") {
			t.Errorf("expected error message in body, got %q", recorder.Body.String())
		}
	})

	t.Run("InvalidJSONBody", func(t *testing.T) {
		t.Parallel()

		handler := handlers.ExecutionHandler(nil)

		cmd := &config.URLCommand{
			URL: "POST /json",
			CommandConfig: config.CommandConfig{
				CommandTemplate: "echo\n{{.body.json.foo}}",
				Params: config.ParamsConfig{
					BodyAsJSON: ptrBool(true),
				},
			},
		}

		req := httptest.NewRequest(http.MethodPost, "/json", strings.NewReader(`{invalid json}`))
		ctx := context.WithValue(req.Context(), handlers.URLCommandKey, cmd)
		req = req.WithContext(ctx)

		recorder := httptest.NewRecorder()

		err := handler.ServeHTTP(recorder, req)
		if err == nil {
			t.Fatal("expected error, got nil")
		}

		if !strings.Contains(err.Error(), "failed to parse JSON body") {
			t.Errorf("expected JSON decoding error, got %v", err)
		}
	})

	testCases := []struct {
		name            string
		method          string
		url             string
		commandTemplate string
		bodyAsJSON      bool
		requestBody     string
		expectedArgs    []string
		outputType      string
		timeout         time.Duration
	}{
		{
			name:            "With url parameter",
			method:          http.MethodGet,
			url:             "/test?foo=bar",
			commandTemplate: "echo\n{{.url.foo}}",
			expectedArgs:    []string{"bar"},
		},
		{
			name:            "With body parameters",
			method:          http.MethodPost,
			url:             "/test",
			commandTemplate: "echo\n{{.body.text}}",
			requestBody:     "hello body",
			expectedArgs:    []string{"hello body"},
		},
		{
			name:            "With bodyAsJson parameter",
			method:          http.MethodPost,
			url:             "/test",
			commandTemplate: "echo\n{{.body.json.foo}}",
			bodyAsJSON:      true,
			requestBody:     `{"foo": "bar"}`,
			expectedArgs:    []string{"bar"},
		},
		{
			name:            "Async with timeout",
			method:          http.MethodGet,
			url:             "/test",
			commandTemplate: "echo\nhello",
			expectedArgs:    []string{"hello"},
			outputType:      "none",
			timeout:         time.Hour,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockRunner := mocks.NewMockRunner(ctrl)
			mockCommand := mocks.NewMockCommand(ctrl)

			mockRunner.EXPECT().
				Command("echo", tc.expectedArgs).
				Return(mockCommand)

			if tc.outputType == "none" {
				mockCommand.EXPECT().Pid().Return(1234).AnyTimes()
				mockRunner.EXPECT().Kill(1234, syscall.SIGKILL).Return(nil).AnyTimes()
			}

			mockCommand.EXPECT().SetStdout(gomock.Any()).Do(func(w io.Writer) {
				_, _ = w.Write([]byte("ok"))
			}).AnyTimes()
			mockCommand.EXPECT().SetStderr(gomock.Any()).AnyTimes()
			mockCommand.EXPECT().SetSysProcAttr(gomock.Any()).AnyTimes()
			mockCommand.EXPECT().Start().Return(nil).AnyTimes()
			mockCommand.EXPECT().Wait().Return(nil).AnyTimes()
			mockCommand.EXPECT().ProcessState().Return(nil).AnyTimes()

			handler := handlers.ExecutionHandler(mockRunner)

			cmd := &config.URLCommand{
				URL: tc.url,
				CommandConfig: config.CommandConfig{
					CommandTemplate: tc.commandTemplate,
					Params: config.ParamsConfig{
						BodyAsJSON: ptrBool(tc.bodyAsJSON),
					},
					OutputType: tc.outputType,
					Timeout:    &tc.timeout,
				},
			}

			var bodyReader io.Reader
			if tc.requestBody != "" {
				bodyReader = strings.NewReader(tc.requestBody)
			}

			req := httptest.NewRequest(tc.method, tc.url, bodyReader)
			ctx := context.WithValue(req.Context(), handlers.URLCommandKey, cmd)

			if tc.timeout > 0 {
				var cancel context.CancelFunc
				ctx, cancel = context.WithTimeout(ctx, tc.timeout)

				defer cancel()
			}

			req = req.WithContext(ctx)
			recorder := httptest.NewRecorder()

			err := handler.ServeHTTP(recorder, req)
			if err != nil {
				t.Errorf("ExecutionHandler returned error: %v", err)
			}

			if tc.outputType != "none" && recorder.Body.String() != "ok" {
				t.Errorf("expected 'ok', got %q", recorder.Body.String())
			}
		})
	}
}

func ptrBool(b bool) *bool {
	return &b
}
