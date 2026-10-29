# Synaplan OpenCloud Integration

> **Early development — not ready for production use.**

📖 **[Service reference docs](https://metadist.github.io/synaplan-opencloud/)** — env vars, example config, and deprecations, regenerated from the `main` branch on every push.

OpenCloud web extension that integrates [Synaplan](https://github.com/metadist/synaplan) AI features into OpenCloud. When both sides share a login provider, each person's OpenCloud login is exchanged for their own Synaplan account with [RFC 8693 token exchange](https://datatracker.ietf.org/doc/html/rfc8693). A shared API key is also supported; in that mode every person uses the same Synaplan account.

From a file you can summarize, translate, chat about it, and make it searchable by AI (including update, remove, and keep it up to date). A result can be saved into your personal space. Synaplan can copy a file into that space, read a folder you allow, and watch a folder or a file. Files sent to Synaplan are labelled as coming from OpenCloud. Sign-in is the shared organisation login; a shared API key is still available and discouraged.

![Screenshot](docs/screenshot.png)

**Requires OpenCloud 9.0.0 or later** (web 8.2). The dev stack pins `opencloudeu/opencloud-rolling:9.0.0`. There was no stable pre-release after 9.0.0; the `daily` tag is not a release.

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
#    Login with: admin / admin, demo / demo or testuser / testpass123
```

## Configuration

The frontend extension and the Go backend are configured separately.

**Frontend** — set the Synaplan instance URL via OpenCloud's [`apps.yaml`](dev/docker/opencloud.apps.yaml):

```yaml
synaplan:
  config:
    synaplanUrl: 'https://synaplan.example.com'
```

The app menu tile opens this URL in a new tab when set.

**Backend** — env vars (full list with descriptions in [`backend/pkg/config/config.go`](backend/pkg/config/config.go)).

The backend authenticates outbound calls to Synaplan in one of two modes. Pick one.

### Mode A: per-user OIDC token exchange (recommended)

Each call to Synaplan rides on the logged-in OpenCloud user's identity, exchanged via Keycloak's RFC 8693 token-exchange grant. Synaplan sees the real user; per-user audit, quotas and rate limits work as expected. Requires Synaplan and OpenCloud to share the same Keycloak realm.

| Var | Purpose |
|-----|---------|
| `SYNAPLAN_URL` | Base URL of the Synaplan instance the backend talks to. |
| `SYNAPLAN_OIDC_TOKEN_ENDPOINT` | Keycloak token endpoint, used for token exchange. |
| `SYNAPLAN_OIDC_EXCHANGE_CLIENT_ID` / `SYNAPLAN_OIDC_EXCHANGE_CLIENT_SECRET` | Confidential client credentials that perform the exchange. |
| `SYNAPLAN_OIDC_TARGET_AUDIENCE` | Audience (Synaplan client ID) baked into the exchanged token. |

### Mode B: shared API key (simple, discouraged)

For single-tenant deployments where Synaplan and OpenCloud don't share an identity provider, the backend can attach a single static Synaplan API key to every outbound call instead of doing token exchange. Generate one in Synaplan's API-Keys settings, then set:

| Var | Purpose |
|-----|---------|
| `SYNAPLAN_URL` | Base URL of the Synaplan instance. |
| `SYNAPLAN_API_KEY` | Synaplan API key (`sk_…`). Sent as `X-API-Key` on every outbound call. |

**Caveat:** in this mode every call to Synaplan looks like the same user. There is no per-user identity, no per-OC-user audit trail, no per-user quotas, and you cannot revoke access for an individual OpenCloud user without revoking the key for everyone. Prefer Mode A unless you understand and accept these tradeoffs.

When `SYNAPLAN_API_KEY` is set it takes precedence and the `SYNAPLAN_OIDC_*` variables are ignored.

### Shared

| Var | Purpose |
|-----|---------|
| `OC_REVA_GATEWAY` | CS3 gateway used by the backend to read user files. |
| `OC_JWT_SECRET` | Shared JWT secret with OpenCloud's reva proxy. Also encrypts keys in the state file when `SYNAPLAN_STATE_KEY` is unset. |
| `OC_MACHINE_AUTH_API_KEY` | Same value as on OpenCloud. Lets copies and watches read and write a person's files without their browser session. |
| `SYNAPLAN_INSTANCE_ID` / `SYNAPLAN_INSTANCE_SECRET` | The platform instance Synaplan assigned to this OpenCloud. Needed for copies, folder reads and watches. |
| `SYNAPLAN_ADMIN_API_KEY` | When set, and no instance id is stored yet, the service registers this OpenCloud on startup. |
| `SYNAPLAN_DELIVERY_URL` | Address Synaplan calls. In dev: `http://host.docker.internal:9106/api/synaplan/inbound`. |
| `SYNAPLAN_PUBLIC_URL` | Address a person opens in the browser, used in links back to a file. |
| `SYNAPLAN_STATE_PATH` | File for read settings and away-keys. Empty keeps them in memory only. |

The backend downloads file contents from OpenCloud's storage data server, so that server must be reachable from the backend. On the OpenCloud side set `STORAGE_USERS_HTTP_ADDR` to a non-loopback address and `STORAGE_USERS_DATA_SERVER_URL` to a URL the backend can resolve (for example `http://opencloud:9158/data`).

The dev defaults live in [`docker-compose.yml`](docker-compose.yml) and use Mode A.

## Development

```bash
# Frontend
make frontend-install       # Install dependencies
make frontend-serve         # Vite dev server on :9201 (with HMR, auto-registers in OpenCloud)
make frontend-build         # Production build (CI only)
make frontend-lint          # ESLint
make frontend-format        # Prettier (write)
make frontend-format-check  # Prettier (check)
make frontend-typecheck     # TypeScript type check
make frontend-test-unit     # Vitest unit tests
make frontend-test-e2e      # Playwright E2E tests (against :9200)
make frontend-test-e2e-dev  # Playwright E2E tests (against vite dev server :9201)

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

# Docs (service reference site — env vars, example config, deprecations)
make docs                  # Build Docusaurus site into docs/generated/ (gitignored)
make docs-serve-prod       # Serve the built site at http://localhost:3000
make docs-clean            # Wipe .cache/ + docs/generated/
```

## License

Apache-2.0
