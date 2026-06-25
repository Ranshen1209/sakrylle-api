# AGENTS.md

Sakrylle API fork of sub2api. This file is a routing index, not a required reading list.

Do not load every referenced document by default. Open the smallest relevant file only when the current task matches its scope.

## Context Routing

| When the task touches... | Read |
| --- | --- |
| production topology, deploy commands, compose, domains, companion services, SSH, common ops | `docs/ops/infrastructure.md` |
| Sakrylle fork behavior, visual theme, model-list fallback, currency display | `docs/ops/customizations.md` |
| SMTP, OAuth/OIDC, clients, scopes, Sakrylle Web SSO, notification email templates | `docs/ops/identity-email.md` |
| groups, channels, pricing, billing source, model mappings, Codex 403, Deepseek, GPT-Plus-Special | `docs/ops/channels-and-billing.md` |
| group 21, channel 13, `gpt-image-2-async`, 12ai async image billing | `docs/ops/async-image-bridge.md` |
| admin password rotation, Cloudflare DNS, `sub.sakrylle.com` China access | `docs/ops/admin-dns.md` |
| upstream sync, rebases, Wire regeneration, recurring merge conflicts, pricing drift, test stubs | `docs/ops/upstream-sync.md` |
| local setup, build/test commands, pnpm, Ent, Wire, dev pitfalls | `docs/development.md` |
| docs organization rules | `docs/ops/README.md` |

## Architecture At A Glance

Monorepo: Go backend (`backend/`), Vue 3 + Vite panel (`frontend/`), deploy assets (`deploy/`), ops docs (`docs/`).

Backend is layered and wired with Google Wire. Provider sets compose in this dependency order (`backend/cmd/server/wire.go`): `config` → `repository` → `service` → `payment` → `middleware` → `handler` → `server`. Entry point is `backend/cmd/server/main.go`; `wire_gen.go` is committed and regenerated via `go generate` after provider changes.

- `internal/handler/` — Gin HTTP handlers (auth/OAuth/OIDC, admin, user, payment, and the `gateway_handler*` request path that proxies `/v1/*` to upstream AI providers).
- `internal/service/` — business logic: billing/pricing, account pooling & scheduling, gateway relay, OAuth token refresh, background workers (most own a `Stop()` consumed by `provideCleanup` in `wire.go`).
- `internal/repository/` — Ent-backed persistence plus Redis caches (e.g. `api_key_cache`); AES encryption for stored credentials.
- `ent/` — generated schema/client; `migrations/` — Atlas migration files. Both committed.
- `internal/server/routes/` — route registration grouped by surface (`gateway.go`, `admin.go`, `auth.go`, `oauth.go`, `payment.go`, `user.go`).

Quick commands (full list in `docs/development.md`): `make build`, `make test-backend`, `make test-frontend`. Backend tests run `go test ./...` + `golangci-lint`; frontend critical specs are pinned in the root `Makefile`.

## Always-On Constraints

- Never drive public 443 edge changes through SSH-over-443; use direct port 22.
- Do not reintroduce default model-list fallback in `/v1/models`.
- Currency is display-only `￥`; do not convert stored numeric values.
- Treat `channel_model_pricing` as upstream baseline pricing; apply margin with `groups.rate_multiplier`.
- Keep OpenAI text/coding groups compatible with Codex tool declarations; see channel docs before changing `allow_image_generation`.
- If Wire providers change, regenerate committed `wire_gen.go` and verify OIDC key rotation is still consumed by cleanup.

For "what changed", use `git log` / `git diff upstream/main`. For operational "why", update the matching doc under `docs/ops/`.
