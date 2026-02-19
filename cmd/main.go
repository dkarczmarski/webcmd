package main

import (
	"context"
	"flag"
	"log"
	"os/signal"
	"syscall"

	"github.com/dkarczmarski/webcmd/pkg/config"
	"github.com/dkarczmarski/webcmd/pkg/server"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to the configuration file")
	flag.Parse()

	configuration, err := config.LoadConfigFromFile(*configPath)
	if err != nil {
		log.Fatalf("[ERROR] Error loading config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	srv := server.New(configuration)
	if err := srv.Run(ctx); err != nil {
		log.Printf("[ERROR] Error running application: %v", err)

		return
	}

	log.Println("[INFO] Server stopped")
}
