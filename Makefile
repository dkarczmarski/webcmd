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

dev-lint:
	golangci-lint run

test:
	go test -v ./...

test-all:
	go test -v -count=1 -tags=integration ./...