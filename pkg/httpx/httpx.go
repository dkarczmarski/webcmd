// Package httpx provides extensions to the standard net/http package,
// including error-returning handlers and middleware chaining.
package httpx

import "net/http"

// WebHandler is an interface for handling web requests that return an error.
type WebHandler interface {
	ServeHTTP(responseWriter http.ResponseWriter, request *http.Request) error
}

// WebHandlerFunc is an adapter to allow the use of ordinary functions as web handlers.
type WebHandlerFunc func(http.ResponseWriter, *http.Request) error

func (f WebHandlerFunc) ServeHTTP(w http.ResponseWriter, r *http.Request) error {
	return f(w, r)
}

// Middleware is a function that wraps a WebHandler and returns a new WebHandler.
type Middleware func(next WebHandler) WebHandler

// WithMiddleware wraps a WebHandler with a single Middleware.
func WithMiddleware(middleware Middleware, handler WebHandler) WebHandler { //nolint:ireturn
	return middleware(handler)
}

// Chain combines multiple Middleware into a single Middleware.
// The middleware are executed in the order they are provided.
func Chain(middleware ...Middleware) Middleware {
	return func(handler WebHandler) WebHandler {
		finalHandler := handler

		for i := len(middleware) - 1; i >= 0; i-- {
			finalHandler = middleware[i](finalHandler)
		}

		return finalHandler
	}
}

// ToHandler converts a WebHandler into a standard http.Handler using a terminal handler.
func ToHandler(
	terminalHandler func(WebHandler) http.Handler,
	webHandler WebHandler,
) http.Handler {
	return terminalHandler(webHandler)
}
