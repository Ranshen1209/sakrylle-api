# Sakrylle API

Sakrylle API is a maintained fork of `Wei-Shaw/sub2api` used by Sakrylle for API gateway, billing, OAuth/OIDC, and the bundled management panel.

This repository keeps the Go backend, Vue frontend, deployment assets, and local operator tooling in one tree. Long-form product and protocol notes are intentionally not duplicated here; keep the README focused on build, development, and repository navigation.

## Repository Layout

| Path | Purpose |
| --- | --- |
| `backend/` | Go API server, gateway handlers, Ent schema/migrations, OAuth/OIDC provider code, tests, and generated wire files. |
| `frontend/` | Vue 3 + Vite management panel. |
| `backend/internal/web/dist/` | Embedded frontend build output placeholder/asset target for the backend. |
| `deploy/` | Docker Compose examples, service files, deployment scripts, and environment template. |
| `nginx/` | Nginx configuration assets. |
| `assets/` | Static project assets. |
| `scripts/` | Project scripts. |
| `tools/` | Maintenance tools, including secret scanning. |
| `.claude/skills/` | Local Codex/Claude skills used while operating this fork. |

## Requirements

- Go `1.26.x`
- Node.js with `pnpm`
- Docker / Docker Compose for containerized local runs
- PostgreSQL and Redis when running the backend outside Compose

## Common Commands

Build everything:

```bash
make build
```

Run backend checks:

```bash
make test-backend
```

Run frontend checks:

```bash
make test-frontend
```

Run the secret scanner:

```bash
make secret-scan
```

Build only the backend:

```bash
make -C backend build
```

Build only the frontend:

```bash
pnpm --dir frontend install
pnpm --dir frontend run build
```

## Local Development

Start from the deployment environment template:

```bash
cp deploy/.env.example .env
```

For local preview, make sure these values are set:

- `JWT_SECRET`: at least 32 bytes
- `TOTP_ENCRYPTION_KEY`: exactly 32 bytes
- `POSTGRES_PASSWORD`: non-empty

Compose examples live under `deploy/`:

```bash
docker compose -f deploy/docker-compose.local.yml up -d
```

For frontend-only work:

```bash
pnpm --dir frontend install
pnpm --dir frontend run dev
```

## Backend Notes

- `backend/cmd/server/wire_gen.go` is committed and should be regenerated after changing Wire providers.
- Use `go install github.com/google/wire/cmd/wire@v0.7.0`, then run Wire from `backend/` when provider wiring changes.
- Ent-generated code and migrations live in `backend/ent/` and `backend/migrations/`.

## Deployment Notes

The production image is published as:

```text
ghcr.io/ranshen1209/sakrylle-api:purple
```

Deployment should be image-based. Keep secrets, database state, uploaded data, and host-specific Nginx/Compose overrides outside the repository.

## Safety Rules

- Do not commit secrets, `.env` files, database files, uploaded user data, or local dependency directories.
- Do not delete `node_modules`, virtual environments, database volumes, or runtime user data as part of cache cleanup.
- Prefer changing group/channel pricing and operational settings through reviewed migrations or explicit SQL runbooks.
