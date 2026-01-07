package handlers_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dkarczmarski/webcmd/pkg/config"
	"github.com/dkarczmarski/webcmd/pkg/server/internal/handlers"
	"github.com/dkarczmarski/webcmd/pkg/server/internal/mocks"
	"go.uber.org/mock/gomock"
)

func TestAuthAndRouteMiddleware(t *testing.T) {
	t.Parallel()

	configuration := &config.Config{
		Authorization: []config.AuthorizationConfig{
			{
				Name: "user1",
				Key:  "key1",
			},
			{
				Name: "user2",
				Key:  "key2",
			},
		},
		URLCommands: []config.URLCommand{
			{
				URL: "GET /test",
				CommandConfig: config.CommandConfig{
					CommandTemplate: "echo test",
					Timeout:         5,
				},
			},
			{
				URL:               "GET /secure",
				AuthorizationName: "user2",
				CommandConfig: config.CommandConfig{
					CommandTemplate: "echo secure",
					Timeout:         5,
				},
			},
		},
	}

	next := func(responseWriter http.ResponseWriter, r *http.Request) {
		cmdConfig, ok := r.Context().Value(handlers.CommandConfigKey).(*config.CommandConfig)
		if !ok || cmdConfig == nil {
			t.Error("CommandConfig missing from context")
		}

		responseWriter.WriteHeader(http.StatusOK)

		_, _ = fmt.Fprint(responseWriter, "ok")
	}

	tests := []struct {
		name           string
		method         string
		url            string
		apiKey         string
		expectedStatus int
	}{
		{
			name:           "no auth required and valid route",
			method:         http.MethodGet,
			url:            "/test",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "no auth required and invalid key",
			method:         http.MethodGet,
			url:            "/test",
			apiKey:         "wrong",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "no auth required and invalid key and route not found",
			method:         http.MethodGet,
			url:            "/unknown",
			apiKey:         "invalid-key",
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "no auth required and valid key and route not found",
			method:         http.MethodGet,
			url:            "/unknown",
			apiKey:         "key1",
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "no auth required and no key and route not found",
			method:         http.MethodGet,
			url:            "/unknown",
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "valid auth with specific user and route",
			method:         http.MethodGet,
			url:            "/secure",
			apiKey:         "key2",
			expectedStatus: http.StatusOK,
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(testCase.method, testCase.url, nil)
			if testCase.apiKey != "" {
				req.Header.Set("X-Api-Key", testCase.apiKey)
			}

			recorder := httptest.NewRecorder()

			handlers.AuthAndRouteMiddleware(next, configuration)(recorder, req)

			if recorder.Code != testCase.expectedStatus {
				t.Errorf("expected status %d, got %d. Body: %s", testCase.expectedStatus, recorder.Code, recorder.Body.String())
			}
		})
	}
}

