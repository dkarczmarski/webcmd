package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/dkarczmarski/webcmd/pkg/cmdbuilder"
	"github.com/dkarczmarski/webcmd/pkg/config"
	"github.com/dkarczmarski/webcmd/pkg/executor"
	"github.com/dkarczmarski/webcmd/pkg/httpx"
)

var (
	ErrStreamingNotSupported = errors.New("streaming not supported")
	ErrCommandNotFound       = errors.New("command not found")
	ErrInvalidJSONBody       = errors.New("invalid JSON body")
)

// CommandExecutor abstracts command execution logic used by the HTTP handler.
//
// It is responsible for orchestrating the full lifecycle of a command execution,
// including:
//
//   - starting the underlying process,
//   - optionally running the command under a call gate,
//   - handling synchronous or asynchronous execution,
//   - enforcing execution timeouts when configured.
//
// The HTTP layer constructs an ExecuteRequest and delegates the actual execution
// to a CommandExecutor implementation. This keeps the handler independent from
// lower-level details such as process management and gate coordination.
//
// The returned ExecuteResult contains the command exit code (when available)
// and any execution error. Errors that occur before the command starts
// (for example gate acquisition failures) are returned through ExecuteResult.Err.
//
// Implementations typically combine process execution (processrunner)
// with gate-based concurrency control (gateexec).
type CommandExecutor interface {
	Execute(
		ctx context.Context,
		req executor.ExecuteRequest,
	) executor.ExecuteResult
}

// ExecutionHandler returns a WebHandler that executes the command associated with
// the URLCommand stored in the request context.
//
// The handler performs the following steps:
//
//   - reads the URLCommand from request context,
//   - extracts request parameters (query, headers, body text, optional body JSON),
//   - renders the configured command template,
//   - selects execution/output mode ("buffered", "stream", or "async"),
//   - executes the command through the provided CommandExecutor.
//
// Execution modes:
//
//   - "buffered" (default):
//     command output is buffered and written to the response after the command exits,
//
//   - "stream":
//     command output is forwarded to the response as it is produced;
//     requires http.Flusher support from the ResponseWriter,
//
//   - "async":
//     command is started asynchronously and output is discarded.
//
// HTTP status code behavior:
//
//   - 200 OK
//     returned when the command starts successfully, regardless of whether the command
//     later exits with code 0 or non-zero, or fails while waiting/executing.
//     In other words, runtime/process execution failures are reported via response
//     headers, not via non-200 status codes.
//
//     In this case the handler sets:
//
//   - X-Success: "true" if exit code == 0, otherwise "false"
//
//   - X-Exit-Code: process exit code if available
//
//   - X-Error-Message: empty on success, otherwise execution error message
//
//   - 429 Too Many Requests
//     returned when command execution cannot start because the call gate rejects the
//     request as busy (callgate.ErrBusy).
//
//   - 404 Not Found
//     returned when URLCommand is missing from request context.
//
//   - 400 Bad Request
//     returned when bodyAsJson is enabled but the request body is not a valid JSON object.
//
//   - 500 Internal Server Error
//     returned when the command cannot be prepared or started at all, for example:
//
//   - streaming was requested but ResponseWriter does not support flushing,
//
//   - command template rendering/building failed,
//
//   - command startup/preparation failed before execution began,
//
//   - handler configuration is invalid (for example unknown execution mode).
//
// Important distinction:
//
// A command that starts successfully but later fails is still treated as an HTTP-level
// success and therefore returns 200 OK. Such failures are exposed through X-Success,
// X-Exit-Code and X-Error-Message headers.
//
// This variant keeps the HTTP layer decoupled from process startup and gate handling:
// command execution orchestration is delegated to the provided CommandExecutor.
func ExecutionHandler(exec CommandExecutor) httpx.WebHandler { //nolint:ireturn
	return httpx.WebHandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) error {
		err := executionHandler(responseWriter, request, exec)
		if err != nil {
			return translateError(err)
		}

		return nil
	})
}

func executionHandler(
	responseWriter http.ResponseWriter,
	request *http.Request,
	exec CommandExecutor,
) error {
	rid, _ := RequestIDFromContext(request.Context())
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
		exec,
		cmd,
		cmdResult,
		responseWriter,
	)
}

