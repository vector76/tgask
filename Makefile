VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags "-X main.version=$(VERSION)"
BINARY := bin/tgask

.PHONY: build build-linux build-all test

build:
	go build $(LDFLAGS) -o $(BINARY) ./cmd/tgask

build-linux:
	GOOS=linux GOARCH=amd64 go build $(LDFLAGS) -o bin/tgask-linux-amd64 ./cmd/tgask

build-all:
	GOOS=linux   GOARCH=amd64  go build $(LDFLAGS) -o bin/tgask-linux-amd64    ./cmd/tgask
	GOOS=darwin  GOARCH=amd64  go build $(LDFLAGS) -o bin/tgask-darwin-amd64   ./cmd/tgask
	GOOS=darwin  GOARCH=arm64  go build $(LDFLAGS) -o bin/tgask-darwin-arm64   ./cmd/tgask
	GOOS=windows GOARCH=amd64  go build $(LDFLAGS) -o bin/tgask-windows-amd64.exe ./cmd/tgask

test:
	go test -race ./...
