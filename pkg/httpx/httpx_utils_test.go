package httpx_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/dkarczmarski/webcmd/pkg/httpx"
)

type customStatusError struct {
	err    error
	status int
	msg    string
}

func (e *customStatusError) Error() string   { return e.err.Error() }
func (e *customStatusError) HTTPStatus() int { return e.status }
func (e *customStatusError) Message() string { return e.msg }

func TestErrorSink(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		err             error
		withErrorHeader bool
		expectedHeader  string
		expectedStatus  int
		expectedBody    string
	}{
		{
			name:            "No error header, normal error (NOT statusCoder)",
			err:             errors.New("standard error"),
			withErrorHeader: false,
			expectedHeader:  "",
			expectedStatus:  http.StatusInternalServerError,
			expectedBody:    "",
		},
		{
			name:            "With error header, normal error (NOT statusCoder)",
			err:             errors.New("standard error"),
			withErrorHeader: true,
			expectedHeader:  "standard error",
			expectedStatus:  http.StatusInternalServerError,
			expectedBody:    "",
		},
		{
			name:            "With error header, WebError with message (statusCoder)",
			err:             httpx.NewWebError(errors.New("internal"), http.StatusBadRequest, "public message"),
			withErrorHeader: true,
			expectedHeader:  "public message",
			expectedStatus:  http.StatusBadRequest,
			expectedBody:    "public message\n",
		},
		{
			name:            "With error header, WebError without message (statusCoder)",
			err:             httpx.NewWebError(errors.New("internal error message"), http.StatusBadRequest, ""),
			withErrorHeader: true,
			expectedHeader:  "internal error message",
			expectedStatus:  http.StatusBadRequest,
			expectedBody:    "",
		},
		{
			name: "Custom statusCoder implementation",
			err: &customStatusError{
				err:    errors.New("custom error"),
				status: http.StatusTeapot,
				msg:    "I am a teapot",
			},
			withErrorHeader: false,
			expectedHeader:  "",
			expectedStatus:  http.StatusTeapot,
			expectedBody:    "I am a teapot\n",
		},
		{
			name:            "SilentError should be ignored",
			err:             httpx.NewSilentError(errors.New("silent error")),
			withErrorHeader: true,
			expectedHeader:  "",
			expectedStatus:  http.StatusOK,
			expectedBody:    "",
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

			gotBody := w.Body.String()
			if gotBody != tt.expectedBody {
				t.Errorf("expected body %q, got %q", tt.expectedBody, gotBody)
			}
		})
	}
}
