# AGENTS.md

Instructions for agents working on this repo.

## Frontend dev workflow

**DO NOT run `pnpm build` (or `make frontend-build`) during development.** In dev, the OpenCloud web UI is served by the [opencloud-eu/web](https://github.com/opencloud-eu/web) vite dev server running on port **9201**, which connects to our OpenCloud instance on 9200. Our extension is loaded via the extension-sdk's auto-registration from `pnpm serve` in this repo — assume the user keeps both running.

- Production build (`pnpm build`) is only for CI and the Docker image.
- The bundled frontend dist mounted into OpenCloud (`./frontend/dist:/web/apps/synaplan`) is only for production/CI.

## Running E2E tests

The E2E tests default to hitting OpenCloud on port 9200 (where the bundled dist is served). When developing against the OpenCloud web dev server, run tests against port 9201:

```bash
# Against OpenCloud web dev server on :9201 (use this during development)
make frontend-test-e2e-dev
# or directly:
cd frontend && pnpm test:e2e:dev
# or with the env var:
BASE_URL_OC=https://host.docker.internal:9201 pnpm test:e2e
```

Both ports are CORS-allowed via the Traefik middleware in `docker-compose.yml`.

## Prerequisites

The dev stack needs the [Synaplan dev stack](https://github.com/metadist/synaplan) running with `docker compose --profile oidc up -d`. This provides Keycloak (shared OIDC provider), the Synaplan backend, and the `synaplan-opencloud` Keycloak confidential client used for token exchange.

## Tool usage

- Use `pnpm` (not `npx`, not `npm`) to invoke frontend tools.
- Prefer the Makefile targets over raw `pnpm`/`go` commands when possible.
- Formatting: `make format` formats both frontend (prettier) and backend (gofmt).
- The `backend/` dir has its own Makefile (`build`, `test`, `lint`, `format`) — the root Makefile delegates to it.

## Commits

- Conventional commits (`feat:`, `fix:`, `docs:`, `chore:`, `refactor:`, `test:`).
- No co-author attribution.
- Semantic branch names (`feat/…`, `fix/…`, `docs/…`, `chore/…`).
- Main branch is protected — all changes go through PRs. The `All Checks Passed` gate must be green before merge.
