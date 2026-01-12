# webcmd

[![Build & Test (Go)](https://github.com/dkarczmarski/webcmd/actions/workflows/build.yml/badge.svg)](https://github.com/dkarczmarski/webcmd/actions/workflows/build.yml)

> [!WARNING]
> **Status: Pre-1.0 (v0.x.x)**
> The public API is not stable yet and may change between releases.

**webcmd** is a lightweight tool that allows you to execute predefined commands on a host machine via HTTP endpoints.

It is designed for small projects, CI/CD tasks, and maintenance jobs where you need to trigger a process or command on a remote machine - without giving full SSH access.

Instead of exposing broad system permissions, **webcmd** lets you define a safe, explicit list of commands that can be executed remotely. Commands can be parameterized and protected with API keys.

## Why webcmd?

In many scenarios (CI/CD pipelines, automation, maintenance):

- You need to run **one specific command** on a remote host.
- Giving SSH access is **too powerful and risky**.
- You want a **simple HTTP interface** instead.

**webcmd** solves this by:
- Exposing commands via HTTP endpoints.
- Allowing optional API key authorization per endpoint.
- Supporting command parameters from the request.

## Build and run

#### Fetch and build

```shell
git clone https://github.com/dkarczmarski/webcmd.git
cd webcmd
go build -o webcmd ./cmd
```

#### Run and test sample commands

Run sample configuration:

```shell
cp config.sample.yaml config.yaml
./webcmd -config config.yaml
```

Run a sample command:

```shell
curl -H "X-Api-Key: MYSECRETKEY" -X POST http://localhost:8080/cmd/echo?message=hello
```

```output
hello
```

Run a sample command:

```shell
curl -H "X-Api-Key: MYSECRETKEY" http://localhost:8080/stream/time
```

```output
1 11:47:28
2 11:47:29
3 11:47:30
4 11:47:31
5 11:47:32
6 11:47:33
7 11:47:34
8 11:47:36
9 11:47:37
10 11:47:38
```

## Quick Start

#### Basic steps

1. Define the command you want to run. You can create a command template that uses parameters taken from URL query parameters or from the request body. In the command definition, each argument must be placed on a separate line.
2. Assign it to an HTTP endpoint.
3. (Optional) Protect the endpoint with an API key.

#### Example 1 - Public endpoint (no authorization)

We want to create an endpoint that returns the current disk usage using the `df -h` command.

- Endpoint: `GET /maintenance/diskspace`.
- Authorization: **none**.
- Command: `df -h`.

Define `config.yaml`:

```yaml
urlCommands:
  - url: GET /maintenance/diskspace
    commandTemplate: |
      df
      -h
````

Call the endpoint:

```shell
curl http://localhost:8080/maintenance/diskspace
```

#### Example 2 - Protected endpoint with API key and parameters

We want to run:

```shell
/usr/local/bin/myapp restart --reason MY_REASON
```

* Endpoint: `POST /myapp/restart`.
* Required API key: `MYSECRETKEY`.
* Parameter: `reason` (taken from query parameters).
* Command: `/usr/local/bin/myapp restart --reason {{.url.reason}}`.

Define `config.yaml`:

```yaml
authorization:
  - name: my-auth-1
    key: MYSECRETKEY
urlCommands:
  - url: POST /myapp/restart
    authorizationName: my-auth-1
    commandTemplate: |
      /usr/local/bin/myapp
      restart
      --reason
      {{.url.reason}}
```

Call the endpoint:

```shell
curl -H "X-Api-Key: MYSECRETKEY" \
     -X POST \
     "http://localhost:8080/myapp/restart?reason=MY_REASON"
```

#### Example 3 - Streaming output

We want to watch the output of the following command online:

```shell
docker logs -f my-container
```

* Endpoint: `GET /docker/logs/my-container`.
* Required API key: `MYSECRETKEY`.
* Command: `docker logs -f my-container`.
* Output type: `stream`.
* Timeout: `0` (no timeout).

Define `config.yaml`:

```yaml
authorization:
  - name: my-auth-1
    key: MYSECRETKEY
urlCommands:
  - url: GET /docker/logs/my-container
    commandTemplate: |
      docker
      logs
      -f
      my-container
    outputType: stream
    timeout: 0
```

Call the endpoint:

```shell
curl -H "X-Api-Key: MYSECRETKEY" \
     -X POST \
     "http://localhost:8080/docker/logs/my-container"
```

When you close the connection, the command is stopped.

#### Example 4 - Asynchronous execution

We want to trigger a long-running background task (e.g., a backup script) without waiting for it to finish.

* Endpoint: `POST /maintenance/backup`.
* Command: `/usr/local/bin/backup.sh`.
* Output type: `none` (asynchronous).
* Timeout: `3600` (the background task will be killed if it runs longer than 1 hour).

Define `config.yaml`:

```yaml
urlCommands:
  - url: POST /maintenance/backup
    commandTemplate: |
      /usr/local/bin/backup.sh
    outputType: none
    timeout: 3600
```

Call the endpoint:

```shell
curl -X POST http://localhost:8080/maintenance/backup
```

The server will return an immediate response as soon as the command starts. The command will continue running in the background. Even in asynchronous mode, the optional `timeout` is respected - if the process exceeds the specified time, it will be terminated.

## Command template

The `commandTemplate` uses Go's `text/template` syntax to inject data from the HTTP request into your command. The following data sources are available:

* `url` - provides access to URL query parameters.
  Example: `{{.url.param_name}}` .

* `body` - provides access to the request body.
  - `{{.body.text}}` - the entire request body as a plain string (enabled by default).
  - `{{.body.json}}` - the request body parsed as JSON (requires `params.bodyAsJson: true` in configuration).
  - `{{.body.json.field_name}}` - specific field from the JSON body.

* `headers` - provides access to HTTP request headers.
  Header names are normalized by replacing hyphens (`-`) with underscores (`_`).
  Example: `{{.headers.X_Api_Key}}` or `{{.headers.User_Agent}}` .

## Configuration (`config.yaml`)

### `server`

* `address` *(optional)* - address the server listens on, in `host:port` format. Default: `"127.0.0.1:8080"`.
  Examples:

    * `":8080"`
    * `"localhost:8080"`

* `https` *(optional)* - HTTPS configuration:
    * `enabled` - enable or disable HTTPS. Default: `false`.
    * `certFile` - path to the SSL certificate file.
    * `keyFile` - path to the SSL key file.

Example:

```yaml
server:
  address: ":8443"
  https:
    enabled: true
    certFile: "./cert.pem"
    keyFile: "./key.pem"
```

### `authorization`

List of API key authorizations.

Each authorization entry contains:

* `name` - identifier referenced by `urlCommands.authorizationName`.
* `key` - API key value, must be provided in the `X-Api-Key` HTTP header.

Example:

```yaml
authorization:
  - name: admin-auth
    key: MYSECRETKEY
```

### `urlCommands`

Defines HTTP endpoints and the commands they execute.

Each entry contains:

* `url`
  HTTP method and path, e.g. `GET /health` or `POST /deploy`.

* `authorizationName` *(optional)*
  Name of authorization defined in `authorization`.

* `commandTemplate`
  Command template:

    * First line: executable.
    * Each following line: one argument.
    * Empty lines are ignored.
    * Supports Go `text/template` syntax [https://golang.org/pkg/text/template/](https://golang.org/pkg/text/template/).

  Request data (e.g. query parameters) can be used as placeholders.

* `timeout` *(optional)*
  Timeout in seconds for the command execution. `0` means no timeout.

* `outputType` *(optional)*
  Determines how the command output is returned:
  - `text`: (default) returns the full output once the command finishes.
  - `stream`: returns the output in real-time as it is produced by the command.
  - `none`: executes the command asynchronously in the background. The HTTP response is sent immediately after the process starts, and any output is discarded. Note that the optional `timeout` is still respected for background processes.

* `params` *(optional)*
  Optional configuration for request body processing:

    * `bodyAsJson` *(optional)*
      If set to `true`, the HTTP request body will be parsed as JSON and made available in the command template under `{{.body.json}}`.
      - Allows access to individual fields, e.g., `{{.body.json.field_name}}`.
      - Using `{{.body.json}}` without a field will insert the full, valid JSON string.
      - Requires a valid `Content-Type: application/json` header.
      - Default: `false`.

The HTTP request body is always available as plain text in the command template as `{{.body.text}}`.

Example 1 - Accessing request body as text:

```yaml
urlCommands:
  - url: POST /echo-text
    commandTemplate: |
      /bin/echo
      -n
      {{.body.text}}
```

Call the endpoint:

```shell
curl -X POST http://localhost:8080/echo-text \
     -d "Hello from request body"
```

Example 2 - Using `bodyAsJson`:

```yaml
urlCommands:
  - url: POST /deploy
    params:
      bodyAsJson: true
    commandTemplate: |
      /usr/local/bin/deploy.sh
      --project
      {{.body.json.project_name}}
      --payload
      {{.body.json}}
```

Call the endpoint:

```shell
curl -X POST http://localhost:8080/deploy \
     -H "Content-Type: application/json" \
     -d '{"project_name": "my-app", "version": "1.0.1"}'
```

In the above example:
- `{{.body.json.project_name}}` will be replaced by `my-app`.
- `{{.body.json}}` will be replaced by the full JSON string: `{"project_name":"my-app","version":"1.0.1"}`.

Example 3 - using URL query parameters:

You can use any URL query parameters in the command template by prefixing the key name with `url.`.

```yaml
urlCommands:
  - url: POST /cmd/echo
    authorizationName: auth-name1    
    commandTemplate: |
      /bin/echo
      {{.url.message}}
    timeout: 30
```
