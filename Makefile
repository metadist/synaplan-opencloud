.PHONY: frontend-install frontend-build frontend-serve frontend-lint frontend-format-check frontend-format frontend-typecheck frontend-test-unit frontend-test-e2e frontend-test-e2e-dev backend-build backend-dev backend-test backend-lint backend-format format ci-local docker-up docker-down docs docs-serve-prod docs-clean

# Frontend
frontend-install:
	cd frontend && pnpm install

frontend-build:
	cd frontend && pnpm build

frontend-serve:
	cd frontend && pnpm serve

frontend-lint:
	cd frontend && pnpm lint

frontend-format-check:
	cd frontend && pnpm format:check

frontend-format:
	cd frontend && pnpm format:write

frontend-typecheck:
	cd frontend && pnpm check:types

frontend-test-unit:
	cd frontend && pnpm test:unit --watch=false

frontend-test-e2e:
	cd frontend && pnpm test:e2e

frontend-test-e2e-dev:
	cd frontend && pnpm test:e2e:dev

# Backend
backend-build:
	make -C backend build

backend-dev:
	cd backend && air

backend-test:
	make -C backend test

backend-lint:
	make -C backend lint

backend-format:
	make -C backend format

# All
format:
	make frontend-format
	make backend-format

# Local mirror of the `frontend` and `backend` jobs in
# .github/workflows/ci.yml — same steps, same order, same flags. Green here
# means those two jobs are green. It does NOT run the E2E job: run
# `make frontend-test-e2e` (bundled build on :9200) or
# `make frontend-test-e2e-dev` (web dev server on :9201) against the local
# stack before pushing. See AGENTS.md, "The gate".
#
# The production bundle is written to frontend/dist-ci/ (gitignored) instead
# of frontend/dist/, so running the gate never swaps the extension that the
# running OpenCloud container — or the :9201 web dev server — is serving.
# OPENCLOUD_EXTENSION_DIST_DIR is honoured by @opencloud-eu/extension-sdk.
#
# `make -C backend test lint` in one invocation runs `generate` once for both.
ci-local:
	cd frontend && pnpm install --frozen-lockfile
	make frontend-lint
	make frontend-format-check
	make frontend-typecheck
	make frontend-test-unit
	cd frontend && OPENCLOUD_EXTENSION_DIST_DIR=dist-ci pnpm build
	make -C backend test lint

# Docker
docker-up:
	docker compose up -d

docker-down:
	docker compose down

# Docs
# Full pipeline via dschmidt/opencloud-service-docs-action, pinned by SHA
# in .github/workflows/docs.yml. run.sh parses that SHA, clones the action
# at it, and runs its build.sh — identical code path to CI.
docs:
	DOCS_OUTPUT="$(CURDIR)/docs/generated" bash .github/docs/run.sh

# Serve the built site — matches Docusaurus's own post-build hint
# (`[INFO] Use 'npm run serve' command to test your build locally`).
docs-serve-prod:
	cd .github/docs/.cache/site && pnpm run serve

# Nuke scratch + built site.
docs-clean:
	rm -rf .github/docs/.cache .github/docs/docs docs/generated