func translateError(err error) error {
	if err == nil {
		return nil
	}

	if errors.Is(err, ErrStreamingNotSupported) {
		return httpx.NewWebError(err, http.StatusInternalServerError, ErrStreamingNotSupported.Error())
	}

	if errors.Is(err, executor.ErrBusy) {
		return httpx.NewWebError(err, http.StatusTooManyRequests, "Too many requests")
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
	exec CommandExecutor,
	cmd *config.URLCommand,
	cmdResult *cmdbuilder.Result,
	writer io.Writer,
	async bool,
	responseWriter http.ResponseWriter,
) error {
	rid, _ := RequestIDFromContext(ctx)

	req := executor.ExecuteRequest{
		Command:                 cmdResult.Command,
		Arguments:               cmdResult.Arguments,
		OutputWriter:            writer,
		Async:                   async,
		GraceTerminationTimeout: cmd.GraceTerminationTimeout,
		CallGate:                cmd.CallGate,
		DefaultGroup:            cmd.URL,
		Timeout:                 cmd.CommandConfig.Timeout,
	}

	res := exec.Execute(ctx, req)
	if res.Err != nil {
		if errors.Is(res.Err, executor.ErrPreExecution) {
			return fmt.Errorf("failed to start command: %w", res.Err)
		}

		responseWriter.Header().Set("X-Success", "false")
		responseWriter.Header().Set("X-Error-Message", res.Err.Error())
		responseWriter.Header().Set("X-Exit-Code", "")
		log.Printf("[ERROR] rid=%s Command failed with error: %v", rid, res.Err)

		return httpx.NewSilentError(res.Err)
	}

	responseWriter.Header().Set("X-Success", strconv.FormatBool(res.ExitCode == 0))
	responseWriter.Header().Set("X-Error-Message", "")
	responseWriter.Header().Set("X-Exit-Code", strconv.Itoa(res.ExitCode))
	log.Printf("[INFO] rid=%s Command finished with exit code: %d", rid, res.ExitCode)

	return nil
}

func getURLCommandFromContext(request *http.Request) (*config.URLCommand, error) {
	cmd, ok := URLCommandFromContext(request.Context())
	if !ok {
		return nil, fmt.Errorf("URLCommand not found in context: %w", ErrCommandNotFound)
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
	exec CommandExecutor,
	cmd *config.URLCommand,
	cmdResult *cmdbuilder.Result,
	responseWriter http.ResponseWriter,
) error {
	executionMode := cmd.CommandConfig.ExecutionMode

	switch executionMode {
	case "async":
		return prepareOutputAndRunAsyncCommand(ctx, exec, cmd, cmdResult, responseWriter)
	case "stream":
		return prepareOutputAndRunStreamCommand(ctx, exec, cmd, cmdResult, responseWriter)
	case "", "buffered":
		const defaultThresholdBufferLimit = 10 * 1024

		buf := NewThresholdBuffer(defaultThresholdBufferLimit)

		defer func() {
			if err := buf.Close(); err != nil {
				log.Printf("[ERROR] failed to close output buffer: %v", err)
			}
		}()

		return prepareOutputAndRunSyncCommand(ctx, exec, cmd, cmdResult, buf, responseWriter)
	default:
		return fmt.Errorf("%w: unknown execution mode %q", ErrBadConfiguration, executionMode)
	}
}

func prepareOutputAndRunAsyncCommand(
	ctx context.Context,
	exec CommandExecutor,
	cmd *config.URLCommand,
	cmdResult *cmdbuilder.Result,
	responseWriter http.ResponseWriter,
) error {
	return runCommand(
		ctx,
		exec,
		cmd,
		cmdResult,
		io.Discard,
		true,
		responseWriter,
	)
}

func prepareOutputAndRunStreamCommand(
	ctx context.Context,
	exec CommandExecutor,
	cmd *config.URLCommand,
	cmdResult *cmdbuilder.Result,
	responseWriter http.ResponseWriter,
) error {
	if _, ok := responseWriter.(http.Flusher); !ok {
		return ErrStreamingNotSupported
	}

	responseWriter.Header().Add("Trailer", "X-Success")
	responseWriter.Header().Add("Trailer", "X-Error-Message")
	responseWriter.Header().Add("Trailer", "X-Exit-Code")

	responseWriter.Header().Set("Content-Type", "text/plain; charset=utf-8")
	responseWriter.Header().Set("Cache-Control", "no-cache")
	// nginx:
	responseWriter.Header().Set("X-Accel-Buffering", "no")

	writer := newFlushResponseWriter(responseWriter)

	return runCommand(
		ctx,
		exec,
		cmd,
		cmdResult,
		writer,
		false,
		responseWriter,
	)
}

func prepareOutputAndRunSyncCommand(
	ctx context.Context,
	exec CommandExecutor,
	cmd *config.URLCommand,
	cmdResult *cmdbuilder.Result,
	buf outputBuffer,
	responseWriter http.ResponseWriter,
) error {
	responseWriter.Header().Set("Content-Type", "text/plain; charset=utf-8")

	err := runCommand(
		ctx,
		exec,
		cmd,
		cmdResult,
		buf,
		false,
		responseWriter,
	)
	if err != nil {
		return err
	}

	if _, writeErr := buf.WriteTo(responseWriter); writeErr != nil {
		log.Printf("[ERROR] failed to write buffered output: %v", writeErr)
	}

	return nil
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
