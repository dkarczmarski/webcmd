# webcmd

**webcmd** is a lightweight tool that allows you to execute predefined commands on a host machine via HTTP endpoints.

It is designed for small projects, CI/CD tasks, and maintenance jobs where you need to trigger a process or command on a remote machine - without giving full SSH access.

Instead of exposing broad system permissions, **webcmd** lets you define a safe, explicit list of commands that can be executed remotely. Commands can be parameterized and protected with API keys.

## Why webcmd?

In many scenarios (CI/CD pipelines, automation, maintenance):

- You need to run **one specific command** on a remote host
- Giving SSH access is **too powerful and risky**
- You want a **simple HTTP interface** instead

**webcmd** solves this by:
- Exposing commands via HTTP endpoints
- Allowing optional API key authorization per endpoint
- Supporting command parameters from the request

## Build and run

```
git clone https://github.com/dkarczmarski/webcmd.git
cd webcmd
go build -o webcmd ./cmd

cp config.sample.yaml config.yaml
./webcmd -config config.yaml
```

## Quick Start

### Basic steps

1. Define the command you want to run (optionally using request parameters)
2. Assign it to an HTTP endpoint
3. (Optional) Protect the endpoint with an API key

### Example 1 - Public endpoint (no authorization)

We want to create an endpoint that returns the current disk usage using the `df -h` command.

- Endpoint: `GET /maintenance/diskspace`
- Authorization: **none**
- Command: `df -h`

Define `config.yaml`:

```yaml
urlCommands:
  - url: GET /maintenance/diskspace
    commandTemplate: |
      df
      -h
````

Call the endpoint:

```sh
curl localhost:8080/maintenance/diskspace
```

#### Example 2 - Protected endpoint with API key and parameters

We want to run:

```sh
/usr/local/bin/myapp restart --reason MY_REASON
```

* Endpoint: `POST /myapp/restart`
* Required API key: `MYKEY123`
* Parameter: `reason` (taken from query parameters)
* Command: `/usr/local/bin/myapp restart --reason {{.url.reason}}`

Define `config.yaml`:

```yaml
authorization:
  - name: my-auth-1
    key: MYKEY123
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

```sh
curl -H "X-Api-Key: MYKEY123" \
     -X POST \
     "localhost:8080/myapp/restart?reason=MY_REASON"
```

## Configuration (`config.yaml`)

### `server`

* `address` *(optional)* - address the server listens on, in `host:port` format. Default: `"127.0.0.1:8080"`.
  Examples:

    * `":8080"`
    * `"localhost:8080"`

* `https` *(optional)* - HTTPS configuration:
    * `enabled` - enable or disable HTTPS. Default: `false`.
    * `certFile` - path to the SSL certificate file
    * `keyFile` - path to the SSL key file

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

* `name` - identifier referenced by `urlCommands.authorizationName`
* `key` - API key value, must be provided in the `X-Api-Key` HTTP header

Example:

```yaml
authorization:
  - name: admin-auth
    key: SUPERSECRETKEY
```

### `urlCommands`

Defines HTTP endpoints and the commands they execute. 

Each entry contains:

* `url`
  HTTP method and path, e.g. `GET /health` or `POST /deploy`

* `authorizationName` *(optional)*
  Name of authorization defined in `authorization`

* `commandTemplate`
  Command template:

    * First line: executable
    * Each following line: one argument
    * Empty lines are ignored
    * Supports Go `text/template` syntax [https://golang.org/pkg/text/template/](https://golang.org/pkg/text/template/)

  Request data (e.g. query parameters) can be used as placeholders.

* `timeout` *(optional)*
  Timeout in seconds for the command execution

Example:

```yaml
urlCommands:
  - url: POST /cmd/echo
    authorizationName: auth-name1    
    commandTemplate: |
      /bin/echo
      {{.url.message}}
    timeout: 30
```