func TestURLCommandHandler(t *testing.T) {
	t.Parallel()

	configuration := &config.Config{
		URLCommands: []config.URLCommand{
			{
				URL: "GET /test",
				CommandConfig: config.CommandConfig{
					CommandTemplate: "echo test",
					Timeout:         5,
				},
			},
		},
	}

	t.Run("success", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		mockExecutor := mocks.NewMockCommandExecutor(ctrl)

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		recorder := httptest.NewRecorder()

		expectedResult := handlers.CommandResult{
			ExitCode: 0,
			Output:   "test output",
		}

		mockExecutor.EXPECT().
			RunCommand(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(
				_ context.Context,
				cmdConfig *config.CommandConfig,
				_ map[string]interface{},
			) handlers.CommandResult {
				if cmdConfig.CommandTemplate != "echo test" {
					t.Errorf("unexpected command template: %s", cmdConfig.CommandTemplate)
				}

				return expectedResult
			})

		ctx := context.WithValue(req.Context(), handlers.CommandConfigKey, &configuration.URLCommands[0].CommandConfig)
		handlers.URLCommandHandler(recorder, req.WithContext(ctx), mockExecutor)

		if recorder.Code != http.StatusOK {
			t.Errorf("expected status OK, got %d", recorder.Code)
		}

		if recorder.Body.String() != "test output" {
			t.Errorf("expected body 'test output', got %q", recorder.Body.String())
		}
	})

	t.Run("execution error", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		mockExecutor := mocks.NewMockCommandExecutor(ctrl)

		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		recorder := httptest.NewRecorder()

		expectedResult := handlers.CommandResult{
			ExitCode: 1,
			Output:   "error message",
		}

		mockExecutor.EXPECT().
			RunCommand(gomock.Any(), gomock.Any(), gomock.Any()).
			Return(expectedResult)

		ctx := context.WithValue(req.Context(), handlers.CommandConfigKey, &configuration.URLCommands[0].CommandConfig)
		handlers.URLCommandHandler(recorder, req.WithContext(ctx), mockExecutor)

		if recorder.Code != http.StatusInternalServerError {
			t.Errorf("expected status InternalServerError, got %d", recorder.Code)
		}

		expectedBody := "Command failed with exit code 1\nOutput: error message"
		if recorder.Body.String() != expectedBody {
			t.Errorf("expected body %q, got %q", expectedBody, recorder.Body.String())
		}
	})

	t.Run("query params", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		mockExecutor := mocks.NewMockCommandExecutor(ctrl)

		req := httptest.NewRequest(http.MethodGet, "/test?param1=val1&param2=val2", nil)
		recorder := httptest.NewRecorder()

		mockExecutor.EXPECT().
			RunCommand(gomock.Any(), gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, _ *config.CommandConfig, params map[string]interface{}) handlers.CommandResult {
				urlParams := params["url"].(map[string]string) //nolint:forcetypeassert
				if urlParams["param1"] != "val1" || urlParams["param2"] != "val2" {
					t.Errorf("unexpected query params: %+v", urlParams)
				}

				return handlers.CommandResult{ExitCode: 0, Output: "ok"}
			})

		ctx := context.WithValue(req.Context(), handlers.CommandConfigKey, &configuration.URLCommands[0].CommandConfig)
		handlers.URLCommandHandler(recorder, req.WithContext(ctx), mockExecutor)

		if recorder.Code != http.StatusOK {
			t.Errorf("expected status OK, got %d", recorder.Code)
		}
	})

	t.Run("body as text", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		mockExecutor := mocks.NewMockCommandExecutor(ctrl)

		bodyContent := "hello world body"
		req := httptest.NewRequest(http.MethodPost, "/test", strings.NewReader(bodyContent))
		recorder := httptest.NewRecorder()

		cmdConfig := &config.CommandConfig{
			CommandTemplate: "echo {{.bodyAsText}}",
			BodyAsText:      true,
		}

		mockExecutor.EXPECT().
			RunCommand(gomock.Any(), cmdConfig, gomock.Any()).
			DoAndReturn(func(_ context.Context, _ *config.CommandConfig, params map[string]interface{}) handlers.CommandResult {
				if params["bodyAsText"] != bodyContent {
					t.Errorf("expected bodyAsText %q, got %q", bodyContent, params["bodyAsText"])
				}

				return handlers.CommandResult{ExitCode: 0, Output: "ok"}
			})

		ctx := context.WithValue(req.Context(), handlers.CommandConfigKey, cmdConfig)
		handlers.URLCommandHandler(recorder, req.WithContext(ctx), mockExecutor)

		if recorder.Code != http.StatusOK {
			t.Errorf("expected status OK, got %d", recorder.Code)
		}
	})

	t.Run("body as text error", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		mockExecutor := mocks.NewMockCommandExecutor(ctrl)

		// Create a reader that returns an error
		errReader := &errorReader{err: errors.New("read error")}
		req := httptest.NewRequest(http.MethodPost, "/test", errReader)
		recorder := httptest.NewRecorder()

		cmdConfig := &config.CommandConfig{
			CommandTemplate: "echo {{.bodyAsText}}",
			BodyAsText:      true,
		}

		// No RunCommand call expected since it should fail before

		ctx := context.WithValue(req.Context(), handlers.CommandConfigKey, cmdConfig)
		handlers.URLCommandHandler(recorder, req.WithContext(ctx), mockExecutor)

		if recorder.Code != http.StatusInternalServerError {
			t.Errorf("expected status InternalServerError, got %d", recorder.Code)
		}

		expectedBody := "Internal Server Error: failed to read request body"
		if recorder.Body.String() != expectedBody {
			t.Errorf("expected body %q, got %q", expectedBody, recorder.Body.String())
		}
	})
}

type errorReader struct {
	err error
}

func (r *errorReader) Read(_ []byte) (int, error) {
	return 0, r.err
}
