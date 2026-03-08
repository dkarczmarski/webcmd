//go:build integration

package integration_test

import (
	_ "embed"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dkarczmarski/webcmd/pkg/config"
	"github.com/dkarczmarski/webcmd/pkg/server"
)

//go:embed test-data/test-config.yaml
var testConfigYaml string

func setupServer(t *testing.T) *server.Server {
	t.Helper()

	configuration, err := config.LoadConfigFromString(testConfigYaml)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	return server.New(configuration)
}

func TestServer_RoutingAndAuth(t *testing.T) {
	t.Parallel()

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
}

func TestServer_CommandExecution(t *testing.T) {
	t.Parallel()

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

	t.Run("Command failure - Exit Code 1", func(t *testing.T) {
		t.Parallel()
		srv := setupServer(t)

		req := httptest.NewRequest(http.MethodGet, "/cmd/false", nil)
		rec := httptest.NewRecorder()

		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status code %d, got %d", http.StatusOK, rec.Code)
		}

		if rec.Header().Get("X-Success") != "false" {
			t.Errorf("Expected X-Success: false, got %q", rec.Header().Get("X-Success"))
		}

		if rec.Header().Get("X-Exit-Code") != "1" {
			t.Errorf("Expected X-Exit-Code: 1, got %q", rec.Header().Get("X-Exit-Code"))
		}
	})

	t.Run("Execution Timeout", func(t *testing.T) {
		t.Parallel()
		srv := setupServer(t)

		req := httptest.NewRequest(http.MethodGet, "/cmd/sleep", nil)
		rec := httptest.NewRecorder()

		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("Expected status code %d, got %d", http.StatusOK, rec.Code)
		}

		if rec.Header().Get("X-Success") != "false" {
			t.Errorf("Expected X-Success: false, got %q", rec.Header().Get("X-Success"))
		}

		errMsg := rec.Header().Get("X-Error-Message")
		expectedMsg := "executor runtime error: process wait failed: signal: killed"

		if errMsg != expectedMsg {
			t.Errorf("Expected timeout error message in X-Error-Message header to be %q, got %q", expectedMsg, errMsg)
		}
	})
}

func TestServer_RequestParams(t *testing.T) {
	t.Parallel()

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

	t.Run("POST /cmd/echo-text - empty body", func(t *testing.T) {
		t.Parallel()
		srv := setupServer(t)

		req := httptest.NewRequest(http.MethodPost, "/cmd/echo-text", nil)
		rec := httptest.NewRecorder()

		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("Expected status %d, got %d. Body: %q", http.StatusOK, rec.Code, rec.Body.String())
		}

		if rec.Body.String() != "" {
			t.Fatalf("Expected empty output, got %q", rec.Body.String())
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

	t.Run("POST /cmd/echo-json - non-object JSON returns 400", func(t *testing.T) {
		t.Parallel()
		srv := setupServer(t)

		req := httptest.NewRequest(http.MethodPost, "/cmd/echo-json", strings.NewReader(`[1,2,3]`))
		req.Header.Set("Content-Type", "application/json")

		rec := httptest.NewRecorder()

		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Fatalf("Expected status %d, got %d. Body: %q", http.StatusBadRequest, rec.Code, rec.Body.String())
		}
	})

	t.Run("GET /cmd/query-first - uses only first query value", func(t *testing.T) {
		t.Parallel()
		srv := setupServer(t)

		req := httptest.NewRequest(http.MethodGet, "/cmd/query-first?a=1&a=2", nil)
		rec := httptest.NewRecorder()

		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("Expected status %d, got %d. Body: %q", http.StatusOK, rec.Code, rec.Body.String())
		}

		if rec.Body.String() != "1" {
			t.Fatalf("Expected output %q, got %q", "1", rec.Body.String())
		}
	})

	t.Run("GET /cmd/headers - normalizes headers and joins multiple values", func(t *testing.T) {
		t.Parallel()
		srv := setupServer(t)

		req := httptest.NewRequest(http.MethodGet, "/cmd/headers", nil)
		req.Header.Add("X-Test-Header", "a")
		req.Header.Add("X-Test", "val1")
		req.Header.Add("X-Test", "val2")

		rec := httptest.NewRecorder()
		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("Expected status %d, got %d. Body: %q", http.StatusOK, rec.Code, rec.Body.String())
		}

		expected := "a val1; val2"
		if rec.Body.String() != expected {
			t.Fatalf("Expected output %q, got %q", expected, rec.Body.String())
		}
	})

	t.Run("POST /cmd/echo-json-disabled - BodyAsJSON=false does not populate body.json", func(t *testing.T) {
		t.Parallel()
		srv := setupServer(t)

		body := `{"a":1}`
		req := httptest.NewRequest(http.MethodPost, "/cmd/echo-json-disabled", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")

		rec := httptest.NewRecorder()

		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("Expected status %d, got %d. Body: %q", http.StatusOK, rec.Code, rec.Body.String())
		}

		expected := body + " <no value>"
		if rec.Body.String() != expected {
			t.Fatalf("Expected output %q, got %q", expected, rec.Body.String())
		}
	})

	t.Run("POST /cmd/echo-text - request body read error returns 500", func(t *testing.T) {
		t.Parallel()
		srv := setupServer(t)

		req := httptest.NewRequest(http.MethodPost, "/cmd/echo-text", nil)
		req.Body = io.NopCloser(&errorReader{}) // force ReadAll to fail
		rec := httptest.NewRecorder()

		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("Expected status %d, got %d. Body: %q", http.StatusInternalServerError, rec.Code, rec.Body.String())
		}

		errMsg := rec.Header().Get("X-Error-Message")
		if errMsg == "" {
			t.Fatalf("Expected X-Error-Message to be set")
		}

		if !strings.Contains(errMsg, "failed to read request body") {
			t.Fatalf("Expected X-Error-Message to contain %q, got %q", "failed to read request body", errMsg)
		}

		if !strings.Contains(errMsg, "unexpected EOF") {
			t.Fatalf("Expected X-Error-Message to contain %q, got %q", "unexpected EOF", errMsg)
		}
	})
}

