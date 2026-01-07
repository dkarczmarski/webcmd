// Package server provides HTTP server for running commands.
package server

//go:generate mockgen -typed -destination=internal/mocks/mock_server.go -package=mocks github.com/dkarczmarski/webcmd/pkg/server/internal/handlers CommandExecutor

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/dkarczmarski/webcmd/pkg/cmdbuilder"
	"github.com/dkarczmarski/webcmd/pkg/cmdrunner"
	"github.com/dkarczmarski/webcmd/pkg/config"
	"github.com/dkarczmarski/webcmd/pkg/server/internal/handlers"
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

// RunCommand builds and executes a command based on the provided configuration and parameters.
func (e *defaultExecutor) RunCommand(
	ctx context.Context,
	commandConfig *config.CommandConfig,
	params map[string]interface{},
) handlers.CommandResult {
	cmdResult, err := cmdbuilder.BuildCommand(commandConfig.CommandTemplate, params)
	if err != nil {
		log.Printf("BuildCommand error: %v", err)

		return handlers.CommandResult{
			ExitCode: -1,
			Output:   fmt.Sprintf("Error building command: %v", err),
		}
	}

	log.Printf("Executing command: %s %v", cmdResult.Command, cmdResult.Arguments)

	res := cmdrunner.RunCommand(ctx, cmdResult.Command, cmdResult.Arguments)

	log.Printf("Command execution result: %+v", res)

	return handlers.CommandResult{
		ExitCode: res.ExitCode,
		Output:   res.Output,
	}
}

const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 10 * time.Second
	writeTimeout      = 10 * time.Second
	idleTimeout       = 120 * time.Second
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
	urlCommandHandler := func(responseWriter http.ResponseWriter, request *http.Request) {
		handlers.URLCommandHandler(responseWriter, request, options.Executor)
	}
	mux.HandleFunc("/", handlers.AuthAndRouteMiddleware(urlCommandHandler, configuration))

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
