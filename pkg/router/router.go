// Package router provides HTTP router for running commands.
package router

import (
	"log"
	"net/http"

	"github.com/dkarczmarski/webcmd/pkg/callgate"
	"github.com/dkarczmarski/webcmd/pkg/cmdrunner"
	"github.com/dkarczmarski/webcmd/pkg/config"
	"github.com/dkarczmarski/webcmd/pkg/httpx"
	"github.com/dkarczmarski/webcmd/pkg/router/handlers"
)

// New creates and initializes a new http.ServeMux instance with the given configuration.
func New(configuration *config.Config) *http.ServeMux {
	registry := callgate.NewRegistry(callgate.WithDefaults())
	mux := http.NewServeMux()
	mux.Handle("/", httpx.ToHandler(
		httpx.ErrorSink(log.Default(), configuration.Server.WithErrorHeader),
		httpx.WithMiddleware(
			httpx.Chain(
				handlers.RequestIDMiddleware(),
				handlers.APIKeyMiddleware(configuration),
				handlers.URLCommandMiddleware(configuration),
				handlers.AuthorizationMiddleware(),
				handlers.TimeoutMiddleware(),
			),
			handlers.ExecutionHandler(&cmdrunner.RealRunner{}, registry),
		)))

	return mux
}