func TestServer_OutputTypes(t *testing.T) {
	t.Parallel()

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

	t.Run("GET /cmd/bad-output - unknown execution mode returns 500", func(t *testing.T) {
		t.Parallel()
		srv := setupServer(t)

		req := httptest.NewRequest(http.MethodGet, "/cmd/bad-output", nil)
		rec := httptest.NewRecorder()

		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("Expected status %d, got %d. Body: %q", http.StatusInternalServerError, rec.Code, rec.Body.String())
		}

		errMsg := rec.Header().Get("X-Error-Message")
		if errMsg == "" || !strings.Contains(errMsg, "unknown execution mode") {
			t.Fatalf("Expected X-Error-Message to contain %q, got %q", "unknown execution mode", errMsg)
		}
	})
}

func TestServer_Templates(t *testing.T) {
	t.Parallel()

	t.Run("GET /cmd/bad-template - template syntax error returns 500", func(t *testing.T) {
		t.Parallel()
		srv := setupServer(t)

		req := httptest.NewRequest(http.MethodGet, "/cmd/bad-template", nil)
		rec := httptest.NewRecorder()

		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("Expected status %d, got %d. Body: %q", http.StatusInternalServerError, rec.Code, rec.Body.String())
		}

		errMsg := rec.Header().Get("X-Error-Message")
		if errMsg == "" || !strings.Contains(errMsg, "error building command") {
			t.Fatalf("Expected X-Error-Message to contain %q, got %q", "error building command", errMsg)
		}
	})
}

func TestServer_CallGate(t *testing.T) {
	t.Parallel()

	t.Run("CallGate busy returns 429 (same explicit group)", func(t *testing.T) {
		t.Parallel()
		srv := setupServer(t)

		req1 := httptest.NewRequest(http.MethodGet, "/cmd/gated-sleep", nil)
		rec1 := httptest.NewRecorder()

		done1 := make(chan struct{})
		go func() {
			srv.ServeHTTP(rec1, req1)
			close(done1)
		}()

		time.Sleep(50 * time.Millisecond)

		req2 := httptest.NewRequest(http.MethodGet, "/cmd/gated-sleep", nil)
		rec2 := httptest.NewRecorder()
		srv.ServeHTTP(rec2, req2)

		if rec2.Code != http.StatusTooManyRequests {
			t.Fatalf("Expected status %d, got %d. Body: %q", http.StatusTooManyRequests, rec2.Code, rec2.Body.String())
		}

		<-done1
	})

	t.Run("CallGate implicit group name isolates different URLs", func(t *testing.T) {
		t.Parallel()
		srv := setupServer(t)

		req1 := httptest.NewRequest(http.MethodGet, "/cmd/gated-implicit-a", nil)
		rec1 := httptest.NewRecorder()

		done1 := make(chan struct{})
		go func() {
			srv.ServeHTTP(rec1, req1)
			close(done1)
		}()

		time.Sleep(50 * time.Millisecond)

		req2 := httptest.NewRequest(http.MethodGet, "/cmd/gated-implicit-b", nil)
		rec2 := httptest.NewRecorder()
		srv.ServeHTTP(rec2, req2)

		if rec2.Code != http.StatusOK {
			t.Fatalf("Expected status %d, got %d. Body: %q", http.StatusOK, rec2.Code, rec2.Body.String())
		}

		<-done1
	})

	t.Run("CallGate empty group name shared across URLs => second returns 429", func(t *testing.T) {
		t.Parallel()
		srv := setupServer(t)

		req1 := httptest.NewRequest(http.MethodGet, "/cmd/gated-empty-a", nil)
		rec1 := httptest.NewRecorder()

		done1 := make(chan struct{})
		go func() {
			srv.ServeHTTP(rec1, req1)
			close(done1)
		}()

		time.Sleep(50 * time.Millisecond)

		req2 := httptest.NewRequest(http.MethodGet, "/cmd/gated-empty-b", nil)
		rec2 := httptest.NewRecorder()
		srv.ServeHTTP(rec2, req2)

		if rec2.Code != http.StatusTooManyRequests {
			t.Fatalf("Expected status %d, got %d. Body: %q", http.StatusTooManyRequests, rec2.Code, rec2.Body.String())
		}

		<-done1
	})

	t.Run("CallGate invalid mode returns 500", func(t *testing.T) {
		t.Parallel()
		srv := setupServer(t)

		req := httptest.NewRequest(http.MethodGet, "/cmd/gated-invalid-mode", nil)
		rec := httptest.NewRecorder()

		srv.ServeHTTP(rec, req)

		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("Expected status %d, got %d. Body: %q", http.StatusInternalServerError, rec.Code, rec.Body.String())
		}

		errMsg := rec.Header().Get("X-Error-Message")
		if errMsg == "" || !strings.Contains(errMsg, "invalid callgate mode") {
			t.Fatalf("Expected X-Error-Message to contain %q, got %q", "invalid callgate mode", errMsg)
		}
	})
}

type errorReader struct{}

func (e *errorReader) Read(_ []byte) (int, error) {
	return 0, io.ErrUnexpectedEOF
}
