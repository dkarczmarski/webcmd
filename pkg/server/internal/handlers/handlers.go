package handlers

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/dkarczmarski/webcmd/pkg/config"
)

type CommandResult struct {
	ExitCode int
	Output   string
}

type CommandExecutor interface {
	RunCommand(ctx context.Context, cmd *config.URLCommand, params map[string]interface{}) CommandResult
}

func HandleURLCommand(
	responseWriter http.ResponseWriter,
	request *http.Request,
	configuration *config.Config,
	executor CommandExecutor,
) {
	queryParams := extractQueryParams(request)
	requestURL := fmt.Sprintf("%s %s", request.Method, request.URL.Path)

	var foundCommand *config.URLCommand

	if configuration != nil {
		for _, cmd := range configuration.URLCommands {
			if strings.TrimSpace(cmd.URL) == requestURL {
				cmdCopy := cmd
				foundCommand = &cmdCopy

				break
			}
		}
	}

	if foundCommand == nil {
		http.NotFound(responseWriter, request)

		return
	}

	params := map[string]interface{}{
		"url": queryParams,
	}
	runResult := executor.RunCommand(request.Context(), foundCommand, params)

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
