package gracehttp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

const (
	GracePeriod       = 5 * time.Second
	ReadHeaderTimeout = 10 * time.Second
	ReadTimeout       = 60 * time.Second
	WriteTimeout      = 60 * time.Second
	IdleTimeout       = 120 * time.Second
)

var (
	ErrConfig          = errors.New("configuration error")
	ErrAlreadyRunning  = errors.New("server already running")
	ErrAlreadyShutdown = errors.New("server already shutdown")
)

type ServerModifier func(*http.Server)

type Option func(*config)

type config struct {
	addr              string
	handler           http.Handler
	listener          net.Listener
	signalCh          <-chan os.Signal
	grace             time.Duration
	baseCtx           context.Context //nolint:containedctx
	rejectNew         bool
	serverMod         []ServerModifier
	readHeaderTimeout time.Duration
	readTimeout       time.Duration
	writeTimeout      time.Duration
	idleTimeout       time.Duration
}

func defaultConfig() config {
	return config{
		addr:              "",
		handler:           nil,
		listener:          nil,
		signalCh:          nil,
		grace:             GracePeriod,
		baseCtx:           context.Background(),
		rejectNew:         false,
		serverMod:         nil,
		readHeaderTimeout: ReadHeaderTimeout,
		readTimeout:       ReadTimeout,
		writeTimeout:      WriteTimeout,
		idleTimeout:       IdleTimeout,
	}
}

func (c config) validate() error {
	if c.grace <= 0 {
		return fmt.Errorf("%w: grace must be > 0", ErrConfig)
	}

	if c.baseCtx == nil {
		return fmt.Errorf("%w: base context must not be nil", ErrConfig)
	}

	return nil
}

func WithAddr(addr string) Option {
	return func(c *config) { c.addr = addr }
}

func WithHandler(h http.Handler) Option {
	return func(c *config) { c.handler = h }
}

func WithListener(ln net.Listener) Option {
	return func(c *config) { c.listener = ln }
}

func WithSignalChan(ch <-chan os.Signal) Option {
	return func(c *config) { c.signalCh = ch }
}

func WithGrace(d time.Duration) Option {
	return func(c *config) { c.grace = d }
}

func WithBaseContext(ctx context.Context) Option {
	return func(c *config) { c.baseCtx = ctx }
}

func RejectNewRequestsOnShutdown() Option {
	return func(c *config) { c.rejectNew = true }
}

func WithHTTPServer(mod ServerModifier) Option {
	return func(c *config) {
		if mod != nil {
			c.serverMod = append(c.serverMod, mod)
		}
	}
}
