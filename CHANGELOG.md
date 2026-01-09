# Changelog

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