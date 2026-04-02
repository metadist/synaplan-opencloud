# Synaplan OpenCloud Integration

> **Early development — not ready for production use.**

OpenCloud web extension that integrates [Synaplan](https://github.com/metadist/synaplan) AI features into OpenCloud. Uses [RFC 8693 token exchange](https://datatracker.ietf.org/doc/html/rfc8693) for per-user authentication.

**Synaplan and OpenCloud must be behind the same OIDC identity provider (Keycloak).** Token exchange only works when both services trust the same Keycloak realm. There is currently no support for separate identity providers.

> **Dev stack requires the [Synaplan dev stack](https://github.com/metadist/synaplan) running with `docker compose --profile oidc up -d`.**
>
> On Linux, add `127.0.0.1 host.docker.internal` to `/etc/hosts` (macOS/Windows: works out of the box).

## Quick Start

```bash
# 1. Start the Synaplan dev stack (in the synaplan repo)
cd ../synaplan && docker compose --profile oidc up -d

# 2. Build and start
make frontend-install && make frontend-build && make docker-up

# 3. Visit https://host.docker.internal:9200
#    Login with: testuser / testpass123
```

## Development

```bash
# Frontend
make frontend-install      # Install dependencies
make frontend-build        # Production build
make frontend-dev          # Watch mode (rebuild on change)
make frontend-lint         # ESLint
make frontend-format       # Prettier (write)
make frontend-format-check # Prettier (check)
make frontend-typecheck    # TypeScript type check
make frontend-test-unit    # Vitest unit tests
make frontend-test-e2e     # Playwright E2E tests

# Backend
make backend-build         # Build Go binary
make backend-dev           # Air hot-reload
make backend-test          # Go tests
make backend-lint          # golangci-lint
make backend-format        # gofmt

# All
make format                # Format frontend + backend

# Docker
make docker-up             # Start OpenCloud + backend
make docker-down           # Stop
```

## License

Apache-2.0
