package server

import (
	"context"

	"github.com/dkarczmarski/webcmd/pkg/config"
)

type contextKey string

// authNameKey is the context key used to store and retrieve the authorization name.
const authNameKey contextKey = "authName"

// urlCommandKey is the context key used to store and retrieve the URL command.
const urlCommandKey contextKey = "urlCommand"

// requestIDKey is the context key used to store and retrieve the request ID.
const requestIDKey contextKey = "requestID"

// WithRequestID returns a new context with the request ID.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey, id)
}

// WithAuthName returns a new context with the authorization name.
func WithAuthName(ctx context.Context, authName string) context.Context {
	return context.WithValue(ctx, authNameKey, authName)
}

// WithURLCommand returns a new context with the URL command.
func WithURLCommand(ctx context.Context, cmd *config.URLCommand) context.Context {
	return context.WithValue(ctx, urlCommandKey, cmd)
}

// RequestIDFromContext retrieves the request ID from the context.
func RequestIDFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(requestIDKey).(string)

	return v, ok
}

// AuthNameFromContext retrieves the authorization name from the context.
func AuthNameFromContext(ctx context.Context) (string, bool) {
	v, ok := ctx.Value(authNameKey).(string)

	return v, ok
}

// URLCommandFromContext retrieves the URL command from the context.
func URLCommandFromContext(ctx context.Context) (*config.URLCommand, bool) {
	v, ok := ctx.Value(urlCommandKey).(*config.URLCommand)

	return v, ok
}
