// Package synaplanapi contains the generated Synaplan API client.
//
// The client is generated from the OpenAPI spec published by the pinned
// synaplan Docker image (see ../../scripts/generate-synaplan-client.sh).
// Neither the spec nor the generated client.gen.go are committed — run
// `go generate ./...` or `make generate` to produce them.
package synaplanapi

//go:generate ../../scripts/generate-synaplan-client.sh

// apiKeyContextKey is referenced by the generated client when the spec
// advertises an ApiKey scheme. oapi-codegen v2.7.1 emits the constant
// but not this type for the current Synaplan spec.
type apiKeyContextKey string
