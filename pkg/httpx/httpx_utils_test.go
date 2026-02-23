package httpx_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dkarczmarski/webcmd/pkg/httpx"
)

func TestErrorSink(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		err             error
		withErrorHeader bool
		expectedHeader  string
		expectedStatus  int
	}{
		{
			name:            "No error header, normal error",
			err:             errors.New("standard error"),
			withErrorHeader: false,
			expectedHeader:  "",
			expectedStatus:  http.StatusInternalServerError,
		},
		{
			name:            "With error header, normal error",
			err:             errors.New("standard error"),
			withErrorHeader: true,
			expectedHeader:  "standard error",
			expectedStatus:  http.StatusInternalServerError,
		},
		{
			name:            "With error header, WebError with message",
			err:             httpx.NewWebError(errors.New("internal"), http.StatusBadRequest, "public message"),
			withErrorHeader: true,
			expectedHeader:  "public message",
			expectedStatus:  http.StatusBadRequest,
		},
		{
			name:            "With error header, WebError without message",
			err:             httpx.NewWebError(errors.New("internal error message"), http.StatusBadRequest, ""),
			withErrorHeader: true,
			expectedHeader:  "internal error message",
			expectedStatus:  http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			handler := httpx.WebHandlerFunc(func(_ http.ResponseWriter, _ *http.Request) error {
				return tt.err
			})

			sink := httpx.ErrorSink(nil, tt.withErrorHeader)
			h := sink(handler)

			req := httptest.NewRequest(http.MethodGet, "http://example.com", nil)
			w := httptest.NewRecorder()

			h.ServeHTTP(w, req)

			if w.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, w.Code)
			}

			gotHeader := w.Header().Get("X-Error-Message")
			if gotHeader != tt.expectedHeader {
				t.Errorf("expected header X-Error-Message %q, got %q", tt.expectedHeader, gotHeader)
			}
		})
	}
}
