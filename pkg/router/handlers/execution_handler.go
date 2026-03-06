package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/dkarczmarski/webcmd/pkg/callgate"
	"github.com/dkarczmarski/webcmd/pkg/cmdbuilder"
	"github.com/dkarczmarski/webcmd/pkg/config"
	"github.com/dkarczmarski/webcmd/pkg/gateexec"
	"github.com/dkarczmarski/webcmd/pkg/httpx"
	"github.com/dkarczmarski/webcmd/pkg/processrunner"
)

var (
	ErrStreamingNotSupported = errors.New("streaming not supported")
	ErrCommandNotFound       = errors.New("command not found")
	ErrInvalidJSONBody       = errors.New("invalid JSON body")
)

// ProcessStarter abstracts starting a process for a command execution.
// It is implemented by processrunner.ProcessRunner, which binds cmdrunner.Runner internally.
// This keeps the handler decoupled from cmdrunner.Runner and makes testing easier.
type ProcessStarter interface {
	StartProcess(
		command string,
		args []string,
		writer io.Writer,
		graceTimeout *time.Duration,
		opts ...processrunner.Option,
	) (*processrunner.Process, error)
}

// GateExecutor abstracts gateexec behavior so handlers don't need callgate.Registry.
// A concrete implementation can internally bind the registry and delegate to gateexec.
type GateExecutor interface {
	Run(ctx context.Context, gateConfig *config.CallGateConfig, key string, action gateexec.Action) (int, error)
}

// ExecutionHandler returns a WebHandler that executes the command associated with
// the URLCommand stored in the request context.
//
// The handler performs the following steps:
//
//   - reads the URLCommand from request context,
//   - extracts request parameters (query, headers, body text, optional body JSON),
//   - renders the configured command template,
//   - selects output mode ("text", "stream", or "none"),
//   - executes the command through the provided GateExecutor and ProcessStarter.
//
// Output modes:
//
//   - "text" (default):
//     command output is buffered and written to the response after the process exits,
//   - "stream":
//     command output is forwarded to the response as it is produced;
//     requires http.Flusher support from the ResponseWriter,
//   - "none":
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
//   - gate/pre-action setup failed before the process was started,
//
//   - handler configuration is invalid (for example unknown output type).
//
// Important distinction:
//
// A command that starts successfully but later fails is still treated as an HTTP-level
// success and therefore returns 200 OK. Such failures are exposed through X-Success,
// X-Exit-Code and X-Error-Message headers.
//
// This variant keeps construction simple for production wiring: it accepts concrete
// implementations that already bind their dependencies (process runner and gate executor).
func ExecutionHandler(pr *processrunner.ProcessRunner, exec GateExecutor) httpx.WebHandler { //nolint:ireturn
	return ExecutionHandlerWithDeps(pr, exec)
}

// ExecutionHandlerWithDeps is a test-friendly variant of ExecutionHandler that accepts abstractions
// for starting processes and executing under a gate.
func ExecutionHandlerWithDeps(starter ProcessStarter, exec GateExecutor) httpx.WebHandler { //nolint:ireturn
	return httpx.WebHandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) error {
		err := executionHandler(responseWriter, request, starter, exec)
		if err != nil {
			return translateError(err)
		}

		return nil
	})
}

func executionHandler(
	responseWriter http.ResponseWriter,
	request *http.Request,
	starter ProcessStarter,
	exec GateExecutor,
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
		starter,
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

	if errors.Is(err, callgate.ErrBusy) {
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
	starter ProcessStarter,
	exec GateExecutor,
	cmd *config.URLCommand,
	cmdResult *cmdbuilder.Result,
	writer io.Writer,
	async bool,
	responseWriter http.ResponseWriter,
) error {
	rid := requestIDFromContext(ctx)
	action := createGateAction(starter, cmd, cmdResult, writer, async)

	exitCode, err := exec.Run(ctx, cmd.CallGate, cmd.URL, action)
	if err != nil {
		if errors.Is(err, gateexec.ErrPreAction) {
			return fmt.Errorf("failed to start command: %w", err)
		}

		responseWriter.Header().Set("X-Success", "false")
		responseWriter.Header().Set("X-Error-Message", err.Error())
		responseWriter.Header().Set("X-Exit-Code", "")
		log.Printf("[ERROR] rid=%s Command failed with error: %v", rid, err)

		return httpx.NewSilentError(err)
	}

	responseWriter.Header().Set("X-Success", strconv.FormatBool(exitCode == 0))
	responseWriter.Header().Set("X-Error-Message", "")
	responseWriter.Header().Set("X-Exit-Code", strconv.Itoa(exitCode))
	log.Printf("[INFO] rid=%s Command failed with exit code: %d", rid, exitCode)

	return nil
}

func createGateAction(
	starter ProcessStarter,
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

		proc, err := startCommandProcess(starter, command, arguments, writer, cmd.GraceTerminationTimeout)
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

func startCommandProcess(
	starter ProcessStarter,
	command string,
	arguments []string,
	writer io.Writer,
	graceTerminationTimeout *time.Duration,
) (*processrunner.Process, error) {
	proc, err := starter.StartProcess(command, arguments, writer, graceTerminationTimeout)
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
	starter ProcessStarter,
	exec GateExecutor,
	cmd *config.URLCommand,
	cmdResult *cmdbuilder.Result,
	responseWriter http.ResponseWriter,
) error {
	outputType := cmd.CommandConfig.OutputType

	switch outputType {
	case "none":
		return prepareOutputAndRunAsyncCommand(ctx, starter, exec, cmd, cmdResult, responseWriter)
	case "stream":
		return prepareOutputAndRunStreamCommand(ctx, starter, exec, cmd, cmdResult, responseWriter)
	case "", "text":
		return prepareOutputAndRunSyncCommand(ctx, starter, exec, cmd, cmdResult, responseWriter)
	default:
		return fmt.Errorf("%w: unknown output type %q", ErrBadConfiguration, outputType)
	}
}

func prepareOutputAndRunAsyncCommand(
	ctx context.Context,
	starter ProcessStarter,
	exec GateExecutor,
	cmd *config.URLCommand,
	cmdResult *cmdbuilder.Result,
	responseWriter http.ResponseWriter,
) error {
	return runCommand(
		ctx,
		starter,
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
	starter ProcessStarter,
	exec GateExecutor,
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
		starter,
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
	starter ProcessStarter,
	exec GateExecutor,
	cmd *config.URLCommand,
	cmdResult *cmdbuilder.Result,
	responseWriter http.ResponseWriter,
) error {
	var buf bytes.Buffer

	responseWriter.Header().Set("Content-Type", "text/plain; charset=utf-8")

	err := runCommand(
		ctx,
		starter,
		exec,
		cmd,
		cmdResult,
		&buf,
		false,
		responseWriter,
	)
	if err != nil {
		return err
	}

	if _, writeErr := responseWriter.Write(buf.Bytes()); writeErr != nil {
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
