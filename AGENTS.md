# AGENTS.md

## Project Overview

Sub2API is an AI API gateway platform for distributing subscription quotas, issuing API keys, billing usage, routing upstream accounts, and operating an admin dashboard.

Primary stack:

- Backend: Go, Gin, Ent ORM, PostgreSQL, Redis.
- Frontend: Vue 3, Vite, TypeScript, TailwindCSS, Pinia, pnpm.
- Deployment: Docker Compose, systemd install scripts, release binaries.

## Common Commands

- Backend dev: `cd backend && go run ./cmd/server`
- Backend generate: `cd backend && go generate ./ent && go generate ./cmd/server`
- Backend unit tests: `cd backend && go test -tags=unit ./...`
- Backend integration tests: `cd backend && go test -tags=integration ./...`
- Backend lint: `cd backend && golangci-lint run ./...`
- Frontend dev: `cd frontend && pnpm dev`
- Frontend build: `cd frontend && pnpm build`
- Frontend lint/typecheck: `cd frontend && pnpm lint:check && pnpm typecheck`
- Root build: `make build`
- Root test: `make test`

Version note: `DEV_GUIDE.md` documents CI expectations around Go 1.25.7, while `backend/go.mod` currently declares Go 1.26.4. Verify the intended Go version before changing CI, Docker, or release configuration.

## Repository Map

- `backend/cmd/server/`: backend application entry.
- `backend/ent/`: Ent generated code and schema.
- `backend/internal/`: handlers, services, repositories, gateway logic, configuration, and server wiring.
- `backend/migrations/`: database migration scripts when present.
- `frontend/src/`: Vue UI, API clients, stores, components, views, i18n, and types.
- `deploy/`: Docker Compose files, example configuration, and install scripts.
- `docs/`: product, API, payment, compliance, and project documentation.

## High-Risk Areas

- Billing, balances, token accounting, payment callbacks, Stripe, Alipay, WeChat Pay, and EasyPay.
- Authentication, JWT, TOTP encryption, admin/user permissions, API key issuance, and key revocation.
- Upstream account OAuth/API-key storage, scheduling, sticky sessions, concurrency limits, and rate limits.
- Gateway request forwarding, response streaming, model mapping, failover, and usage recording.
- Ent schema, migrations, repository contracts, and generated code.
- Security controls such as CORS, CSP, URL allowlists, trusted proxies, response header filtering, and secret handling.
- Deployment scripts, Docker Compose files, release config, systemd service behavior, and environment templates.
- Frontend admin settings, payment flows, account batch operations, and i18n-visible user workflows.

## Project Workflow

- Use `$t-workflow` when a task needs batch grouping, complex parallelism, high-risk or release gates, cross-session recovery, durable audit records, workflow setup/migration, inbox, or optional organization handoff.
- Ordinary single-repository bugfixes, features, docs updates, and focused reviews may run in the current session with a native task plan when useful.
- Do not copy global `$t-workflow` routing algorithms, templates, or skill prose into project docs. Project docs should keep only project facts, commands, constraints, storage location, and stricter local safety rules.
- Current workflow storage mode: `local`.
- Current workflow root: `.codex/workflow/`.
- Workflow record schema: `record_schema_version: 2`.
- Local workflow records are recovery/audit artifacts and should remain out of product Git unless the user explicitly switches this project to shared records.

## Language And Comments

- Respond to the user in Chinese by default.
- New code comments should use Chinese unless a file or public API convention clearly requires English.
- Do not translate existing comments just to normalize language.
- Keep identifiers, package names, database names, API paths, framework terms, and third-party names in their established style.
- Comments should explain intent, constraints, tradeoffs, and non-obvious behavior.

## Safety And Verification

- Do not modify secrets, credentials, environment files, deployment settings, access policies, payment settings, or production data unless explicitly requested.
- Do not introduce a new production dependency without explaining why and asking for approval.
- Prefer small, reversible diffs and preserve unrelated user changes.
- After code changes, check Git status and report changed files, checks run, results, risks, and follow-up recommendations.
