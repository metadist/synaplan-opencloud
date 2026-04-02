.PHONY: build test lint format

build:
	go build -o bin/synaplan ./cmd/synaplan

test:
	go test ./...

lint:
	golangci-lint run

format:
	gofmt -w .
