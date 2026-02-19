package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/dkarczmarski/webcmd/pkg/config"
	"github.com/dkarczmarski/webcmd/pkg/gracehttp"
	"github.com/dkarczmarski/webcmd/pkg/router"
)

const (
	readHeaderTimeout = 5 * time.Second
	readTimeout       = 10 * time.Second
	writeTimeout      = 0 // No timeout for streaming
	idleTimeout       = 0 // No timeout for streaming
)

type Server struct {
	cfg    *config.Config
	router http.Handler
}

func New(cfg *config.Config) *Server {
	return &Server{
		cfg:    cfg,
		router: router.New(cfg),
	}
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.router.ServeHTTP(w, r)
}

//nolint:contextcheck
func (s *Server) Run(ctx context.Context) error {
	srv, err := gracehttp.New(
		gracehttp.WithAddr(s.cfg.Server.Address),
		gracehttp.WithHandler(s.router),
		gracehttp.WithBaseContext(ctx),
		gracehttp.WithHTTPServer(func(s *http.Server) {
			s.ReadHeaderTimeout = readHeaderTimeout
			s.ReadTimeout = readTimeout
			s.WriteTimeout = writeTimeout
			s.IdleTimeout = idleTimeout
		}),
	)
	if err != nil {
		return fmt.Errorf("create gracehttp server: %w", err)
	}

	log.Printf("[INFO] Starting server on %s", s.cfg.Server.Address)

	if err := srv.Run(); err != nil {
		return fmt.Errorf("run server: %w", err)
	}

	return nil
}
