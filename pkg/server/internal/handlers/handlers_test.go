package handlers_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dkarczmarski/webcmd/pkg/config"
	"github.com/dkarczmarski/webcmd/pkg/server/internal/handlers"
	"github.com/dkarczmarski/webcmd/pkg/server/internal/mocks"
	"go.uber.org/mock/gomock"
)

func TestHandleURLCommand(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{
		URLCommands: []config.URLCommand{
			{
				URL:             "GET /test",
				CommandTemplate: "echo test",
				Timeout:         5,
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
			DoAndReturn(func(_ context.Context, cmd *config.URLCommand, _ map[string]interface{}) handlers.CommandResult {
				if cmd.URL != "GET /test" {
					t.Errorf("unexpected command URL: %s", cmd.URL)
				}

				return expectedResult
			})

		handlers.HandleURLCommand(recorder, req, cfg, mockExecutor)

		if recorder.Code != http.StatusOK {
			t.Errorf("expected status OK, got %d", recorder.Code)
		}

		if recorder.Body.String() != "test output" {
			t.Errorf("expected body 'test output', got %q", recorder.Body.String())
		}
	})

	t.Run("not found", func(t *testing.T) {
		t.Parallel()

		ctrl := gomock.NewController(t)
		mockExecutor := mocks.NewMockCommandExecutor(ctrl)

		req := httptest.NewRequest(http.MethodGet, "/non-existent", nil)
		recorder := httptest.NewRecorder()

		handlers.HandleURLCommand(recorder, req, cfg, mockExecutor)

		if recorder.Code != http.StatusNotFound {
			t.Errorf("expected status NotFound, got %d", recorder.Code)
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

		handlers.HandleURLCommand(recorder, req, cfg, mockExecutor)

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
			DoAndReturn(func(_ context.Context, _ *config.URLCommand, params map[string]interface{}) handlers.CommandResult {
				urlParams := params["url"].(map[string]string) //nolint:forcetypeassert
				if urlParams["param1"] != "val1" || urlParams["param2"] != "val2" {
					t.Errorf("unexpected query params: %+v", urlParams)
				}

				return handlers.CommandResult{ExitCode: 0, Output: "ok"}
			})

		handlers.HandleURLCommand(recorder, req, cfg, mockExecutor)

		if recorder.Code != http.StatusOK {
			t.Errorf("expected status OK, got %d", recorder.Code)
		}
	})
}
