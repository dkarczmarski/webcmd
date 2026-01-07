package handlers

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/dkarczmarski/webcmd/pkg/config"
)

type contextKey string

// CommandConfigKey is the context key used to store and retrieve the command configuration.
const CommandConfigKey contextKey = "commandConfigKey"

// AuthAndRouteMiddleware handles both user authorization and routing of requests to the appropriate command.
func AuthAndRouteMiddleware(next http.HandlerFunc, configuration *config.Config) http.HandlerFunc {
	return func(responseWriter http.ResponseWriter, request *http.Request) {
		authName := authorize(request, configuration)
		foundURLCommand, found := findURLCommand(request, configuration)

		if !found {
			log.Printf("Not Found: %s %s", request.Method, request.URL.Path)
			http.NotFound(responseWriter, request)

			return
		}

		if !isAuthorized(foundURLCommand, authName) {
			log.Printf("Forbidden: %s %s (User: %s)", request.Method, request.URL.Path, authName)
			responseWriter.WriteHeader(http.StatusForbidden)

			return
		}

		ctx := context.WithValue(request.Context(), CommandConfigKey, &foundURLCommand.CommandConfig)

		next(responseWriter, request.WithContext(ctx))
	}
}

func authorize(request *http.Request, configuration *config.Config) string {
	apiKey := request.Header.Get("X-Api-Key")

	if apiKey != "" {
		for _, auth := range configuration.Authorization {
			if auth.Key == apiKey {
				return auth.Name
			}
		}
	}

	return ""
}

func findURLCommand(request *http.Request, configuration *config.Config) (*config.URLCommand, bool) {
	requestURL := fmt.Sprintf("%s %s", request.Method, request.URL.Path)

	for _, cmd := range configuration.URLCommands {
		if strings.TrimSpace(cmd.URL) == requestURL {
			cmdCopy := cmd

			return &cmdCopy, true
		}
	}

	return nil, false
}

func isAuthorized(foundURLCommand *config.URLCommand, authName string) bool {
	if foundURLCommand.AuthorizationName == "" {
		return true
	}

	if authName == "" {
		return false
	}

	allowedNames := strings.Split(foundURLCommand.AuthorizationName, ",")

	for _, name := range allowedNames {
		if strings.TrimSpace(name) == authName {
			return true
		}
	}

	return false
}

// CommandResult defines the outcome of a command execution, including an exit code and output string.
type CommandResult struct {
	ExitCode int
	Output   string
}

// CommandExecutor is an interface for types that can build and run system commands.
type CommandExecutor interface {
	RunCommand(ctx context.Context, cmd *config.CommandConfig, params map[string]interface{}) CommandResult
}

// URLCommandHandler handles requests by extracting parameters and executing the associated command.
func URLCommandHandler(
	responseWriter http.ResponseWriter,
	request *http.Request,
	executor CommandExecutor,
) {
	commandConfig, ok := request.Context().Value(CommandConfigKey).(*config.CommandConfig)

	if !ok || commandConfig == nil {
		log.Printf("Internal Server Error: command configuration missing in context")
		responseWriter.WriteHeader(http.StatusInternalServerError)

		if _, err := fmt.Fprintf(responseWriter, "Internal Server Error: command configuration missing"); err != nil {
			log.Printf("Failed to write response: %v", err)
		}

		return
	}

	queryParams := extractQueryParams(request)
	params := map[string]interface{}{
		"url": queryParams,
	}

	log.Printf("Executing command for: %s %s", request.Method, request.URL.Path)
	runResult := executor.RunCommand(request.Context(), commandConfig, params)

	if runResult.ExitCode != 0 {
		log.Printf("Command execution failed (Exit Code: %d): %s", runResult.ExitCode, runResult.Output)
		responseWriter.WriteHeader(http.StatusInternalServerError)

		if _, err := fmt.Fprintf(responseWriter, "Command failed with exit code %d\nOutput: %s",
			runResult.ExitCode, runResult.Output); err != nil {
			log.Printf("Failed to write response: %v", err)
		}

		return
	}

	log.Printf("Command execution successful")

	if _, err := fmt.Fprint(responseWriter, runResult.Output); err != nil {
		log.Printf("Failed to write response: %v", err)
	}
}

func extractQueryParams(request *http.Request) map[string]string {
	params := make(map[string]string)
	query := request.URL.Query()

	for key, values := range query {
		if len(values) > 0 {
			params[key] = values[0]
		}
	}

	return params
}
