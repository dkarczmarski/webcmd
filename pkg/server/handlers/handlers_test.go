package handlers_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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

		mockExecutor := mocks.NewMockCommandExecutor(ctrl)
		mockExecutor.EXPECT().
			RunCommand(gomock.Any(), "echo", []string{"hello"}, gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, _ []string, w io.Writer) (int, error) {
				_, _ = w.Write([]byte("hello\n"))

				return 0, nil
			})

		handler := handlers.ExecutionHandler(mockExecutor)

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

	t.Run("CommandNotFound", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockExecutor := mocks.NewMockCommandExecutor(ctrl)
		handler := handlers.ExecutionHandler(mockExecutor)

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

	t.Run("CommandExecutionFailure", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockExecutor := mocks.NewMockCommandExecutor(ctrl)
		mockExecutor.EXPECT().
			RunCommand(gomock.Any(), "exit", []string{"1"}, gomock.Any()).
			DoAndReturn(func(_ context.Context, _ string, _ []string, w io.Writer) (int, error) {
				_, _ = w.Write([]byte("error message"))

				return 1, errors.New("exit status 1")
			})

		handler := handlers.ExecutionHandler(mockExecutor)

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

		if !strings.Contains(recorder.Body.String(), "Command failed with exit code: 1") {
			t.Errorf("expected failure message in body, got %q", recorder.Body.String())
		}

		if !strings.Contains(recorder.Body.String(), "exit status 1") {
			t.Errorf("expected error message in body, got %q", recorder.Body.String())
		}
	})

	testCases := []struct {
		name            string
		method          string
		url             string
		commandTemplate string
		bodyAsText      bool
		bodyAsJSON      bool
		requestBody     string
		expectedArgs    []string
	}{
		{
			name:            "With url parameter",
			method:          http.MethodGet,
			url:             "/test?foo=bar",
			commandTemplate: "echo\n{{.url.foo}}",
			expectedArgs:    []string{"bar"},
		},
		{
			name:            "With bodyAsText parameter",
			method:          http.MethodPost,
			url:             "/test",
			commandTemplate: "echo\n{{.body.text}}",
			bodyAsText:      true,
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
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockExecutor := mocks.NewMockCommandExecutor(ctrl)
			mockExecutor.EXPECT().
				RunCommand(gomock.Any(), "echo", tc.expectedArgs, gomock.Any()).
				DoAndReturn(func(_ context.Context, _ string, _ []string, w io.Writer) (int, error) {
					_, _ = w.Write([]byte("ok"))

					return 0, nil
				})

			handler := handlers.ExecutionHandler(mockExecutor)

			cmd := &config.URLCommand{
				URL: tc.url,
				CommandConfig: config.CommandConfig{
					CommandTemplate: tc.commandTemplate,
					Params: config.ParamsConfig{
						BodyAsText: ptrBool(tc.bodyAsText),
						BodyAsJSON: ptrBool(tc.bodyAsJSON),
					},
				},
			}

			var bodyReader io.Reader
			if tc.requestBody != "" {
				bodyReader = strings.NewReader(tc.requestBody)
			}

			req := httptest.NewRequest(tc.method, tc.url, bodyReader)
			ctx := context.WithValue(req.Context(), handlers.URLCommandKey, cmd)
			req = req.WithContext(ctx)

			recorder := httptest.NewRecorder()

			err := handler.ServeHTTP(recorder, req)
			if err != nil {
				t.Errorf("ExecutionHandler returned error: %v", err)
			}

			if recorder.Body.String() != "ok" {
				t.Errorf("expected 'ok', got %q", recorder.Body.String())
			}
		})
	}
}

func ptrBool(b bool) *bool {
	return &b
}
