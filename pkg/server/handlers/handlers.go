// Package handlers provides HTTP handlers and middleware for the server.
package handlers

//go:generate go run go.uber.org/mock/mockgen -typed -destination=./internal/mocks/mock_cmdrunner.go -package=mocks github.com/dkarczmarski/webcmd/pkg/cmdrunner Runner,Command

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os/exec"
	"strings"
	"syscall"
	"time"

	"github.com/dkarczmarski/webcmd/pkg/cmdbuilder"
	"github.com/dkarczmarski/webcmd/pkg/cmdrunner"
	"github.com/dkarczmarski/webcmd/pkg/config"
	"github.com/dkarczmarski/webcmd/pkg/httpx"
)

var (
	ErrUnauthorized          = errors.New("unauthorized")
	ErrInvalidRequestContext = errors.New("invalid request context")
	ErrBadConfiguration      = errors.New("bad configuration")
)

type contextKey string

// AuthNameKey is the context key used to store and retrieve the authorization name.
const AuthNameKey contextKey = "authName"

// URLCommandKey is the context key used to store and retrieve the URL command.
const URLCommandKey contextKey = "urlCommand"

// APIKeyMiddleware creates a new Middleware that reads X-Api-Key header,
// finds the matching authorization name, and adds it to the request context
// under AuthNameKey.
func APIKeyMiddleware(configuration *config.Config) httpx.Middleware {
	return func(next httpx.WebHandler) httpx.WebHandler {
		return httpx.WebHandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) error {
			apiKey := request.Header.Get("X-Api-Key")

			var authName string

			if apiKey != "" {
				for _, auth := range configuration.Authorization {
					if auth.Key == apiKey {
						authName = auth.Name

						break
					}
				}
			}

			if authName != "" {
				ctx := context.WithValue(request.Context(), AuthNameKey, authName)

				return next.ServeHTTP(responseWriter, request.WithContext(ctx))
			}

			return next.ServeHTTP(responseWriter, request)
		})
	}
}

// URLCommandMiddleware creates a new Middleware that finds the matching URL command
// and adds it to the request context under URLCommandKey.
func URLCommandMiddleware(configuration *config.Config) httpx.Middleware {
	return func(next httpx.WebHandler) httpx.WebHandler {
		return httpx.WebHandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) error {
			requestURL := request.Method + " " + request.URL.Path

			for _, cmd := range configuration.URLCommands {
				if cmd.URL == requestURL {
					ctx := context.WithValue(request.Context(), URLCommandKey, &cmd)

					return next.ServeHTTP(responseWriter, request.WithContext(ctx))
				}
			}

			return next.ServeHTTP(responseWriter, request)
		})
	}
}

// AuthorizationMiddleware creates a new Middleware that checks if the user is authorized
// to execute the command based on the information in the request context.
// It supports multiple authorized names separated by commas in the command configuration.
func AuthorizationMiddleware() httpx.Middleware {
	return func(next httpx.WebHandler) httpx.WebHandler {
		return httpx.WebHandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) error {
			cmd, err := getURLCommandFromContext(request)
			if err != nil {
				return httpx.NewWebError(err, http.StatusNotFound, "Command not found")
			}

			if cmd.AuthorizationName == "" {
				return next.ServeHTTP(responseWriter, request)
			}

			valAuth := request.Context().Value(AuthNameKey)
			authName, _ := valAuth.(string)

			if authName == "" {
				return httpx.NewWebError(
					fmt.Errorf("authentication required for %s: %w", cmd.URL, ErrUnauthorized),
					http.StatusUnauthorized,
					"Authentication required: please provide a valid API key.",
				)
			}

			allowedNames := strings.Split(cmd.AuthorizationName, ",")
			for _, name := range allowedNames {
				if strings.TrimSpace(name) == authName {
					return next.ServeHTTP(responseWriter, request)
				}
			}

			return httpx.NewWebError(
				fmt.Errorf("user '%s' not authorized for %s: %w", authName, cmd.URL, ErrUnauthorized),
				http.StatusForbidden,
				fmt.Sprintf("Access denied: user '%s' does not have permission to execute this command.", authName),
			)
		})
	}
}

// TimeoutMiddleware creates a new Middleware that sets a timeout for the request context
// based on the command configuration.
func TimeoutMiddleware() httpx.Middleware {
	return func(next httpx.WebHandler) httpx.WebHandler {
		return httpx.WebHandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) error {
			cmd, err := getURLCommandFromContext(request)
			if err != nil {
				return httpx.NewWebError(err, http.StatusNotFound, "Command not found")
			}

			if cmd.Timeout != nil {
				ctx, cancel := context.WithTimeout(request.Context(), *cmd.Timeout)
				defer cancel()

				return next.ServeHTTP(responseWriter, request.WithContext(ctx))
			}

			return next.ServeHTTP(responseWriter, request)
		})
	}
}

