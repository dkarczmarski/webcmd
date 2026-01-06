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

	cfg, err := config.LoadConfigFromString(testConfigYaml)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	srv := server.New(cfg)

	t.Run("POST /cmd/echo", func(t *testing.T) {
		t.Parallel()

		req := httptest.NewRequest(http.MethodPost, "/cmd/echo?param1=hello", nil)
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

	t.Run("404 Not Found", func(t *testing.T) {
		t.Parallel()

		req := httptest.NewRequest(http.MethodGet, "/cmd/non-existent", nil)
		rec := httptest.NewRecorder()

		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("Expected status code %d, got %d", http.StatusNotFound, rec.Code)
		}
	})
}
