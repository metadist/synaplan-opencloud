# AGENTS.md

Instructions for agents working on this repo.

## Architecture in one paragraph

synaplan-opencloud is an OpenCloud web extension (Vue 3, built with the
OpenCloud extension-sdk) plus a small Go service that follows the OpenCloud
service conventions (cobra commands, env-tagged config, reva/CS3 for file
bytes). The extension adds Synaplan actions to the file context menu —
summarize, translate, add to / update in / remove from the knowledge base,
save a result into the personal space — and calls the Go service at
`/api/synaplan/*` through OpenCloud's proxy. The Go service authenticates the
OpenCloud user, swaps their Keycloak login for a Synaplan token with RFC 8693
token exchange (or attaches one shared API key in the discouraged Mode B),
reads the file through the CS3 gateway, and calls Synaplan with a client that
is **generated** from the pinned Synaplan image's OpenAPI spec. There is no
database of its own and no state beyond what Synaplan and OpenCloud hold.

## Repository layout

| Path | What lives there |
| ---- | ---------------- |
| `backend/cmd/synaplan/` | Binary entrypoint; `pkg/command/` has the `server`, `health`, `version` commands. Route wiring is in `pkg/command/server.go`. |
| `backend/internal/handler/` | One HTTP handler per file (`me.go`, `translate.go`, `summarize.go`, `knowledge*.go`, `files.go`, `assets.go`, `brandicon.go`) sharing the `Handler` struct in `handler.go`. |
| `backend/internal/synaplanapi/` | The generated Synaplan client. `client.gen.go` and `.cache/openapi.json` are **never committed**; `gen.go` + `scripts/generate-synaplan-client.sh` produce them. |
| `backend/internal/synaplanauth/`, `tokenexchange/`, `cs3reader/` | Request auth middleware and outbound auth editors, the Keycloak token-exchange client, the CS3 file reader. |
| `backend/pkg/config/` | Config struct with `yaml` / `env` / `desc` tags; `defaults/` holds the defaults, `parser/` the env parsing. The docs site is generated from these tags. |
| `frontend/src/` | `index.ts` registers the app; `extensions/` the context-menu actions; `components/` the dialogs; `composables/` shared logic; `views/Synaplan.vue` the in-app page. |
| `frontend/tests/unit/` | Vitest + happy-dom, one spec per source file. |
| `frontend/tests/e2e/`, `frontend/support/` | Playwright journeys, helpers (`authHelper`, `filesHelper`, `contextMenuHelper`, `synaplanApiHelper`), page objects and fixtures. |
| `dev/docker/` | OpenCloud proxy, CSP, web and apps config for the dev stack; Traefik TLS. |
| `docker-compose.yml`, `docker-compose.ci.yml` | The dev stack; the CI overlay switches it to the pre-built image and the Synaplan test-stack ports. |
| `.github/workflows/ci.yml` | The gate (below). `docs.yml` builds the service reference site. `renovate.json5` keeps every pin current. |

## Prerequisites — the local stack

