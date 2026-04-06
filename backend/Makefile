.PHONY: build test lint format generate

# Generate the Synaplan API client from the pinned synaplan image's OpenAPI
# spec. The generated client is NOT committed — build/test/lint targets
# depend on this, so run `make generate` first after a fresh checkout.
generate:
	go generate ./...

build: generate
	go build -o bin/synaplan ./cmd/synaplan

test: generate
	go test ./...

lint: generate
	golangci-lint run

format:
	gofmt -w .
