// Package server provides HTTP server for running commands.
package server

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"github.com/dkarczmarski/webcmd/pkg/cmdrunner"
	"github.com/dkarczmarski/webcmd/pkg/config"
	"github.com/dkarczmarski/webcmd/pkg/httpx"
	"github.com/dkarczmarski/webcmd/pkg/server/handlers"
)

// Server represents the HTTP server instance.
type Server struct {
	httpServer    *http.Server
	configuration *config.Config
}

// Options defines the configuration options for the Server.
type Options struct {
	Addr     string
	Executor handlers.CommandExecutor
}

type defaultExecutor struct{}

// RunCommand executes a command based on the provided command and arguments.
func (e *defaultExecutor) RunCommand(
	ctx context.Context,
	command string,
	arguments []string,
	writer io.Writer,
) (int, error) {
	//nolint:wrapcheck // error is intentionally forwarded as-is to the client
	return cmdrunner.RunCommand(ctx, command, arguments, writer)
}

const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 10 * time.Second
	writeTimeout      = 0 // No timeout for streaming
	idleTimeout       = 0 // No timeout for streaming
)

// WithAddr returns an option function that sets the server address.
func WithAddr(addr string) func(*Options) {
	return func(o *Options) {
		o.Addr = addr
	}
}

// New creates and initializes a new Server instance with the given configuration and options.
func New(configuration *config.Config, opts ...func(*Options)) *Server {
	options := Options{
		Addr:     "127.0.0.1:8080",
		Executor: &defaultExecutor{},
	}

	for _, opt := range opts {
		opt(&options)
	}

	mux := http.NewServeMux()
	mux.Handle("/", httpx.ToHandler(
		httpx.ErrorSink(log.Default()),
		httpx.WithMiddleware(
			httpx.Chain(
				handlers.APIKeyMiddleware(configuration),
				handlers.URLCommandMiddleware(configuration),
				handlers.AuthorizationMiddleware(),
				handlers.TimeoutMiddleware(),
			),
			handlers.ExecutionHandler(options.Executor),
		)))

	//nolint:exhaustruct
	httpServer := &http.Server{
		Addr:              options.Addr,
		Handler:           mux,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	return &Server{
		httpServer:    httpServer,
		configuration: configuration,
	}
}

// ServeHTTP implements the http.Handler interface by delegating to the underlying HTTP server.
func (s *Server) ServeHTTP(responseWriter http.ResponseWriter, request *http.Request) {
	s.httpServer.Handler.ServeHTTP(responseWriter, request)
}

// Start begins listening for and serving HTTP requests.
func (s *Server) Start() error {
	httpsConfig := s.configuration.Server.HTTPSConfig
	if httpsConfig.Enabled {
		certFile := httpsConfig.CertFile
		keyFile := httpsConfig.KeyFile

		if err := s.httpServer.ListenAndServeTLS(certFile, keyFile); err != nil {
			return fmt.Errorf("listen and serve TLS: %w", err)
		}
	} else {
		if err := s.httpServer.ListenAndServe(); err != nil {
			return fmt.Errorf("listen and serve: %w", err)
		}
	}

	return nil
}

// Shutdown gracefully shuts down the server without interrupting any active connections.
func (s *Server) Shutdown(ctx context.Context) error {
	if err := s.httpServer.Shutdown(ctx); err != nil {
		return fmt.Errorf("server shutdown: %w", err)
	}

	return nil
}