// ExecutionHandler creates a new WebHandler that executes the command
// associated with the URLCommand found in the request context using the provided runner.
// It handles command building, output preparation, and execution.
// If the command fails, it writes the error message to the response.
//
//nolint:ireturn
func ExecutionHandler(runner cmdrunner.Runner) httpx.WebHandler {
	return httpx.WebHandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) error {
		log.Printf("[INFO] Executing command for: %s %s", request.Method, request.URL.Path)

		cmd, err := getURLCommandFromContext(request)
		if err != nil {
			return httpx.NewWebError(err, http.StatusNotFound, "Command not found")
		}

		params, err := extractParams(request, cmd)
		if err != nil {
			return err
		}

		cmdResult, err := buildCommand(cmd.CommandConfig.CommandTemplate, params)
		if err != nil {
			return err
		}

		writer, async, err := prepareOutput(responseWriter, cmd.CommandConfig.OutputType)
		if err != nil {
			return err
		}

		exitCode, exitErr := executeCommand(
			request.Context(), runner, cmdResult.Command, cmdResult.Arguments, writer, async, cmd.GraceTerminationTimeout,
		)

		if exitCode != 0 || exitErr != nil {
			log.Printf("[WARN] Command failed with exit code: %d, error: %v", exitCode, exitErr)

			errorMessage := fmt.Sprintf("Command failed with exit code: %d, error: %v", exitCode, exitErr)
			if _, err := responseWriter.Write([]byte(errorMessage)); err != nil {
				log.Printf("[ERROR] Failed to write error message: %v", err)
			}
		}

		return nil
	})
}

func prepareOutput(responseWriter http.ResponseWriter, outputType string) (io.Writer, bool, error) {
	var (
		writer io.Writer
		async  bool
	)

	switch outputType {
	case "none":
		writer = io.Discard
		async = true
	case "stream":
		if _, ok := responseWriter.(http.Flusher); !ok {
			return nil, false, fmt.Errorf("streaming not supported: %w", ErrBadConfiguration)
		}

		writer = newFlushResponseWriter(responseWriter)

		responseWriter.Header().Set("Content-Type", "text/plain; charset=utf-8")
		responseWriter.Header().Set("Cache-Control", "no-cache")
		// nginx:
		responseWriter.Header().Set("X-Accel-Buffering", "no")
	case "", "text":
		writer = responseWriter

		responseWriter.Header().Set("Content-Type", "text/plain; charset=utf-8")
	default:
		return nil, false, fmt.Errorf("%w: unknown output type %q", ErrBadConfiguration, outputType)
	}

	return writer, async, nil
}

func extractParams(request *http.Request, cmd *config.URLCommand) (map[string]interface{}, error) {
	queryParams := extractQueryParams(request)
	headers := extractHeaders(request)
	params := map[string]interface{}{
		"url":     queryParams,
		"headers": headers,
	}

	bodyBytes, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, httpx.NewWebError(
			fmt.Errorf("failed to read request body: %w", err),
			http.StatusInternalServerError,
			"",
		)
	}

	setNestedParam(params, "body", "text", string(bodyBytes))

	if config.IsTrue(cmd.CommandConfig.Params.BodyAsJSON) {
		if err := processBodyAsJSON(bodyBytes, params); err != nil {
			return nil, err
		}
	}

	return params, nil
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

func extractHeaders(request *http.Request) map[string]string {
	headers := make(map[string]string)

	for key, values := range request.Header {
		if len(values) > 0 {
			// Normalize keys for Go templates (replace '-' with '_')
			normalizedKey := strings.ReplaceAll(key, "-", "_")
			headers[normalizedKey] = strings.Join(values, "; ")
		}
	}

	return headers
}

type JSONBody map[string]interface{}

func (j JSONBody) String() string {
	b, err := json.Marshal(j)
	if err != nil {
		return fmt.Sprintf("error marshaling json: %v", err)
	}

	return string(b)
}

func processBodyAsJSON(bodyBytes []byte, params map[string]interface{}) error {
	var bodyJSON JSONBody
	if err := json.Unmarshal(bodyBytes, &bodyJSON); err != nil {
		return httpx.NewWebError(
			fmt.Errorf("failed to parse JSON body: %w", err),
			http.StatusBadRequest,
			"",
		)
	}

	setNestedParam(params, "body", "json", bodyJSON)

	return nil
}

func buildCommand(
	template string,
	params map[string]interface{},
) (*cmdbuilder.Result, error) {
	cmdResult, err := cmdbuilder.BuildCommand(template, params)
	if err != nil {
		return nil, fmt.Errorf("error building command: %w", err)
	}

	return &cmdResult, nil
}

