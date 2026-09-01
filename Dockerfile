FROM golang:1.25-alpine@sha256:1ae0735f00daffa3aaf1363a5184c0d2dc55c78e3db4ec70241cdac97bf84b59 AS builder

WORKDIR /app
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ .

# The Synaplan API client is generated from the pinned synaplan image's
# OpenAPI spec and is NOT committed. It must be generated on the host
# (via `make -C backend generate`) before running docker build.
RUN test -f internal/synaplanapi/client.gen.go || { \
  echo "ERROR: backend/internal/synaplanapi/client.gen.go is missing."; \
  echo "Run 'make -C backend generate' on the host before docker build."; \
  exit 1; }

RUN CGO_ENABLED=0 go build -o /synaplan-opencloud ./cmd/synaplan

FROM alpine:3.20@sha256:d9e853e87e55526f6b2917df91a2115c36dd7c696a35be12163d44e6e2a4b6bc
COPY --from=builder /synaplan-opencloud /usr/local/bin/synaplan-opencloud
COPY frontend/dist/ /web/apps/synaplan/
EXPOSE 9106
ENTRYPOINT ["synaplan-opencloud", "server"]
