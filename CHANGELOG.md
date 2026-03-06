# Changelog

## v0.9.0

### Changed
- **Breaking Change**: Renamed `outputType` to `executionMode` in `URLCommand` configuration.
    - The new field `executionMode` accepts three values:
        - `buffered`: Run command synchronously and return full output once it finishes (replaces former `text`).
        - `stream`: Run command synchronously and stream output in real-time.
        - `async`: Start command and return immediately without waiting for it to finish (replaces former `none`).
    - No backward compatibility for `outputType`.

## v0.8.0

### Added
- Standardized HTTP status codes for command execution results:
    - **200 OK**: Returned when a command starts successfully (even if it later exits with a non-zero code).
    - **429 Too Many Requests**: Returned when the call gate is busy.
    - **400 Bad Request**: Returned for invalid JSON in the request body (when `bodyAsJson` is enabled).
    - **404 Not Found**: Returned when the command or endpoint is missing.
    - **500 Internal Server Error**: Returned for internal or configuration failures before the command starts.
- Improved command execution reporting via response headers:
    - `X-Success`: Indicates if the process exited with code 0 (`"true"`/`"false"`).
    - `X-Exit-Code`: Provides the process exit code (if available).
    - `X-Error-Message`: Contains the execution error message (when `withErrorHeader` is enabled).
- Added `withErrorHeader` configuration option to the `server` section to control the inclusion of execution error messages in HTTP response headers.

## v0.7.0

### Added
- Introduced execution concurrency control with `single` and `sequential` modes.
    - `single` mode: Only one instance of a command (or a group of commands) can run at a time; subsequent requests are rejected.
    - `sequential` mode: Requests are queued and executed one after another.
- Added `callGate` configuration to endpoints for fine-grained execution isolation and queuing.

## v0.6.0

### Added
- Added `shutdownGracePeriod` configuration parameter to control the graceful shutdown time.

## v0.5.0

### Added
- Added `RequestIDMiddleware` to track requests with a unique ID (returned in `X-Request-ID` header).
- Added `graceTerminationTimeout` configuration option for commands to allow graceful shutdown before forceful termination.
- Support for `time.Duration` format (e.g., `10s`, `1m`) in `timeout` and `graceTerminationTimeout` configuration fields.

## v0.4.0

### Added
- Commands are now executed in a separate process group, ensuring that they and all their child processes are terminated together.
- Added support for asynchronous execution when `outputType` is set to `none`.

## v0.3.0

### Added
- Added HTTP headers extraction in command templates (`{{.headers.Header_Name}}`).

### Removed
- Removed `bodyAsText` option from configuration (request body as text is now always available via `{{.body.text}}`).

## v0.2.0

### Added
- Added `params` section in command configuration (`URLCommand`) for explicit control over request body processing.
- Introduced default values for configuration:
    - Server listens on `127.0.0.1:8080` by default (or `127.0.0.1:8443` for HTTPS).
    - `bodyAsText` is enabled by default (`true`).
    - `bodyAsJson` is disabled by default (`false`).

### Changed
- **Breaking Change**: Updated request body access in command templates (`commandTemplate`):
    - Replaced `{{.bodyAsText}}` with `{{.body.text}}`.
    - Replaced `{{.bodyAsJson}}` with `{{.body.json}}`.

## v0.1.0

- Execute system commands via HTTP endpoints.
- API key authorization (X-Api-Key header).
- Parameterize executed commands.
- Retrieve parameters from URL query params for command generation.
- Use request body as text or JSON for command parameterization (with support for extracting individual JSON fields).
- Two output modes: text (full output) and stream (real-time).
- Configurable command timeouts.
- HTTPS support.
- Easy configuration using YAML.