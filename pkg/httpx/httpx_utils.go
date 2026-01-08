package httpx

import (
	"errors"
	"log"
	"net/http"
	"runtime/debug"
)

// WebError represents an HTTP error with an associated HTTP status code and an optional public message.
type WebError struct {
	err        error
	httpStatus int
	message    string
}

// NewWebError creates a new WebError.
func NewWebError(err error, status int, message string) *WebError {
	return &WebError{
		err:        err,
		httpStatus: status,
		message:    message,
	}
}

func (e *WebError) Error() string {
	if e == nil {
		return "<nil>"
	}

	if e.err != nil {
		return e.err.Error()
	}

	if e.message != "" {
		return e.message
	}

	return "web error"
}

func (e *WebError) Unwrap() error { return e.err }

// HTTPStatus returns the associated HTTP status code.
func (e *WebError) HTTPStatus() int { return e.httpStatus }

// Message returns the optional public message.
func (e *WebError) Message() string { return e.message }

type statusCoder interface {
	error
	HTTPStatus() int
}

type messageCarrier interface {
	Message() string
}

// Compile-time check.
var (
	_ statusCoder    = (*WebError)(nil)
	_ messageCarrier = (*WebError)(nil)
)

// ErrorSink returns a terminal handler that logs errors and writes appropriate HTTP responses.
// If logger is nil, log.Default() is used.
func ErrorSink(logger *log.Logger) func(WebHandler) http.Handler {
	if logger == nil {
		logger = log.Default()
	}

	return func(next WebHandler) http.Handler {
		return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
			err := next.ServeHTTP(responseWriter, request)
			if err == nil {
				return
			}

			status := http.StatusInternalServerError
			msg := ""

			var sc statusCoder
			if errors.As(err, &sc) {
				status = sc.HTTPStatus()

				if mc, ok := sc.(messageCarrier); ok {
					msg = mc.Message()
				}
			}

			if status >= http.StatusInternalServerError {
				logger.Printf("[ERROR] %s %s: %v\nStack Trace:\n%s",
					request.Method, request.URL.Path, err, debug.Stack(),
				)
			} else {
				logger.Printf("[WARN] %s %s: %v", request.Method, request.URL.Path, err)
			}

			if msg != "" {
				http.Error(responseWriter, msg, status)

				return
			}

			responseWriter.WriteHeader(status)
		})
	}
}
