.PHONY: build test lint

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

build:
	go build -o bin/blinkenkeysd ./cmd/blinkenkeysd
	CGO_ENABLED=0 go build -ldflags "-X main.version=$(VERSION)" -o bin/blincli ./cmd/blincli

test:
	go test ./...

lint:
	prek run --all-files
