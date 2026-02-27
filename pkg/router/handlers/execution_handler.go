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
	"strings"
	"time"

	"github.com/dkarczmarski/webcmd/pkg/callgate"
	"github.com/dkarczmarski/webcmd/pkg/cmdbuilder"
	"github.com/dkarczmarski/webcmd/pkg/cmdrunner"
	"github.com/dkarczmarski/webcmd/pkg/config"
	"github.com/dkarczmarski/webcmd/pkg/gateexec"
	"github.com/dkarczmarski/webcmd/pkg/httpx"
	"github.com/dkarczmarski/webcmd/pkg/processrunner"
)

var (
	ErrCommandFailed   = errors.New("command failed")
	ErrCommandNotFound = errors.New("command not found")
	ErrInvalidJSONBody = errors.New("invalid JSON body")
)

// ExecutionHandler returns a WebHandler that executes the command associated with the URLCommand stored in the
// request context.
// It builds the command from the configured template and request parameters, prepares the response output,
// and runs the command using the provided runner.
// If CallGate is configured for the URLCommand, it uses the shared registry to obtain a gate for the group and
// applies the selected gate mode (e.g. single/sequence) to limit concurrent executions.
// The handler supports output modes: "text" (default), "stream" (flushes as data arrives), and "none" (async,
// discarding output).
// When execution fails (non-zero exit code or error), it logs the failure and writes an error message to the
// response body.
func ExecutionHandler(runner cmdrunner.Runner, registry *callgate.Registry) httpx.WebHandler { //nolint:ireturn
	return httpx.WebHandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) error {
		return translateError(executionHandler(responseWriter, request, runner, registry))
	})
}

func executionHandler(
	responseWriter http.ResponseWriter,
	request *http.Request,
	runner cmdrunner.Runner,
	registry *callgate.Registry,
) error {
	rid := requestIDFromContext(request.Context())
	log.Printf("[INFO] rid=%s Executing command for: %s %s", rid, request.Method, request.URL.Path)

	cmd, err := getURLCommandFromContext(request)
	if err != nil {
		return err
	}

	params, err := extractParams(request, cmd)
	if err != nil {
		return err
	}

	cmdResult, err := buildCommand(cmd.CommandConfig.CommandTemplate, params)
	if err != nil {
		return err
	}

	return prepareOutputAndRunCommand(
		request.Context(),
		runner,
		registry,
		cmd,
		cmdResult,
		responseWriter,
	)
}

func translateError(err error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, callgate.ErrBusy) {
		return httpx.NewWebError(err, http.StatusTooManyRequests, "Too many requests")
	}

	if errors.Is(err, gateexec.ErrRegistry) {
		return httpx.NewWebError(err, http.StatusInternalServerError, "Invalid callgate configuration")
	}

	if errors.Is(err, ErrCommandNotFound) {
		return httpx.NewWebError(err, http.StatusNotFound, "Command not found")
	}

	if errors.Is(err, ErrInvalidJSONBody) {
		return httpx.NewWebError(err, http.StatusBadRequest, "must be a JSON object")
	}

	return err
}

func runCommand(
	ctx context.Context,
	runner cmdrunner.Runner,
	registry *callgate.Registry,
	cmd *config.URLCommand,
	cmdResult *cmdbuilder.Result,
	writer io.Writer,
	async bool,
	responseWriter http.ResponseWriter,
) error {
	rid := requestIDFromContext(ctx)
	exec := gateexec.New(registry)
	action := createGateAction(runner, cmd, cmdResult, writer, async)

	exitCode, err := exec.Run(ctx, cmd.CallGate, cmd.URL, action)

	return handleCommandResult(rid, exitCode, err, responseWriter)
}

func createGateAction(
	runner cmdrunner.Runner,
	cmd *config.URLCommand,
	cmdResult *cmdbuilder.Result,
	writer io.Writer,
	async bool,
) gateexec.Action {
	return func(ctx context.Context) (int, <-chan struct{}, error) {
		command := cmdResult.Command
		arguments := cmdResult.Arguments

		rid := requestIDFromContext(ctx)
		log.Printf("[INFO] rid=%s Executing command: %s %v", rid, command, arguments)

		proc, err := startCommandProcess(runner, command, arguments, writer, cmd.GraceTerminationTimeout)
		if err != nil {
			return -1, nil, err
		}

		if async {
			asyncCtx := context.WithoutCancel(ctx)

			var cancel context.CancelFunc = func() {}

			if cmd.CommandConfig.Timeout != nil {
				asyncCtx, cancel = context.WithTimeout(asyncCtx, *cmd.CommandConfig.Timeout)
			}

			return 0, waitAsyncAndLog(asyncCtx, proc, cancel), nil
		}

		exitCode, err := proc.WaitSync(ctx)
		if err != nil {
			return exitCode, nil, fmt.Errorf("process wait failed: %w", err)
		}

		return exitCode, nil, nil
	}
}

func waitAsyncAndLog(
	ctx context.Context,
	proc *processrunner.Process,
	cancel context.CancelFunc,
) <-chan struct{} {
	resCh := proc.WaitAsync(ctx)

	done := make(chan struct{})

	go func() {
		defer close(done)
		defer cancel()

		rid := requestIDFromContext(ctx)

		result := <-resCh
		if result.Err != nil {
			log.Printf("[ERROR] rid=%s Asynchronous command failed (exit code: %d), error: %v",
				rid, result.ExitCode, result.Err)

			return
		}

		log.Printf("[INFO] rid=%s Asynchronous command finished successfully (exit code: %d)",
			rid, result.ExitCode)
	}()

	return done
}

