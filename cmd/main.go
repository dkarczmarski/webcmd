package main

import (
	"context"
	"log"

	"github.com/dkarczmarski/webcmd/pkg/cmdbuilder"
	"github.com/dkarczmarski/webcmd/pkg/cmdrunner"
)

func main() {
	ctx := context.Background()

	template := "echo\n{{.Param1}}\n{{.Param2}}"
	params := map[string]interface{}{
		"Param1": "AAA",
		"Param2": "123",
	}

	buildResult, err := cmdbuilder.BuildCommand(template, params)
	if err != nil {
		log.Fatalf("Error building command: %v", err)
	}

	timeout := 5

	result := cmdrunner.RunCommand(ctx, buildResult.Command, buildResult.Arguments, timeout)

	log.Printf("Exit Code: %d\n", result.ExitCode)
	log.Printf("Output: %s\n", result.Output)
}
