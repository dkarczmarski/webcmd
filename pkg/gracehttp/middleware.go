package gracehttp

import "net/http"

// RejectOnShutdown wraps an http.Handler and returns 503 Service Unavailable if shutdown has been initiated.
func RejectOnShutdown(shutdown <-chan struct{}, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		select {
		case <-shutdown:
			http.Error(writer, "shutting down", http.StatusServiceUnavailable)

			return
		default:
			next.ServeHTTP(writer, request)
		}
	})
}
