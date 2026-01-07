package main

import (
	"flag"
	"log"

	"github.com/dkarczmarski/webcmd/pkg/config"
	"github.com/dkarczmarski/webcmd/pkg/server"
)

func main() {
	configPath := flag.String("config", "config.yaml", "path to the configuration file")
	flag.Parse()

	configuration, err := config.LoadConfigFromFile(*configPath)
	if err != nil {
		log.Fatalf("Error loading config: %v", err)
	}

	srv := server.New(configuration, server.WithAddr(configuration.Server.Address))

	log.Printf("Starting server")

	if err := srv.Start(); err != nil {
		log.Fatalf("Error starting server: %v", err)
	}
}
