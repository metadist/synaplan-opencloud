FROM golang:1.27-alpine@sha256:70bd7ddea8b6cbe5d6943cde279d6b67feb10cc924fb9100d1d8757c1575b280 AS builder

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

FROM alpine:3.24@sha256:f59abe656b692d901e184be3445e084786602108599d2a51d229c0f5a48c71bf
COPY --from=builder /synaplan-opencloud /usr/local/bin/synaplan-opencloud
COPY frontend/dist/ /web/apps/synaplan/
EXPOSE 9106
ENTRYPOINT ["synaplan-opencloud", "server"]
