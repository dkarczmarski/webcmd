//go:build integration

package integration_test

import (
	_ "embed"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dkarczmarski/webcmd/pkg/config"
	"github.com/dkarczmarski/webcmd/pkg/server"
)

//go:embed test-data/test-config.yaml
var testConfigYaml string

func TestServerIntegration(t *testing.T) {
	t.Parallel()

	setupServer := func(t *testing.T) *server.Server {
		t.Helper()

		configuration, err := config.LoadConfigFromString(testConfigYaml)
		if err != nil {
			t.Fatalf("Failed to load config: %v", err)
		}

		return server.New(configuration)
	}

	t.Run("POST /cmd/echo", func(t *testing.T) {
		t.Parallel()
		srv := setupServer(t)

		req := httptest.NewRequest(http.MethodPost, "/cmd/echo?param1=hello", nil)

		req.Header.Set("X-Api-Key", "ABC111")

		rec := httptest.NewRecorder()

		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status code %d, got %d. Body: %s", http.StatusOK, rec.Code, rec.Body.String())
		}

		expectedOutput := "hello\n"
		if rec.Body.String() != expectedOutput {
			t.Errorf("Expected output %q, got %q", expectedOutput, rec.Body.String())
		}
	})

	t.Run("GET /cmd/date", func(t *testing.T) {
		t.Parallel()
		srv := setupServer(t)

		req := httptest.NewRequest(http.MethodGet, "/cmd/date", nil)
		rec := httptest.NewRecorder()

		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status code %d, got %d. Body: %s", http.StatusOK, rec.Code, rec.Body.String())
		}

		output := strings.TrimSpace(rec.Body.String())
		if len(output) == 0 {
			t.Errorf("Expected non-empty output from date command")
		}
	})

	t.Run("POST /cmd/echo-text", func(t *testing.T) {
		t.Parallel()
		srv := setupServer(t)

		body := "hello from body"
		req := httptest.NewRequest(http.MethodPost, "/cmd/echo-text", strings.NewReader(body))
		rec := httptest.NewRecorder()

		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status code %d, got %d. Body: %s", http.StatusOK, rec.Code, rec.Body.String())
		}

		expectedOutput := body
		if rec.Body.String() != expectedOutput {
			t.Errorf("Expected output %q, got %q", expectedOutput, rec.Body.String())
		}
	})

	t.Run("POST /cmd/echo-json", func(t *testing.T) {
		t.Parallel()
		srv := setupServer(t)

		body := `{"baz":123,"foo":"bar"}`
		req := httptest.NewRequest(http.MethodPost, "/cmd/echo-json", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")

		rec := httptest.NewRecorder()

		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status code %d, got %d. Body: %s", http.StatusOK, rec.Code, rec.Body.String())
		}

		expectedOutput := body
		if rec.Body.String() != expectedOutput {
			t.Errorf("Expected output %q, got %q", expectedOutput, rec.Body.String())
		}
	})

	t.Run("404 Not Found", func(t *testing.T) {
		t.Parallel()
		srv := setupServer(t)

		req := httptest.NewRequest(http.MethodGet, "/cmd/non-existent", nil)

		req.Header.Set("X-Api-Key", "ABC111")

		rec := httptest.NewRecorder()

		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("Expected status code %d, got %d", http.StatusNotFound, rec.Code)
		}
	})

	t.Run("401 Unauthorized - No API Key", func(t *testing.T) {
		t.Parallel()
		srv := setupServer(t)

		req := httptest.NewRequest(http.MethodPost, "/cmd/echo", nil)
		rec := httptest.NewRecorder()

		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("Expected status code %d, got %d", http.StatusUnauthorized, rec.Code)
		}
	})

	t.Run("401 Unauthorized - Invalid API Key", func(t *testing.T) {
		t.Parallel()
		srv := setupServer(t)

		req := httptest.NewRequest(http.MethodPost, "/cmd/echo", nil)
		req.Header.Set("X-Api-Key", "INVALID")

		rec := httptest.NewRecorder()

		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("Expected status code %d, got %d", http.StatusUnauthorized, rec.Code)
		}
	})

	t.Run("403 Forbidden - Insufficient Permissions", func(t *testing.T) {
		t.Parallel()
		srv := setupServer(t)

		req := httptest.NewRequest(http.MethodPost, "/cmd/forbidden", nil)
		req.Header.Set("X-Api-Key", "ABC222") // auth-name2, but command needs auth-name1

		rec := httptest.NewRecorder()

		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusForbidden {
			t.Errorf("Expected status code %d, got %d", http.StatusForbidden, rec.Code)
		}
	})

	t.Run("504 Gateway Timeout", func(t *testing.T) {
		t.Parallel()
		srv := setupServer(t)

		req := httptest.NewRequest(http.MethodGet, "/cmd/sleep", nil)
		rec := httptest.NewRecorder()

		srv.ServeHTTP(rec, req)

		// Intentional decision to return 200 OK because the command's exit status
		// is unknown when headers are sent (especially for streaming).
		// Error messages are appended to the response body.
		if rec.Code != http.StatusOK {
			t.Errorf("Expected status code %d, got %d", http.StatusOK, rec.Code)
		}

		if !strings.Contains(rec.Body.String(), "context deadline exceeded") {
			t.Errorf("Expected timeout error message in body, got %q", rec.Body.String())
		}
	})

	t.Run("Command failure - Exit Code 1", func(t *testing.T) {
		t.Parallel()
		srv := setupServer(t)

		req := httptest.NewRequest(http.MethodGet, "/cmd/false", nil)
		rec := httptest.NewRecorder()

		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status code %d, got %d", http.StatusOK, rec.Code)
		}

		if !strings.Contains(rec.Body.String(), "Command failed with exit code: 1") {
			t.Errorf("Expected error message in body, got %q", rec.Body.String())
		}
	})

	t.Run("Invalid JSON Body", func(t *testing.T) {
		t.Parallel()
		srv := setupServer(t)

		req := httptest.NewRequest(http.MethodPost, "/cmd/echo-json", strings.NewReader(`{invalid json`))
		req.Header.Set("Content-Type", "application/json")

		rec := httptest.NewRecorder()

		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("Expected status code %d, got %d", http.StatusBadRequest, rec.Code)
		}
	})

	t.Run("Method Not Allowed (treated as 404)", func(t *testing.T) {
		t.Parallel()
		srv := setupServer(t)

		// /cmd/echo is POST, trying GET
		req := httptest.NewRequest(http.MethodGet, "/cmd/echo", nil)
		rec := httptest.NewRecorder()

		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("Expected status code %d, got %d", http.StatusNotFound, rec.Code)
		}
	})

	t.Run("Output Type Stream", func(t *testing.T) {
		t.Parallel()
		srv := setupServer(t)

		req := httptest.NewRequest(http.MethodGet, "/cmd/stream", nil)
		rec := httptest.NewRecorder()

		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status code %d, got %d", http.StatusOK, rec.Code)
		}

		if rec.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
			t.Errorf("Expected Content-Type text/plain, got %q", rec.Header().Get("Content-Type"))
		}

		if rec.Body.String() != "stream-output\n" {
			t.Errorf("Expected %q, got %q", "stream-output\n", rec.Body.String())
		}
	})

	t.Run("Output Type None", func(t *testing.T) {
		t.Parallel()
		srv := setupServer(t)

		req := httptest.NewRequest(http.MethodGet, "/cmd/none", nil)
		rec := httptest.NewRecorder()

		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status code %d, got %d", http.StatusOK, rec.Code)
		}

		if rec.Body.Len() != 0 {
			t.Errorf("Expected empty body, got %q", rec.Body.String())
		}
	})
}
