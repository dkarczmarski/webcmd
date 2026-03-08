package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/dkarczmarski/webcmd/pkg/httpx"
)

var (
	ErrUnauthorized          = errors.New("unauthorized")
	ErrInvalidRequestContext = errors.New("invalid request context")
	ErrBadConfiguration      = errors.New("bad configuration")
)

// RequestIDMiddleware creates a new Middleware that extracts the request ID from the X-Request-Id header,
// or generates a new one if not present, and adds it to the request context.
// It also sets the X-Request-Id header in the response.
func RequestIDMiddleware() httpx.Middleware {
	const header = "X-Request-Id"

	return func(next httpx.WebHandler) httpx.WebHandler {
		return httpx.WebHandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) error {
			rid := strings.TrimSpace(request.Header.Get(header))
			if rid == "" {
				rid = generateRequestID()
			}

			ctx := WithRequestID(request.Context(), rid)

			responseWriter.Header().Set(header, rid)

			return next.ServeHTTP(responseWriter, request.WithContext(ctx))
		})
	}
}

func generateRequestID() string {
	b := make([]byte, 4) //nolint:mnd
	_, _ = rand.Read(b)

	return hex.EncodeToString(b)
}

// APIKeyMiddleware creates a new Middleware that reads X-Api-Key header,
// finds the matching authorization name, and adds it to the request context.
func APIKeyMiddleware(resolver *RequestResolver) httpx.Middleware {
	return func(next httpx.WebHandler) httpx.WebHandler {
		return httpx.WebHandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) error {
			apiKey := request.Header.Get("X-Api-Key")

			if apiKey != "" {
				if authName, ok := resolver.ResolveAuthName(apiKey); ok {
					ctx := WithAuthName(request.Context(), authName)

					return next.ServeHTTP(responseWriter, request.WithContext(ctx))
				}
			}

			return next.ServeHTTP(responseWriter, request)
		})
	}
}

// URLCommandMiddleware creates a new Middleware that finds the matching URL command
// and adds it to the request context.
func URLCommandMiddleware(resolver *RequestResolver) httpx.Middleware {
	return func(next httpx.WebHandler) httpx.WebHandler {
		return httpx.WebHandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) error {
			requestURL := request.Method + " " + request.URL.Path

			if cmd, ok := resolver.ResolveURLCommand(requestURL); ok {
				ctx := WithURLCommand(request.Context(), cmd)

				return next.ServeHTTP(responseWriter, request.WithContext(ctx))
			}

			return next.ServeHTTP(responseWriter, request)
		})
	}
}

// AuthorizationMiddleware creates a new Middleware that checks if the user is authorized
// to execute the command based on the information in the request context.
// It supports multiple authorized names separated by commas in the command configuration.
func AuthorizationMiddleware() httpx.Middleware {
	return func(next httpx.WebHandler) httpx.WebHandler {
		return httpx.WebHandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) error {
			cmd, err := getURLCommandFromContext(request)
			if err != nil {
				return httpx.NewWebError(err, http.StatusNotFound, "Command not found")
			}

			if cmd.AuthorizationName == "" {
				return next.ServeHTTP(responseWriter, request)
			}

			authName, _ := AuthNameFromContext(request.Context())

			if authName == "" {
				return httpx.NewWebError(
					fmt.Errorf("authentication required for %s: %w", cmd.URL, ErrUnauthorized),
					http.StatusUnauthorized,
					"Authentication required: please provide a valid API key.",
				)
			}

			allowedNames := strings.Split(cmd.AuthorizationName, ",")
			for _, name := range allowedNames {
				if strings.TrimSpace(name) == authName {
					return next.ServeHTTP(responseWriter, request)
				}
			}

			return httpx.NewWebError(
				fmt.Errorf("user '%s' not authorized for %s: %w", authName, cmd.URL, ErrUnauthorized),
				http.StatusForbidden,
				fmt.Sprintf("Access denied: user '%s' does not have permission to execute this command.", authName),
			)
		})
	}
}

// TimeoutMiddleware creates a new Middleware that sets a timeout for the request context
// based on the command configuration.
func TimeoutMiddleware() httpx.Middleware {
	return func(next httpx.WebHandler) httpx.WebHandler {
		return httpx.WebHandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) error {
			cmd, err := getURLCommandFromContext(request)
			if err != nil {
				return httpx.NewWebError(err, http.StatusNotFound, "Command not found")
			}

			if cmd.Timeout != nil {
				ctx, cancel := context.WithTimeout(request.Context(), *cmd.Timeout)
				defer cancel()

				return next.ServeHTTP(responseWriter, request.WithContext(ctx))
			}

			return next.ServeHTTP(responseWriter, request)
		})
	}
}
