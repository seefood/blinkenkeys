.PHONY: build test lint

build:
	go build -o bin/blinkenkeysd ./cmd/blinkenkeysd

test:
	go test ./...

lint:
	prek run --all-files
