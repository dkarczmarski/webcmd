// Package server provides HTTP server for running commands.
package server

//go:generate mockgen -typed -source=server.go -destination=internal/mocks/mock_server.go -package=mocks

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

type Server struct {
	httpServer *http.Server
}

type Options struct {
	Addr     string
	Executor handlers.CommandExecutor
}

type defaultExecutor struct{}

func (e *defaultExecutor) RunCommand(
	ctx context.Context,
	cmd *config.URLCommand,
	params map[string]interface{},
) handlers.CommandResult {
	cmdResult, err := cmdbuilder.BuildCommand(cmd.CommandTemplate, params)
	if err != nil {
		log.Printf("BuildCommand error: %v", err)

		return handlers.CommandResult{
			ExitCode: -1,
			Output:   fmt.Sprintf("Error building command: %v", err),
		}
	}

	log.Printf("BuildCommand result: %+v", cmdResult)

	res := cmdrunner.RunCommand(ctx, cmdResult.Command, cmdResult.Arguments, cmd.Timeout)

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

func New(configuration *config.Config, opts ...func(*Options)) *Server {
	options := Options{
		Addr:     "127.0.0.1:8080",
		Executor: &defaultExecutor{},
	}

	for _, opt := range opts {
		opt(&options)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(responseWriter http.ResponseWriter, request *http.Request) {
		handlers.HandleURLCommand(responseWriter, request, configuration, options.Executor)
	})

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
		httpServer: httpServer,
	}
}

func (s *Server) ServeHTTP(responseWriter http.ResponseWriter, request *http.Request) {
	s.httpServer.Handler.ServeHTTP(responseWriter, request)
}

func (s *Server) Start() error {
	if err := s.httpServer.ListenAndServe(); err != nil {
		return fmt.Errorf("listen and serve: %w", err)
	}

	return nil
}