func executeCommand(
	ctx context.Context,
	runner cmdrunner.Runner,
	command string,
	arguments []string,
	writer io.Writer,
	async bool,
	graceTerminationTimeout *time.Duration,
) (int, error) {
	log.Printf("[INFO] Executing command: %s %v", command, arguments)

	cmd := runner.Command(command, arguments...)

	//nolint:exhaustruct
	cmd.SetSysProcAttr(&syscall.SysProcAttr{
		Setpgid: true,
	})
	cmd.SetStdout(writer)
	cmd.SetStderr(writer)

	if err := cmd.Start(); err != nil {
		return -1, fmt.Errorf("failed to start command: %w", err)
	}

	if async {
		handleAsyncWait(ctx, runner, cmd, command, graceTerminationTimeout)

		return 0, nil
	}

	return handleSyncWait(ctx, runner, cmd, command, graceTerminationTimeout)
}

func handleSyncWait(
	ctx context.Context,
	runner cmdrunner.Runner,
	cmd cmdrunner.Command,
	command string,
	graceTerminationTimeout *time.Duration,
) (int, error) {
	done := make(chan struct{})

	go func() {
		terminateOnContextDone(ctx, runner, done, cmd, command, graceTerminationTimeout)
	}()

	err := cmd.Wait()

	close(done)

	return determineExitCodeAndError(ctx, cmd, err)
}

func handleAsyncWait(
	ctx context.Context,
	runner cmdrunner.Runner,
	cmd cmdrunner.Command,
	command string,
	graceTerminationTimeout *time.Duration,
) {
	done := make(chan struct{})

	go func() {
		log.Printf("[INFO] Asynchronously waiting for command to finish: %s", command)

		waitErr := cmd.Wait()

		close(done)

		if waitErr != nil {
			log.Printf("[ERROR] Asynchronous command failed: %s, error: %v", command, waitErr)
		} else {
			log.Printf("[INFO] Asynchronous command finished successfully: %s", command)
		}
	}()

	go func() {
		terminateOnContextDone(ctx, runner, done, cmd, command, graceTerminationTimeout)
	}()
}

func terminateOnContextDone(
	ctx context.Context,
	runner cmdrunner.Runner,
	done <-chan struct{},
	cmd cmdrunner.Command,
	command string,
	graceTerminationTimeout *time.Duration,
) {
	select {
	case <-ctx.Done():
		pid := cmd.Pid()

		if graceTerminationTimeout == nil {
			log.Printf(
				"[INFO] Context closed, no grace termination timeout set, sending SIGKILL to process group: %s",
				command,
			)
			signalProcessGroup(runner, pid, syscall.SIGKILL, command)

			return
		}

		log.Printf("[INFO] Context closed, sending SIGTERM to process group: %s", command)
		signalProcessGroup(runner, pid, syscall.SIGTERM, command)

		t := time.NewTimer(*graceTerminationTimeout)
		defer t.Stop()

		select {
		case <-t.C:
			log.Printf("[INFO] Process still running after %v, sending SIGKILL to process group: %s",
				*graceTerminationTimeout, command)
			signalProcessGroup(runner, pid, syscall.SIGKILL, command)
		case <-done:
		}

	case <-done:
	}
}

func signalProcessGroup(runner cmdrunner.Runner, pid int, sig syscall.Signal, command string) {
	if pid <= 0 {
		log.Printf("[WARN] Cannot send %s to process group: PID is %d for command: %s", sig, pid, command)

		return
	}

	if err := runner.Kill(pid, sig); err != nil {
		log.Printf("[ERROR] Failed to send %s to process group %d: %v", sig, -pid, err)
	}
}

func determineExitCodeAndError(ctx context.Context, cmd cmdrunner.Command, err error) (int, error) {
	if err != nil {
		if isTimeoutOrCanceled(ctx) {
			// Timeout or cancellation takes precedence over other errors as this is intentional.
			//nolint:wrapcheck
			return -1, ctx.Err()
		}

		var exitError *exec.ExitError
		if errors.As(err, &exitError) {
			return exitError.ExitCode(), err
		}

		return -1, err
	}

	if cmd.ProcessState() != nil {
		return cmd.ProcessState().ExitCode(), nil
	}

	return 0, nil
}

func isTimeoutOrCanceled(ctx context.Context) bool {
	return ctx.Err() != nil && (errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(ctx.Err(), context.Canceled))
}

func getURLCommandFromContext(request *http.Request) (*config.URLCommand, error) {
	valCmd := request.Context().Value(URLCommandKey)
	if valCmd == nil {
		return nil, fmt.Errorf("URLCommand not found in context: %w", ErrInvalidRequestContext)
	}

	cmd, ok := valCmd.(*config.URLCommand)
	if !ok {
		return nil, fmt.Errorf("URLCommand not found in context: %w", ErrInvalidRequestContext)
	}

	return cmd, nil
}

func setNestedParam(params map[string]interface{}, parentKey, childKey string, value interface{}) {
	if _, ok := params[parentKey]; !ok {
		params[parentKey] = make(map[string]interface{})
	}

	if parentMap, ok := params[parentKey].(map[string]interface{}); ok {
		parentMap[childKey] = value
	}
}
