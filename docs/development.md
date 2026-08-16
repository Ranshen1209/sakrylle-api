# Development Notes

This document replaces the old root `DEV_GUIDE.md`, which contained outdated fork names, Go versions, PostgreSQL versions, and CI details.

## Current Stack

| Area | Current value |
| --- | --- |
| Upstream | `Wei-Shaw/sub2api` |
| Production branch | `main` |
| Integration branch | `theme/monet-purple` |
| Backend | Go `1.26.6`, Gin, Ent |
| Frontend | Vue 3, Vite, pnpm |
| Runtime services | PostgreSQL 18, Redis 8 |
| CI image alias | `ghcr.io/ranshen1209/sakrylle-api:purple` |

## Local Requirements

- Go `1.26.x`
- pnpm 9
- Node.js compatible with the frontend toolchain
- Docker / Docker Compose for local service orchestration
- PostgreSQL and Redis if running outside Compose

Local preview needs:

- `JWT_SECRET` at least 32 bytes
- `TOTP_ENCRYPTION_KEY` exactly 32 bytes
- `POSTGRES_PASSWORD` non-empty

## Common Commands

```bash
# Build backend and frontend
make build

# Backend tests
make test-backend

# Frontend lint, typecheck, critical vitest
make test-frontend

# Frontend security audit exception check
make security-audit

# Backend only
make -C backend build

# Frontend only
pnpm --dir frontend install
pnpm --dir frontend run build
```

`make secret-scan` is kept only as a compatibility alias and currently runs `security-audit`.

## Backend Notes

- `backend/cmd/server/wire_gen.go` is committed; regenerate it after Wire provider changes.
- Ent generated code is committed; run `go generate ./ent` from `backend/` after schema changes.
- Interface changes often require updating test stubs across service, handler, middleware, and repository tests.
- Backend Makefile test target runs `go test ./...` and `golangci-lint run ./...`.

## Frontend Notes

- Use pnpm, not npm.
- Commit `frontend/pnpm-lock.yaml` when dependency graph changes.
- If `node_modules` was created by another package manager, remove it before `pnpm install`.
- Critical frontend tests are listed in the root `Makefile` under `FRONTEND_CRITICAL_VITEST`.

## Shell And SQL Gotchas

- Bcrypt hashes contain `$`; quote carefully when writing SQL through shells.
- Prefer psql stdin or SQL files for updates containing bcrypt hashes.
- Use `127.0.0.1` instead of `localhost` when IPv6 resolution causes local PostgreSQL confusion.

## Documentation Gotchas

- Root `CLAUDE.md` should remain a compact index.
- Operational details belong in `docs/ops/`.
- If frontend code imports raw markdown from `docs/legal/*.md`, Docker builds need that subtree present in the build context.
