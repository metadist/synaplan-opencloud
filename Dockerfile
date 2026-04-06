FROM golang:1.25-alpine@sha256:8e02eb337d9e0ea459e041f1ee5eece41cbb61f1d83e7d883a3e2fb4862063fa AS builder

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

FROM alpine:3.20@sha256:a4f4213abb84c497377b8544c81b3564f313746700372ec4fe84653e4fb03805
COPY --from=builder /synaplan-opencloud /usr/local/bin/synaplan-opencloud
COPY frontend/dist/ /web/apps/synaplan/
EXPOSE 9106
ENTRYPOINT ["synaplan-opencloud", "server"]
