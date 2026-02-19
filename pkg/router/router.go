// Package router provides HTTP router for running commands.
package router

import (
	"log"
	"net/http"

	"github.com/dkarczmarski/webcmd/pkg/cmdrunner"
	"github.com/dkarczmarski/webcmd/pkg/config"
	"github.com/dkarczmarski/webcmd/pkg/httpx"
	"github.com/dkarczmarski/webcmd/pkg/router/handlers"
)

// New creates and initializes a new http.ServeMux instance with the given configuration.
func New(configuration *config.Config) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("/", httpx.ToHandler(
		httpx.ErrorSink(log.Default()),
		httpx.WithMiddleware(
			httpx.Chain(
				handlers.RequestIDMiddleware(),
				handlers.APIKeyMiddleware(configuration),
				handlers.URLCommandMiddleware(configuration),
				handlers.AuthorizationMiddleware(),
				handlers.TimeoutMiddleware(),
			),
			handlers.ExecutionHandler(&cmdrunner.RealRunner{}),
		)))

	return mux
}
