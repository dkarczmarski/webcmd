package gracehttp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	sigChanCapacity = 2
)

const (
	stateNew      uint32 = 0
	stateRunning  uint32 = 1
	stateShutdown uint32 = 2
)

var (
	ErrListen   = errors.New("listen")
	ErrShutdown = errors.New("shutdown")
)

type Server struct {
	httpServer *http.Server
	ln         net.Listener

	appCtx    context.Context //nolint:containedctx
	appCancel context.CancelFunc

	sigCh <-chan os.Signal

	shutdownOnce sync.Once
	shutdownCh   chan struct{}

	grace time.Duration
	state atomic.Uint32
}

func New(opts ...Option) (*Server, error) {
	cfg := defaultConfig()

	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	appCtx, appCancel := context.WithCancel(cfg.baseCtx)

	//nolint: exhaustruct
	srv := &Server{
		ln:         cfg.listener,
		appCtx:     appCtx,
		appCancel:  appCancel,
		sigCh:      cfg.signalCh,
		shutdownCh: make(chan struct{}),
		grace:      cfg.grace,
	}

	handler := cfg.handler
	if cfg.rejectNew {
		handler = RejectOnShutdown(srv.shutdownCh, handler)
	}

	//nolint: exhaustruct
	httpSrv := &http.Server{
		Addr:              cfg.addr,
		Handler:           handler,
		ReadHeaderTimeout: cfg.readHeaderTimeout,
		ReadTimeout:       cfg.readTimeout,
		WriteTimeout:      cfg.writeTimeout,
		IdleTimeout:       cfg.idleTimeout,

		BaseContext: func(net.Listener) context.Context { return appCtx },
	}

	for _, mod := range cfg.serverMod {
		mod(httpSrv)
	}

	if err := validateHTTPServer(httpSrv); err != nil {
		return nil, err
	}

	srv.httpServer = httpSrv

	return srv, nil
}

func validateHTTPServer(hs *http.Server) error {
	if hs.Handler == nil {
		return fmt.Errorf("%w: http.Server.Handler is nil", ErrConfig)
	}

	if hs.BaseContext == nil {
		return fmt.Errorf("%w: http.Server.BaseContext is nil", ErrConfig)
	}

	return nil
}

// Addr returns the server's listening address.
func (s *Server) Addr() string {
	if s.ln == nil {
		return s.httpServer.Addr
	}

	return s.ln.Addr().String()
}

// AppContext returns the root application context, which is canceled when shutdown is initiated.
func (s *Server) AppContext() context.Context { return s.appCtx }

// ShutdownRequested returns a channel that is closed when server shutdown is initiated.
func (s *Server) ShutdownRequested() <-chan struct{} { return s.shutdownCh }

// ServeHTTP implements the http.Handler interface by delegating to the underlying HTTP server.
func (s *Server) ServeHTTP(responseWriter http.ResponseWriter, request *http.Request) {
	s.httpServer.Handler.ServeHTTP(responseWriter, request)
}

// Run starts serving HTTP requests and blocks until the server is shut down or an unrecoverable error occurs.
// If Run creates its own signal channel, the first termination signal triggers a graceful shutdown and the
// second signal forces an immediate close.
func (s *Server) Run() error {
	if err := s.checkState(); err != nil {
		return err
	}

	if err := s.ensureListener(); err != nil {
		return err
	}

	sigCh, stopSig := s.setupSignals()
	defer stopSig()

	// Check if shutdown was requested during setup
	if s.state.Load() == stateShutdown {
		return ErrAlreadyShutdown
	}

	errCh := make(chan error, 1)

	go func() {
		errCh <- s.httpServer.Serve(s.ln)
	}()

	return s.waitForExit(sigCh, errCh)
}

func (s *Server) checkState() error {
	if !s.state.CompareAndSwap(stateNew, stateRunning) {
		if s.state.Load() == stateShutdown {
			return ErrAlreadyShutdown
		}

		return ErrAlreadyRunning
	}

	return nil
}

func (s *Server) ensureListener() error {
	if s.ln != nil {
		return nil
	}

	addr := s.httpServer.Addr
	if addr == "" {
		return fmt.Errorf("%w: http server address is not set", ErrConfig)
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrListen, err)
	}

	s.ln = ln

	return nil
}

func (s *Server) setupSignals() (<-chan os.Signal, func()) {
	if s.sigCh != nil {
		return s.sigCh, func() {}
	}

	ch := make(chan os.Signal, sigChanCapacity)
	signal.Notify(ch, os.Interrupt, syscall.SIGTERM, syscall.SIGQUIT)

	return ch, func() {
		signal.Stop(ch)
		close(ch)
	}
}

func (s *Server) waitForExit(sigCh <-chan os.Signal, errCh <-chan error) error {
	select {
	case <-sigCh:
		// 1st signal: initiate graceful shutdown
		s.requestShutdown()

		// 2nd signal: force close (hard shutdown)
		go func() {
			<-sigCh

			_ = s.httpServer.Close()

			if s.ln != nil {
				_ = s.ln.Close()
			}
		}()

		return s.shutdown(s.grace)
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}

		return nil
	}
}

// Shutdown gracefully shuts down the server, allowing in-flight requests to complete within
// the configured grace period.
func (s *Server) Shutdown() error {
	if s.state.CompareAndSwap(stateRunning, stateShutdown) {
		s.requestShutdown()

		return s.shutdown(s.grace)
	}

	if s.state.Load() == stateShutdown {
		return ErrAlreadyShutdown
	}

	if s.state.CompareAndSwap(stateNew, stateShutdown) {
		s.requestShutdown()

		return s.shutdown(s.grace)
	}

	return ErrAlreadyShutdown
}

func (s *Server) requestShutdown() {
	s.shutdownOnce.Do(func() {
		close(s.shutdownCh)
		s.appCancel()
	})
}

func (s *Server) shutdown(grace time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()

	if err := s.httpServer.Shutdown(ctx); err != nil {
		_ = s.httpServer.Close()

		return fmt.Errorf("%w: %w", ErrShutdown, err)
	}

	return nil
}
