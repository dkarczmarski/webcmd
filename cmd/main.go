package main

import (
	"log"

	"github.com/dkarczmarski/webcmd/pkg/config"
	"github.com/dkarczmarski/webcmd/pkg/server"
)

func main() {
	cfg, err := config.LoadConfigFromFile("test-config.yaml")
	if err != nil {
		log.Fatalf("Error loading config: %v", err)
	}

	srv := server.New(cfg, server.WithAddr(cfg.Server.Address))

	log.Printf("Starting server")

	if err := srv.Start(); err != nil {
		log.Fatalf("Error starting server: %v", err)
	}
}
