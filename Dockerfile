FROM golang:1.25-alpine@sha256:8e02eb337d9e0ea459e041f1ee5eece41cbb61f1d83e7d883a3e2fb4862063fa AS builder

WORKDIR /app
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ .
RUN CGO_ENABLED=0 go build -o /synaplan-opencloud ./cmd/synaplan

FROM alpine:3.20@sha256:a4f4213abb84c497377b8544c81b3564f313746700372ec4fe84653e4fb03805
COPY --from=builder /synaplan-opencloud /usr/local/bin/synaplan-opencloud
COPY frontend/dist/ /web/apps/synaplan/
EXPOSE 9106
ENTRYPOINT ["synaplan-opencloud", "server"]
