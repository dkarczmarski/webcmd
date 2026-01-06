package main

import (
	"context"
	"log"

	"github.com/dkarczmarski/webcmd/pkg/cmdrunner"
)

func main() {
	ctx := context.Background()
	command := "echo"
	args := []string{"AAA", "123"}
	timeout := 5

	result := cmdrunner.RunCommand(ctx, command, args, timeout)

	log.Printf("Exit Code: %d\n", result.ExitCode)
	log.Printf("Output: %s\n", result.Output)
}
