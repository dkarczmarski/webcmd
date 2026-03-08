package server

import (
	"log"
	"net/http"

	"github.com/dkarczmarski/webcmd/pkg/callgate"
	"github.com/dkarczmarski/webcmd/pkg/cmdrunner"
	"github.com/dkarczmarski/webcmd/pkg/config"
	"github.com/dkarczmarski/webcmd/pkg/gateexec"
	"github.com/dkarczmarski/webcmd/pkg/httpx"
	"github.com/dkarczmarski/webcmd/pkg/processrunner"
)

// NewRouter creates and initializes a new http.ServeMux instance with the given configuration.
func NewRouter(configuration *config.Config) *http.ServeMux {
	processRunner := processrunner.New(&cmdrunner.RealRunner{})
	registry := callgate.NewRegistry(callgate.WithDefaults())
	exec := gateexec.New(registry)
	mux := http.NewServeMux()

	mux.Handle("/", httpx.ToHandler(
		httpx.ErrorSink(log.Default(), configuration.Server.WithErrorHeader),
		httpx.WithMiddleware(
			httpx.Chain(
				RequestIDMiddleware(),
				APIKeyMiddleware(configuration),
				URLCommandMiddleware(configuration),
				AuthorizationMiddleware(),
				TimeoutMiddleware(),
			),
			ExecutionHandler(processRunner, exec),
		)))

	return mux
}
