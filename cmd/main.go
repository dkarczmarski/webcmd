package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/dkarczmarski/webcmd/pkg/config"
	"github.com/dkarczmarski/webcmd/pkg/server"
)

const shutdownGracePeriod = 10 * time.Second

func runWebServer(ctx context.Context, cfg *config.Config) error {
	srv := server.New(cfg, server.WithAddr(cfg.Server.Address))

	go func() {
		<-ctx.Done()

		log.Println("[INFO] Server is shutting down...")

		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownGracePeriod)

		defer cancel()

		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("[ERROR] Error during server shutdown: %v", err)
		}
	}()

	log.Printf("[INFO] Starting server on %s", cfg.Server.Address)

	if err := srv.Start(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("start server: %w", err)
	}

	return nil
}

func Run(ctx context.Context, cfg *config.Config) error {
	ctx, stop := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	return runWebServer(ctx, cfg)
}

func main() {
	configPath := flag.String("config", "config.yaml", "path to the configuration file")
	flag.Parse()

	configuration, err := config.LoadConfigFromFile(*configPath)
	if err != nil {
		log.Fatalf("[ERROR] Error loading config: %v", err)
	}

	if err := Run(context.Background(), configuration); err != nil {
		log.Fatalf("[ERROR] Error running application: %v", err)
	}

	log.Println("[INFO] Server stopped")
}
