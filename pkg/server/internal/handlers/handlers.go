package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/dkarczmarski/webcmd/pkg/config"
)

type contextKey string

const CommandConfigKey contextKey = "commandConfigKey"

func AuthAndRouteMiddleware(next http.HandlerFunc, configuration *config.Config) http.HandlerFunc {
	return func(responseWriter http.ResponseWriter, request *http.Request) {
		authName := authorize(request, configuration)
		foundURLCommand, found := findURLCommand(request, configuration)

		if !found {
			http.NotFound(responseWriter, request)

			return
		}

		if !isAuthorized(foundURLCommand, authName) {
			responseWriter.WriteHeader(http.StatusForbidden)
			_, _ = fmt.Fprintf(responseWriter, "Forbidden: user %s not authorized for this command", authName)

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

type CommandResult struct {
	ExitCode int
	Output   string
}

type CommandExecutor interface {
	RunCommand(ctx context.Context, cmd *config.CommandConfig, params map[string]interface{}) CommandResult
}

func URLCommandHandler(
	responseWriter http.ResponseWriter,
	request *http.Request,
	executor CommandExecutor,
) {
	commandConfig, ok := request.Context().Value(CommandConfigKey).(*config.CommandConfig)

	if !ok || commandConfig == nil {
		responseWriter.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprintf(responseWriter, "Internal Server Error: command configuration missing")

		return
	}

	queryParams := extractQueryParams(request)
	params := map[string]interface{}{
		"url": queryParams,
	}
	runResult := executor.RunCommand(request.Context(), commandConfig, params)

	if runResult.ExitCode != 0 {
		responseWriter.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprintf(responseWriter, "Command failed with exit code %d\nOutput: %s",
			runResult.ExitCode, runResult.Output)

		return
	}

	_, _ = fmt.Fprint(responseWriter, runResult.Output)
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
