# Changelog

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