func handleCommandResult(rid string, exitCode int, err error, responseWriter http.ResponseWriter) error {
	if err != nil {
		if errors.Is(err, callgate.ErrBusy) || errors.Is(err, gateexec.ErrRegistry) || errors.Is(err, gateexec.ErrAcquire) {
			return translateError(err)
		}

		log.Printf("[WARN] rid=%s Command failed with exit code: %d, error: %v", rid, exitCode, err)

		writeErrorMessage(rid, responseWriter, fmt.Sprintf("Command failed with exit code: %d, error: %v", exitCode, err))

		return nil
	}

	if exitCode != 0 {
		log.Printf("[WARN] rid=%s Command failed with exit code: %d", rid, exitCode)

		writeErrorMessage(rid, responseWriter, fmt.Sprintf("Command failed with exit code: %d", exitCode))
	}

	return nil
}

func writeErrorMessage(rid string, responseWriter http.ResponseWriter, message string) {
	if _, writeErr := responseWriter.Write([]byte(message)); writeErr != nil {
		log.Printf("[ERROR] rid=%s Failed to write error message: %v", rid, writeErr)
	}
}

func startCommandProcess(
	runner cmdrunner.Runner,
	command string,
	arguments []string,
	writer io.Writer,
	graceTerminationTimeout *time.Duration,
) (*processrunner.Process, error) {
	proc, err := processrunner.StartProcess(runner, command, arguments, writer, graceTerminationTimeout)
	if err != nil {
		return nil, fmt.Errorf("failed to start process: %w", err)
	}

	return proc, nil
}

func getURLCommandFromContext(request *http.Request) (*config.URLCommand, error) {
	valCmd := request.Context().Value(URLCommandKey)
	if valCmd == nil {
		return nil, fmt.Errorf("URLCommand not found in context: %w", ErrCommandNotFound)
	}

	cmd, ok := valCmd.(*config.URLCommand)
	if !ok {
		return nil, fmt.Errorf("URLCommand not found in context: %w", ErrInvalidRequestContext)
	}

	return cmd, nil
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
		return nil, fmt.Errorf("failed to read request body: %w", err)
	}

	setNestedParam(params, "body", "text", string(bodyBytes))

	if config.IsTrue(cmd.CommandConfig.Params.BodyAsJSON) {
		if err := processBodyAsJSON(bodyBytes, params); err != nil {
			return nil, err
		}
	}

	return params, nil
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

func prepareOutputAndRunCommand(
	ctx context.Context,
	runner cmdrunner.Runner,
	registry *callgate.Registry,
	cmd *config.URLCommand,
	cmdResult *cmdbuilder.Result,
	responseWriter http.ResponseWriter,
) error {
	outputType := cmd.CommandConfig.OutputType

	switch outputType {
	case "none":
		return prepareOutputAndRunAsyncCommand(ctx, runner, registry, cmd, cmdResult, responseWriter)
	case "stream":
		return prepareOutputAndRunStreamCommand(ctx, runner, registry, cmd, cmdResult, responseWriter)
	case "", "text":
		return prepareOutputAndRunSyncCommand(ctx, runner, registry, cmd, cmdResult, responseWriter)
	default:
		return fmt.Errorf("%w: unknown output type %q", ErrBadConfiguration, outputType)
	}
}

func prepareOutputAndRunAsyncCommand(
	ctx context.Context,
	runner cmdrunner.Runner,
	registry *callgate.Registry,
	cmd *config.URLCommand,
	cmdResult *cmdbuilder.Result,
	responseWriter http.ResponseWriter,
) error {
	return runCommand(
		ctx,
		runner,
		registry,
		cmd,
		cmdResult,
		io.Discard,
		true,
		responseWriter,
	)
}

func prepareOutputAndRunStreamCommand(
	ctx context.Context,
	runner cmdrunner.Runner,
	registry *callgate.Registry,
	cmd *config.URLCommand,
	cmdResult *cmdbuilder.Result,
	responseWriter http.ResponseWriter,
) error {
	if _, ok := responseWriter.(http.Flusher); !ok {
		return httpx.NewWebError(
			fmt.Errorf("streaming not supported: %w", ErrBadConfiguration),
			http.StatusInternalServerError,
			"response writer does not support flushing",
		)
	}

	writer := newFlushResponseWriter(responseWriter)

	responseWriter.Header().Set("Content-Type", "text/plain; charset=utf-8")
	responseWriter.Header().Set("Cache-Control", "no-cache")
	// nginx:
	responseWriter.Header().Set("X-Accel-Buffering", "no")

	return runCommand(
		ctx,
		runner,
		registry,
		cmd,
		cmdResult,
		writer,
		false,
		responseWriter,
	)
}

func prepareOutputAndRunSyncCommand(
	ctx context.Context,
	runner cmdrunner.Runner,
	registry *callgate.Registry,
	cmd *config.URLCommand,
	cmdResult *cmdbuilder.Result,
	responseWriter http.ResponseWriter,
) error {
	writer := responseWriter

	responseWriter.Header().Set("Content-Type", "text/plain; charset=utf-8")

	return runCommand(
		ctx,
		runner,
		registry,
		cmd,
		cmdResult,
		writer,
		false,
		responseWriter,
	)
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
		return fmt.Errorf("%w: failed to parse JSON body: %w", ErrInvalidJSONBody, err)
	}

	setNestedParam(params, "body", "json", bodyJSON)

	return nil
}

func setNestedParam(params map[string]interface{}, parentKey, childKey string, value interface{}) {
	if _, ok := params[parentKey]; !ok {
		params[parentKey] = make(map[string]interface{})
	}

	if parentMap, ok := params[parentKey].(map[string]interface{}); ok {
		parentMap[childKey] = value
	}
}
