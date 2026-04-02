.PHONY: frontend-install frontend-build frontend-dev frontend-lint frontend-format-check frontend-format frontend-typecheck frontend-test-unit frontend-test-e2e backend-build backend-dev backend-test backend-lint backend-format format docker-up docker-down

# Frontend
frontend-install:
	cd frontend && pnpm install

frontend-build:
	cd frontend && pnpm build

frontend-dev:
	cd frontend && pnpm build:w

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
