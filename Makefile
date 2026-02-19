dev-install-lint:
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

dev-goimports:
	goimports -w .

dev-gofumpt:
	gofumpt -w .

dev-go-mockgen-install:
	go install go.uber.org/mock/mockgen@latest

dev-go-generate:
	go generate ./...

dev-generate-cert:
	openssl req -x509 -newkey rsa:4096 \
      -keyout key.pem \
      -out cert.pem \
      -days 365 \
      -nodes \
      -subj "/CN=localhost"

dev-lint:
	golangci-lint run

test:
	go test -v -race ./...

test-all:
	go test -v -race -count=1 -tags=integration ./...

test-https-local:
	curl -k -X POST "https://localhost:8443/cmd/echo?message=hello123" \
      -H "X-Api-Key: MYSECRETKEY"

build:
	go build -o webcmd cmd/main.go

clean:
	rm -f webcmd
