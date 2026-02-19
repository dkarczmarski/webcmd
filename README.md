# webcmd

[![Build & Test (Go)](https://github.com/dkarczmarski/webcmd/actions/workflows/build.yml/badge.svg)](https://github.com/dkarczmarski/webcmd/actions/workflows/build.yml)

> [!WARNING]
> **Status: Pre-1.0 (v0.x.x)**
> The public API is not stable yet and may change between releases.

**webcmd** is a lightweight tool that allows you to execute predefined commands on a host machine via HTTP/HTTPS endpoints.

It is designed for small projects, CI/CD tasks, and maintenance jobs where you need to trigger a process or command on a remote machine - without giving full SSH access.

Instead of exposing broad system permissions, **webcmd** lets you define a safe, explicit list of commands that can be executed remotely. Commands can be parameterized and protected with API keys.

## Why webcmd?

In many scenarios (CI/CD pipelines, automation, maintenance):

- You need to run **one specific command** on a remote host.
- Giving SSH access is **too powerful and risky**.
- You want a **simple HTTP/HTTPS interface** instead.

**webcmd** solves this by:
- Exposing commands via HTTP/HTTPS endpoints.
- Allowing optional API key authorization per endpoint.
- Supporting command parameters from the request.

## Build and run

#### Fetch and build

```shell
git clone https://github.com/dkarczmarski/webcmd.git
cd webcmd
go build -o webcmd cmd/main.go
```

#### Run and test sample commands

Run sample configuration:

```shell
cp config.sample.yaml config.yaml
./webcmd -config config.yaml
```

For more examples, check [config.sample.yaml](config.sample.yaml) and [config.sample-ssl.yaml](config.sample-ssl.yaml) (HTTPS).

Test the `POST /cmd/echo` endpoint, which executes the `/bin/echo` command with a message passed as a query parameter. This endpoint requires authorization using an API key:

```shell
curl -H "X-Api-Key: MYSECRETKEY" -X POST http://localhost:8080/cmd/echo?message=hello
```

```output
hello
```

Test the `GET /stream/time` endpoint, which streams the output of a bash script that prints the current time every second for 10 seconds. This endpoint also requires an API key for authorization:

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

1. Define the command you want to run. You can create a command template that uses parameters taken from URL query parameters or from the request body or from the http header. In the command definition, each argument must be placed on a separate line.
2. Assign it to an HTTP/HTTPS endpoint.
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
* no Timeout.

Define `config.yaml`:

```yaml
authorization:
  - name: my-auth-1
    key: MYSECRETKEY
urlCommands:
  - url: GET /docker/logs/my-container
    authorizationName: my-auth-1
    commandTemplate: |
      docker
      logs
      -f
      my-container
    outputType: stream
```

Call the endpoint:

```shell
curl -H "X-Api-Key: MYSECRETKEY" \
     "http://localhost:8080/docker/logs/my-container"
```

When you close the connection, the command is stopped.

#### Example 4 - Asynchronous execution

We want to trigger a long-running background task (e.g., a backup script) without waiting for it to finish.

* Endpoint: `POST /maintenance/backup`.
* Command: `/usr/local/bin/backup.sh`.
* Output type: `none` (asynchronous).
* Timeout: `1h`.

Define `config.yaml`:

```yaml
urlCommands:
  - url: POST /maintenance/backup
    commandTemplate: |
      /usr/local/bin/backup.sh
    outputType: none
    timeout: 1h
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

* `shutdownGracePeriod` *(optional)* - the time to wait for active requests to finish before the server shuts down (e.g., `5s`, `30s`). Format: [Go Duration](https://pkg.go.dev/time#ParseDuration). Default: `5s`.

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

Defines HTTP/HTTPS endpoints and the commands they execute.

Each entry contains:

* `url`
  HTTP method and path, e.g. `GET /health` or `POST /deploy`.

* `authorizationName` *(optional)*
  Name of authorization defined in `authorization`. Multiple names can be separated by commas (e.g., `auth1,auth2`).

* `commandTemplate`
  Command template:

    * First line: executable.
    * Each following line: one argument.
    * Empty lines are ignored.
    * Supports Go `text/template` syntax [https://golang.org/pkg/text/template/](https://golang.org/pkg/text/template/).

  Request data (e.g. query parameters, HTTP headers, body and JSON body) can be used as placeholders.

* `timeout` *(optional)*
  Timeout for the command execution (e.g., `30s`, `1m`, `1h`). Format: [Go Duration](https://pkg.go.dev/time#ParseDuration).

* `graceTerminationTimeout` *(optional)*
  The time to wait for the process to exit gracefully after sending `SIGTERM` when the context is cancelled (e.g., client disconnects or timeout occurs) before sending `SIGKILL`. Format: [Go Duration](https://pkg.go.dev/time#ParseDuration). Default: no grace period (sends `SIGKILL` immediately).

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
    timeout: 30s
```

Call the endpoint:

```shell
curl -H "X-Api-Key: MYSECRETKEY" -X POST http://localhost:8080/cmd/echo?message=hello
```