The dev stack needs the [Synaplan dev stack](https://github.com/metadist/synaplan)
running with `docker compose --profile oidc up -d`. This provides Keycloak
(shared OIDC provider), the Synaplan backend, and the `synaplan-opencloud`
Keycloak confidential client used for token exchange.

| Port | Owner |
| ---- | ----- |
| `9200` | OpenCloud with the **bundled** extension (`frontend/dist` mounted at `/web/apps/synaplan`) |
| `9201` | OpenCloud **web dev server** (checkout of [opencloud-eu/web](https://github.com/opencloud-eu/web)) — loads the extension from `pnpm serve` |
| `9106` | The Go service (air hot-reload inside the `synaplan-backend` container; Go edits apply without a rebuild) |
| `8000` / `5173` | Synaplan API / UI (dev stack) |
| `8443` / `8080` | Keycloak issuer (https) / admin + token endpoint (http) |
| `8001`, `8444` / `8081` | The same in Synaplan's **test stack**, which CI uses (`docker-compose.ci.yml`) |

- On Linux / WSL2 add `127.0.0.1 host.docker.internal` to `/etc/hosts` once;
  accept Keycloak's self-signed certificate once at
  `https://host.docker.internal:8443`.
- Synaplan's `backend/.env` must carry the realm values
  (`OIDC_CLIENT_ID=synaplan-app`, `OIDC_CLIENT_SECRET=test-oidc-secret`,
  `OIDC_DISCOVERY_URL=http://host.docker.internal:8080/realms/synaplan`) and
  at least one AI provider key (`GROQ_API_KEY`, `OPENAI_API_KEY`,
  `ANTHROPIC_API_KEY`, `GOOGLE_GEMINI_API_KEY`, `XAI_API_KEY`), then
  `docker compose up -d backend worker scheduler` there. Without the OIDC
  values every call answers 401; without a key summarize and translate fail.
- Test accounts come from Synaplan's `_docker/keycloak/setup.sh`:
  `testuser` / `testpass123` (`testuser@synaplan.com`, used by every E2E
  spec), `admin` / `admin`, `demo` / `demo`. The Keycloak clients are
  `opencloud` (public), `synaplan-app` (confidential, `test-oidc-secret`) and
  `synaplan-opencloud` (confidential, token exchange). Changes to the realm
  belong in the `synaplan` repo, not here.
- Ready when: `curl -ks https://host.docker.internal:9200/status.php` says
  `installed`, `curl -s -o /dev/null -w '%{http_code}' http://localhost:9106/api/synaplan/me`
  answers `401`, and `testuser` sees the Synaplan submenu on a file.

Host tools for the gate: Go (the toolchain named in `backend/go.mod`),
[golangci-lint v2](https://golangci-lint.run/docs/welcome/install/) on `PATH`
(CI uses the action's current v2 — install the same major with its
`install.sh`), `pnpm` (the version in `package.json#packageManager`; pnpm 10+
switches itself), Docker (the client generator runs the pinned Synaplan
image), and Playwright's Chromium (`cd frontend && pnpm exec playwright
install --with-deps chromium`).

## Frontend dev workflow

**DO NOT run `pnpm build` (or `make frontend-build`) during development.** In
dev, the OpenCloud web UI is served by the opencloud-eu/web vite dev server
running on port **9201**, which connects to our OpenCloud instance on 9200. Our
extension is loaded via the extension-sdk's auto-registration from `pnpm serve`
in this repo — assume the user keeps both running. OpenCloud on `:9200` serves
whatever is in `frontend/dist`, so a stale build there competes with the dev
server's copy of the same app id.

- Production build (`pnpm build`) is only for CI, the Docker image, and the
  bundled flow on `:9200` (`make frontend-build && make docker-up`).
- `make ci-local` builds into `frontend/dist-ci/` for exactly this reason: the
  gate proves the bundle compiles without touching what OpenCloud serves.
- Every user-facing string goes through `$gettext` (vue3-gettext); strings live
  in `frontend/l10n/translations.json`.
- Every element an E2E spec touches carries a `data-testid`
  (`synaplan-<dialog>-<element>`, e.g. `synaplan-summarize-submit`); the
  context-menu triggers keep OpenCloud's `.oc-files-actions-<action>-trigger`
  class. Change a test id only together with its spec.
- `<script setup lang="ts">`, OpenCloud design-system components (`oc-*`),
  composables in `src/composables/`, one Vitest spec per source file in
  `tests/unit/` (stub `web-pkg` the way the existing specs do). Parse the Go
  service's responses with zod instead of casting.
- ESLint warnings are not a CI failure today; a PR must not add any.

## Backend dev workflow

- Self-contained `go.mod` under `backend/`. The root Makefile delegates via
  `backend-*` targets; `make -C backend …` works too. `build`, `test`, `lint`
  all depend on `generate`, so you normally never call it yourself.
- `make -C backend generate` dumps the OpenAPI spec from the Synaplan image
  pinned in `.github/workflows/ci.yml` (`SYNAPLAN_IMAGE_DIGEST`) and runs
  oapi-codegen. To iterate against a locally modified Synaplan:
  `SYNAPLAN_SRC_CONTAINER=synaplan-backend make -C backend generate`. Never
  hand-edit `client.gen.go`; never commit it. An endpoint missing from the
  client means the pin is too old — bump it (below), don't write the call by
  hand.
- Handlers stay thin and read top-down: decode and validate → CS3 read →
  Synaplan call → `writeJSON`. Validation runs before any I/O so unit tests can
  exercise it against a bare `Handler{}`; everything past validation is
  covered by E2E. Per-call timeouts go on the request context (LLM calls take
  minutes; `/me` uses the incoming context).
- Errors carry context: `fmt.Errorf("creating synaplan API client: %w", err)`.
  Never log or echo `SYNAPLAN_API_KEY`, exchanged tokens or `OC_JWT_SECRET`.
- Env var conventions: service-specific vars use the bare service name,
  `SYNAPLAN_HTTP_ADDR`, `SYNAPLAN_URL`, `SYNAPLAN_OIDC_*`; cross-service vars
  use `OC_`: `OC_REVA_GATEWAY`, `OC_JWT_SECRET`, `OC_INSECURE`; where both
  make sense list both, `env:"OC_LOG_LEVEL;SYNAPLAN_LOG_LEVEL"`. Never
  introduce an `OC_SYNAPLAN_*` prefix — no other OpenCloud service does that.
- A new config field is a struct field with `yaml`, `env` **and** `desc` tags,
  a default in `pkg/config/defaults/`, and a row in the README tables. The
  reference site (`make docs`, `.github/workflows/docs.yml`) is generated from
  the `desc` tags — a field without one ships undocumented.

### Go formatting, linting and documentation

- `gofmt` is the format; `make backend-format` applies it, and
  `golangci-lint run` fails on any diff (`backend/.golangci.yml`, formatters
  section). The linters are `errcheck`, `govet`, `staticcheck`, `unused`,
  `ineffassign`; widen that list only in its own PR.
- Every package has a package comment; every exported identifier has a doc
  comment that starts with its name. Unexported handlers, middleware and
  helpers get a comment too when the *why* is not obvious from the code — the
  existing files set the tone (comments explain the OpenCloud or Synaplan
  constraint behind a decision, not what the next line does).
- Tests are table-driven, use `net/http/httptest`, and never reach the network
  or Docker. A fake upstream is an `httptest.Server` (see `brandicon_test.go`).
  No sleeps, no shared mutable state between cases.

## The gate — all of it, before every push

`main` is protected. Nothing merges on a red check, and nothing is pushed
that was not green locally first. The CI run is ~15 minutes; a miss costs a
full cycle.

| Local step | CI job it mirrors |
| ---------- | ----------------- |
| `make ci-local` | **Frontend** (`pnpm install --frozen-lockfile`, `lint`, `format:check`, `check:types`, `test:unit`, `build`) and **Backend** (`go generate`, `go test ./...`, `golangci-lint run`) |
| `make frontend-build && CI=1 make frontend-test-e2e` (bundled `:9200` — the build refreshes `frontend/dist`, which OpenCloud serves; `ci-local` does not touch it) or `CI=1 make frontend-test-e2e-dev` (web dev server `:9201`, no build) | **E2E** (Playwright against the Synaplan test stack and the built image) |
| `make frontend-build && docker build .` (after `ci-local` generated the client) | **Docker Build** — when the `Dockerfile`, a compose file, or the build inputs changed |
| `make docs` | **Docs** — when `config.go` tags, the README or `.github/docs/` changed |

Rules:

- `make ci-local` first, always. A filtered run (`go test ./internal/handler`,
  `vitest <file>`, `playwright test tests/e2e/foo.spec.ts`) is diagnosis, not
  the gate — finish with the unfiltered targets.
- E2E is **required** before pushing anything a person can see or that
  touches a route, the proxy config, auth, or the Synaplan client. A
  backend-only refactor with no HTTP contract change may skip it. A selector,
  translation or route miss will not show up in Vitest.
- Run E2E the way CI does: `CI=1` (one worker, one retry, trace on retry).
  OpenCloud 8.1.0 crashes under parallel Playwright load
  (`fatal error: concurrent map read and map write`) and the container has
  no restart policy — bring it back with `docker compose up -d` and wait for
  `status.php`.
- Locally the suite runs against **real AI providers** (the keys in
  Synaplan's `.env`); in CI it runs against Synaplan's test stack with the
  Ollama stub and Tika. A spec must pass in both: assert on the terminal
  state (the result box is visible and non-empty, the success banner names
  the group), never on model wording, never on timing.
- Read the Go service log before blaming the extension:
  `docker compose logs synaplan-backend`. `summarize: summary/generate status
  500 … invalid x-api-key` — `translate:` uses the same Synaplan endpoint —
  or any other provider `authentication_error` means the
  model Synaplan has as default for `testuser` belongs to a provider whose
  key in `../synaplan/backend/.env` is missing or invalid — fix the key or
  pick a model with a working key in Synaplan's AI settings, restart its
  `backend worker`, and rerun. The knowledge journeys only need embeddings
  and Tika, so they stay green while summarize and translate fail that way.
- Never `--no-verify`, never "fix" a flaky test by raising a timeout without
  a timeline from the trace (`playwright-report/`, uploaded as an artifact
  when CI fails).
- After a Synaplan pin bump, run the whole gate: a regenerated client is a
  compile-time contract check, and the E2E suite is the runtime one.

## E2E with Playwright — how the journeys are built

The four specs in `frontend/tests/e2e/` are the pattern: `synaplan.spec.ts`
(in-app view, `/me`), and `summarize`, `translation`, `knowledge`, each with a
context-menu visibility check plus a text-file and a PDF (Tika) journey.
Every journey crosses every seam — OpenCloud UI → proxy → Go service →
Keycloak token exchange → Synaplan → (Tika, model) → back into the dialog —
so a green run is the proof that the integration works, not that a component
renders. Shipped flows without a journey yet (knowledge **update** and
**remove**, save-to-space, the failure paths) are the first specs to add; a
sprint that touches one of them adds its journey in the same PR.

- **Fixtures by API, not by UI.** `getAccessToken` (Keycloak password grant,
  `scope=openid`), `getPersonalSpace`, `ensureFolder`, `uploadTextFile`,
  `uploadFileFromDisk` seed files over Graph + WebDAV in `filesHelper.ts`;
  binary fixtures live in `support/filesForUpload/`. Driving the upload UI is
  slow and couples the spec to file-browser markup.
- **Isolation.** All test files go under `synaplan-e2e/` in the personal
  space, names carry `Date.now()`, `afterEach` deletes them
  (`deleteFileQuiet`) and logs out. State that lives in Synaplan (the
  `OC_E2E_TEST` knowledge group) is wiped in `beforeEach` through
  `synaplanApiHelper` with a `synaplan-app` token, and the spec runs in
  `serial` mode because workers would race the wipe.
- **Environment knobs.** `BASE_URL_OC` (UI under test: `:9200` or `:9201`),
  `OC_API_URL` (Graph/WebDAV for fixtures, stays `:9200` in dev mode),
  `KEYCLOAK_HTTPS_URL`, `SYNAPLAN_API_URL` — defaults are the dev stack, CI
  overrides them in `ci.yml`. Never hardcode a port inside a spec.
- **Timeouts are explicit and justified.** `test.setTimeout(60_000)` for a
  model round-trip, `toBeVisible({ timeout: 30_000 })` on the result. No
  `waitForTimeout`. When a future feature streams a result (SSE or a
  progress state), wait for the terminal element (`…-result`, `…-success`,
  `…-error`) and assert that intermediate states did not leave a stuck
  spinner — never assert on individual tokens.
- **Every user-visible PR adds or extends a journey**: the new menu entry is
  visible for the signed-in user; the happy path reaches its terminal state
  and the result is where the user will look for it; the failure path shows
  one readable sentence; cleanup leaves no file or group behind. Put the
  journey's name in the PR body next to the local run that walked it.
- Add helpers to `support/helpers/` when two specs need the same step; keep
  page objects (`support/pages/`) for OpenCloud's own screens only.
- A spec that needs a new Synaplan capability also needs the Synaplan pin
  that has it — the test stack in CI runs exactly `SYNAPLAN_REF`.

## Pinned Synaplan and other dependencies

- Every GitHub Action is pinned by commit SHA with the version as a comment;
  every Docker image by digest; Go and npm dependencies by exact version.
  Renovate (`renovate.json5`) raises the PRs and groups them; each one still
  goes through the full gate and a review. Do not loosen a pin by hand.
- `SYNAPLAN_REF` (git SHA, the compose files the E2E job clones) and
  `SYNAPLAN_IMAGE_DIGEST` (the image the client is generated from and the test
  stack runs) in `ci.yml` are **one pair** and the single source of truth —
  `generate-synaplan-client.sh` greps the digest out of that file. Bump them
  together, in a `chore: bump Synaplan to …` PR, and say what changed in the
  API. Renovate opens the paired bump as the `Synaplan` group.
- OpenCloud is pinned in `docker-compose.yml` and `frontend/package.json`
  (`@opencloud-eu/*` all on the same version). A bump is its own PR with the
  E2E run on the new version; `README.md` states the minimum version.

## Documentation is part of the change

- README: configuration tables, Make targets and the quick start stay true
  with every PR that changes them.
- Go: package and exported-identifier comments (above); `desc` tags on
  config. The docs workflow renders a `site-preview` artifact on every PR —
  open it when you touched config.
- Frontend: a composable or extension gets a header comment with the
  OpenCloud concept it plugs into (see `useSynaplanBird.ts`).
- PR body: what changed, why, how to see it (the journey and the local run),
  and `Fixes #NNN` when there is an issue.

## Commits, branches, pull requests

- Conventional commits (`feat:`, `fix:`, `docs:`, `chore:`, `refactor:`,
  `test:`, `ci:`). Code, comments and commit messages are English.
- No co-author or tool attribution of any kind.
- Semantic branch names (`feat/…`, `fix/…`, `docs/…`, `chore/…`), branched
  from current `main`, one topic per branch and PR.
- Main branch is protected — all changes go through PRs. The `All Checks
  Passed` gate must be green before merge. The ruleset `BaseRules` on the
  default branch enforces: no deletion, no force-push, a pull request with
  one approving review, and the required status check `All Checks Passed`.
  The bypass for organisation and repository admins exists for emergencies;
  it is not the workflow.
- Rebase on `main` before asking for review; the status check is not
  "strict", so a stale branch can be green and still break `main` after a
  merge.
- Squash-merge unless every commit on the branch stands on its own; the PR
  title becomes the commit subject, so it is a conventional commit too.
- Merge conflicts: merge both sides by hand, never `git checkout --ours` /
  `--theirs` on code, and ask when unsure.
- A `v*` tag on a green `main` commit publishes
  `ghcr.io/metadist/synaplan-opencloud:<version>`; `latest` follows `main`.

### The flow — one topic at a time

Every change, by a person or an agent, goes through these steps in this
order. None is optional and none is done in parallel with the next topic.

1. **Branch** off current `main`: `feat/…`, `fix/…`, `docs/…`, `chore/…`.
   One topic per branch; the topic is one sentence you could use as the
   commit subject.
2. **Implement** that topic only. Run `make ci-local`, and the E2E journey
   when the change is user-visible (see "The gate"). Never push red.
3. **Commit and push** the branch. Conventional commit, English, no
   attribution.
4. **Open the pull request** against `main`. Title = the conventional commit
   subject. Body = what changed, why, how to see it (the journey and the
   local run you did), and `Fixes #NNN` when there is an issue. Findings
   that are not part of the change (a broken local key, a flaky stack) go
   into the body too, under their own heading, so the reviewer knows what
   was *not* fixed.
5. **Wait for the Copilot review and for `All Checks Passed`.** Address
   them professionally: every review thread — Copilot's or a person's —
   gets a reply that is either the hash of the fixing commit or one sentence
   on why it does not apply. Resolve a thread only after the code or the
   text matches the reply. Push the fixes to the same branch; never open a
   second PR for them.
6. **Make CI green on the same branch.** Reproduce a red check locally
   first (`make ci-local`; the failing spec with its trace from the
   `playwright-report` artifact; the backend logs in the job output), name
   the cause, fix it, re-run the local step that covers the diff, push, and
   wait again. Never retry a timing failure blind, never raise a timeout
   without the timeline, never re-run a job as the fix — unless every E2E
   job died at `Unable to download artifact`, which is Actions infra.
7. **Ready** means: `All Checks Passed` green on the current head and every
   thread answered. Say so in the PR. The owner merges; do not merge your
   own PR and do not start the next topic before this one is ready.

## Tool usage

- Use `pnpm` (not `npx`, not `npm`) to invoke frontend tools.
- Prefer the Makefile targets over raw `pnpm`/`go` commands when possible.
- Formatting: `make format` formats both frontend (prettier) and backend
  (gofmt).
- The `backend/` dir has its own Makefile (`build`, `test`, `lint`, `format`,
  `generate`) — the root Makefile delegates to it.

## Secrets

- Never commit `.env` files, API keys (`sk_…`), tokens, or certificates. The
  compose values `synaplan-dev-secret`, `synaplan-opencloud-secret` and
  `test-oidc-secret` are development-only and must not appear in any
  production config or documentation example as real values.
- Synaplan's provider keys live in `../synaplan/backend/.env`. Use them for
  local runs; never print, paste or log one — not in a PR, a trace, a
  screenshot or a chat.
- The shared-API-key mode (`SYNAPLAN_API_KEY`) is documented as discouraged;
  keep the startup warning and never add a code path that logs the key.

## Boundaries

### Ask first before

- Adding or upgrading a dependency (`pnpm`, `go get`, a GitHub Action, a
  Docker image) outside a Renovate PR.
- Changing `ci.yml`, `docs.yml`, `renovate.json5`, the `Dockerfile`, or a
  compose file.
- Adding a public route to the Go service, changing an auth mode, or changing
  what the proxy config exposes without a Bearer check.
- Bumping the OpenCloud version or the Synaplan pin by hand.
- Widening the golangci-lint set or the ESLint config.

### Never do

- Commit or push to `main`; force-push any branch with an open PR.
- Commit `client.gen.go`, `.cache/openapi.json`, `frontend/dist*`,
  `node_modules/`, `backend/bin/`, or anything from `.github/docs/.cache/`.
- Merge, or ask to merge, with a red or skipped check.
- Hand-edit generated code, or hand-write a Synaplan API call the generated
  client does not have.
- Hardcode a host or port in shipped code; the dev defaults belong in the
  compose files and `README.md`.
