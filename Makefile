.PHONY: frontend-install frontend-build frontend-serve frontend-lint frontend-format-check frontend-format frontend-typecheck frontend-test-unit frontend-test-e2e frontend-test-e2e-dev backend-build backend-dev backend-test backend-lint backend-format format docker-up docker-down docs docs-serve-prod docs-clean

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
