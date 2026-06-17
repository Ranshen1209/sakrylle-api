# AGENTS.md

Sakrylle API fork of sub2api. Maintenance notes — operational facts, gotchas, and "why" decisions only. For "what changed", use `git log` / `git diff upstream/main`.

## Repository & Infrastructure

- **Upstream**: [Wei-Shaw/sub2api](https://github.com/Wei-Shaw/sub2api)
- **Fork**: [Ranshen1209/sub2api](https://github.com/Ranshen1209/sub2api), branch `theme/monet-purple`
- **Production**: `sub.sakrylle.com` (app), `api.sakrylle.com` (API), `doc.sakrylle.com` (docs), `status.sakrylle.com` (monitor)
- **Server**: `cliproxyapi-jp` (64.83.47.108, SSH alias `ssh-tokyo`)
- **Architecture**: `Public 443 → sslh → 127.0.0.1:8443 → Nginx → upstream container`

Compose stack at `/opt/stack/docker-compose.yml`:
- `sub2api` (service name) — `ghcr.io/ranshen1209/sakrylle-api:purple` (repo renamed sub2api→sakrylle-api; GHA pushes here)
- `sub2api-postgres` (PG 18), `sub2api-redis` (Redis 8), `relay-pulse`, `smtp-relay` (unused, can cleanup)

Configs at `/opt/stack/`: `sub2api/.env`, `nginx/conf.d/*.conf`, `relay-pulse/config/`, `secrets/cloudflare.ini`, `certs/live/sakrylle.com/`

Admin: `admin@sub2api.local`, password in `.env` as `ADMIN_PASSWORD` (bootstrap only; restarts don't reseed).

## Customizations Overview

Visual rebrand + backend behavior changes. No core gateway logic touched.

- **Theme**: Monet purple `#9181bd` (was teal `#14b8a6`). Search `frontend/tailwind.config.js` + grep `frontend/src/` when extending.
- **Backend**:
  - `/v1/models` never falls back to `claude.DefaultModels` / `openai.DefaultModels` (prevents leaking unconfigured models).
  - SMTP nil-auth allowed in `email_service.go`.
  - Turnstile widget follows site's dark mode, not OS.
- **Currency**: see [Currency policy](#currency-policy).

## Currency Policy

UI renders `￥` everywhere. **Display-only — numeric values NOT converted.** `users.balance` of `8.94` was `$8.94`, now `￥8.94`; DB unchanged.

**Why**: Avoids FX-conversion bugs (price drift, audit-log off-by-ones, recharge pipeline double conversion). Tradeoff: internal columns keep USD names (`balance`, `quota_used`, `daily_limit_usd`).

**NOT changed**: `/v1/models` pricing (USD standard), `balance_recharge_multiplier` (1.0), DB column names, `channel_model_pricing` rows (upstream USD rates).

**Regression watch**: `grep -rEn '￥\{[a-zA-Z_]'` — `￥{<identifier>` is likely mangled JS interpolation (real i18n: `￥{usd}`).

## Build & Deploy

Push to `theme/monet-purple` → GHA → `ghcr.io/ranshen1209/sakrylle-api:purple`:

```bash
git push origin theme/monet-purple
ssh ssh-tokyo 'docker pull ghcr.io/ranshen1209/sakrylle-api:purple && cd /opt/stack && docker compose up -d sub2api'
curl -sS https://sub.sakrylle.com/health
```

Local preview: `JWT_SECRET` ≥32B (64 hex), `TOTP_ENCRYPTION_KEY` exactly 32B (64 hex), `POSTGRES_PASSWORD` set. Access `http://localhost:18080`.

## Companion Services

| Domain | Purpose | Source | Notes |
|---|---|---|---|
| `api.sakrylle.com` | Nginx reverse proxy to `/v1/*` | `nginx/conf.d/sakrylle-api.conf` | Separates API from web UI |
| `doc.sakrylle.com` | VitePress docs (zh/en) | [Ranshen1209/sakrylle-docs](https://github.com/Ranshen1209/sakrylle-docs) (private) | GHA deploy on push to `main`. **Nginx gotcha**: `try_files` MUST include `$uri.html` before fallback for `cleanUrls: true` |
| `status.sakrylle.com` | relay-pulse fork | [Ranshen1209/relay-pulse](https://github.com/Ranshen1209/relay-pulse) `theme/sakrylle` | 7 probes @ 3m cadence, ~$0.51/mo. Config hot-reloads |
| `sakrylle.com` / `www` | 301 redirect | `nginx/conf.d/sakrylle-redirect.conf` | → `https://sub.sakrylle.com/` |

**doc.sakrylle.com locked policies** (do not revert):
- Refund: 3 days (not 7), SLA: best-effort, no age limit, reselling needs written permission.
- `codex-auto-review` alias docs: now `gpt-5.4` (was `gpt-5.4-mini`, changed 2026-06-01).
- Group naming: `Claude-Kiro` / `GPT-Pro` / `GPT-Plus` (capital P).

## Email & OAuth

**SMTP**: Resend (`smtp.resend.com:465`) since 2026-05-24. DB `settings`: `smtp_host`, `smtp_port=465`, `smtp_username=resend`, `smtp_password=<re_...>`, `smtp_from=support@sakrylle.com`, `smtp_use_tls=true`. **Update via direct SQL** (admin PUT resets host/port), then restart.

DNS: SPF/DKIM/DMARC on `sakrylle.com` + `send.sakrylle.com`. Outlook/Gmail/163/iCloud deliver. **QQ still delayed** (new domain reputation); workaround: QQ email warning modal (DB key `qq_email_warning_enabled`).

**OAuth provider** (Sakrylle issues tokens): endpoints at `/oauth/*`, clients in `oauth_clients` table. Settings: `oauth_provider_enabled`, `oauth_default_group_id=5` (GPT-Image). **Embedded-frontend gotcha**: `backend/internal/web/embed_on.go` SPA fallback MUST bypass `/oauth/` or routes get intercepted.

**Sakrylle Studio OIDC client** (registered in production 2026-06-16): `settings.oauth_issuer=https://oidc1.sakrylle.com`; client `sakrylle-studio`, public desktop, PKCE required, `default_group_id=3`, redirect allowlist `http://127.0.0.1/callback` + `http://localhost/callback`. Loopback matching permits random ports (`http://127.0.0.1:<port>/callback`) but the path must be exactly `/callback` (not `/oauth/callback`). Allowed/default scopes: `openid profile email models:read responses:create messages:create usage:read offline_access`. Missing client symptom: `/oauth/authorize` 400 page with `oauth client not found`; restart `sub2api` after DB-only client changes.

**GitHub OAuth login** (Sakrylle is client): settings keys `github_oauth_enabled`, `github_oauth_client_id`, `github_oauth_client_secret`, redirect URLs. Manage via direct SQL.

**Password reset**: requires `frontend_url=https://sub.sakrylle.com` in settings. Missing → 500.

## Upstream Channels & Groups

6 channels serving 11 groups. All accounts are `apikey` type. **All channels have `restrict_models=true`** (since 2026-06-01) — only `channel_model_pricing.models` pass; else 503. Toggle: `UPDATE channels SET restrict_models=<bool> WHERE id=...` + restart.

**Topology** (channel 12 added & verified end-to-end 2026-06-10; rest verified 2026-06-01):

| Channel | billing_model_source | Groups | Key notes |
|---|---|---|---|
| 7 Claude | channel_mapped | 2 Kiro (0.4x) / 7 Code (0.6x) / 8 Special (0.25x) | Empty `model_mapping` |
| 12 Claude Max | channel_mapped | 16 Claude-Max (2.0x, `claude_code_only`) / 17 Claude-Max-C (2.2x) | `claude-fable-5` only; accts 120/121 `GreenMountain-Claude-Max[-C]`, empty `model_mapping`. Pricing row: input `1e-5` / output `5e-5` / cache-write `1.25e-5` / cache-read `1e-6` (official rates, ×multiplier bills). fable-5 = adaptive-thinking only (no manual `budget_tokens`). |
| 8 Deepseek | channel_mapped | 6 Deepseek (0.7x) / 9 Official (1.0x) | `platform='anthropic'` on both |
| 9 OpenAI GPT | channel_mapped | 3 Pro (0.4x) / 4 Plus (0.25x) / 10 Plus-Special (0.2x) | `codex-auto-review→gpt-5.4` alias |
| 10 GPT-Image-2-4K | requested | 11 GPT-Image-2-4K (1.0x) | `gpt-image-2-4k→gpt-image-2-vip`, ￥0.18/call |
| 11 GPT-Image-2 | channel_mapped | 5 GPT-Image (1.0x) | $0.10/call, needs `allow_image_generation=true` |

**Pricing semantics**: `channel_model_pricing` rows are **per-token USD at official upstream rates**. Final bill: `tokens × price × groups.rate_multiplier`. Change multiplier, NOT pricing rows (they're audit/reconciliation baseline).

**Account-level `model_mapping`** lives in `accounts.credentials.model_mapping` (jsonb), NOT `accounts.extra`. Empty/absent = passes all models.

### Critical Gotchas

#### 1. `codex-auto-review` two-gate rule (footgun, hit 2026-06-01)

**Remapped mini→gpt-5.4 on 2026-06-01** (~3.3× cost). Three config pieces on channel 9:
1. **Pricing visibility**: `codex-auto-review` in gpt-5.4 row's `models` array.
2. **Channel mapping**: `channels.model_mapping = {"openai":{"codex-auto-review":"gpt-5.4"}}` (rewrites upstream request).
3. **Account mapping**: accts 111/112 must contain `"codex-auto-review":"gpt-5.4"` key.

**Why #3 needed**: account selection runs BEFORE channel mapping rewrites. Account w/ non-empty mapping but missing key → filtered out → 503. **GPT-Plus-Special (group 10) is 503 canary** — only account 111 (explicit mapping, no empty fallback). Drop `codex-auto-review` key from 111 → instant 503.

Drop channel mapping → coderelay 400. Drop pricing alias → `/v1/models` hides it.

#### 2. GPT-Image-2-4K uses `billing_model_source='requested'`

**Why not `channel_mapped`**: image billing path bills FIRST candidate only (not full sweep). With `channel_mapped`, first candidate = mapped `gpt-image-2-vip` → pricing row miss → $0 white-use. `requested` keys on client name `gpt-image-2-4k`, so:
- Pricing row `["gpt-image-2-4k"]` bills correctly.
- `model_mapping` still rewrites upstream to `gpt-image-2-vip`.
- `/v1/models` exposes only `gpt-image-2-4k`.

#### 3. Deepseek 运维陷阱 (已踩 3 个)

1. **新建 channel/group 后必须重启 sub2api** — cache miss → legacy `CalculateCost` fallback → `pricing not found` → **白用**. `docker compose restart sub2api`.

2. **API key 改 user_id/group_id 后发 Redis invalidate** — 否则 cache 持有旧值 60s:
   ```bash
   docker exec sub2api-redis redis-cli PUBLISH auth:cache:invalidate '<full-key-string>'
   ```

3. **`/v1/models` 按 `group.platform` 路由** — Deepseek groups 必须 `platform='anthropic'`. 写错成 `openai` → pricing lookup miss → 空数组（call 仍成功但用户看不到模型）.

**协议**: 两组都 `platform='anthropic'`。网关双向 transform OpenAI↔Anthropic，所以两种入口都能用。选 Anthropic 是因为 `thinking` block 是一等公民（OpenAI compat 压成 `reasoning_content` 字符串）。

**计费**: pricing 行存上游**促销价** (v4-pro 2.5 折)。促销结束必须刷新，否则亏本卖。

#### 4. GPT-Plus-Special (group 10) — single account, no fallback

History: `ai.centos.hk` → 95 OAuth accounts (2026-05-31) → **single apikey account 111 `GPT-Plus-Special-Coderelay`** (2026-06-01).

**Channel vs account independent**: pricing/routing on `channels.id=9`, credentials on bound `accounts`. Never delete channel 9 pricing rows — makes all GPT groups bill $0 and empties `/v1/models`.

**503 canary**: unlike Pro (fallback acct 2) / Plus (fallback acct 3), group 10 only has explicit-mapping acct 111. Missing key → 503. **Fix**: bind empty-mapping fallback account.

`gpt-5.3-codex` removed 2026-06-01 (OpenAI retired).

## Async Image Bridge (group 21 / channel 13 / account 1131 → 12ai)

Fixes the gpt-image-2 **524** (upstream sync render >100s hits its Cloudflare origin timeout). For accounts flagged async, the gateway submits to **12ai's async task API** (`cdn.12ai.org` `POST /v1/task/submit` → poll `GET /v1/task/{id}` → download `outputs[]` → return `b64_json`), so no hop stays open >100s. Client protocol unchanged. Code: `backend/internal/service/openai_images_async.go` + `openai_images_usage_synth.go`; branch in `openai_images.go` `forwardOpenAIImagesAPIKey` on `account.IsAsyncImage()`.

**Billing**: the async API returns **no token usage**, so the gateway **synthesizes** an `OpenAIUsage` from `(size, quality, reference images, prompt)` and feeds the existing **token** billing path. Config lives in **`accounts.credentials`** (NOT channel features_config — the account, not the channel, is threaded into `ForwardImages`):

```jsonc
// account 1131 credentials
"async_enabled": "true",               // STRING "true" (GetCredential ignores JSON bools)
"async_base_url": "https://cdn.12ai.org",
"api_key": "<async-task-permission key>",   // submit+poll both use this
"async_image_host_suffix": "12ai.org", // SSRF allowlist for downloaded image URLs
"async_image_synth": {
  "output_token_table": { "1K": {"low":196,"medium":1756,"high":7023},
                          "2K": {"low":397,"medium":3571,"high":14281},
                          "4K": {"low":367,"medium":3299,"high":13195} },
  "ref_image_tokens":   { "1K":1024, "2K":1521, "4K":1508 },
  "image_input_ratio":  1.6            // = image-input ¥12.8 ÷ text-input ¥8; default 1.6 if absent
}
```

**Pricing — to change rates, NO code change needed**:
- Text-input ¥8 / output ¥48 → `channel_model_pricing` (channel 13, `billing_mode=token`, input `8e-6` / output `48e-6`). Synthesized image-output tokens go in `OutputTokens` (billed at output_price); leave `image_output_price=0`.
- Image-input ¥12.8 has **no dedicated pricing field**, so it's billed via `image_input_ratio` (ref tokens × ratio into `InputTokens` at ¥8 ⇒ effective ¥12.8). If upstream changes ¥8 or ¥12.8, update `image_input_ratio = image_input_price ÷ input_price` in account creds (default 1.6).
- Margin → group 21 `rate_multiplier` (1.5). Final = synth tokens × channel price × group multiplier.

**Quality**: `auto`/missing → forced to `medium` (upstream submit + billing). Clients request model **`gpt-image-2-async`** — channel 13 `model_mapping {gpt-image-2-async→gpt-image-2}` rewrites it to `gpt-image-2` for the 12ai upstream, and **`billing_model_source=requested`** bills on the client name `gpt-image-2-async` (matches the `["gpt-image-2-async"]` pricing row). Do NOT use `channel_mapped` here — it bills on the mapped `gpt-image-2`, missing the row (white-use) and colliding with channel 11's per-image `gpt-image-2` in the model plaza (the distinct name also fixes that plaza display). `output_token_table` calibration: run async generations, read net charge from the 12ai dashboard (`net ÷ ¥48/1M = effective output tokens`). Missing table cell → conservative row-max + warn; `OutputTokens==0` (table gap) → **fail-closed**, image not delivered. **DB-only changes (pricing/mapping/billing_source/credentials) need `docker compose restart sub2api` — `docker compose up -d` is a no-op when the image is unchanged, so the cache won't reload (symptom: 503 "channel pricing restriction blocked").** **Model-plaza display**: the plaza resolves pricing **per-group** (from the channel serving that group), so same-name models across channels no longer collide. To show the 图片输入 rate (= `input_price × image_input_ratio`, e.g. ¥12.8/1M base), the plaza reads a **display copy** of `image_input_ratio` from channel 13's `features_config` (the `/channels/available` endpoint is channel-scoped and can't see account creds). Keep `channels.features_config.image_input_ratio` in sync with the account's billing `async_image_synth.image_input_ratio`.

## Notification Email Templates

24 templates (12 events × 2 locales) in `settings` table, key `notification_email_template:<event>:<locale>`. Design: Monet purple header, inline-styled, dark mode support. Generator: `/tmp/sakrylle_email_templates.py` (one-off).

## Admin & DNS Ops

**Admin password rotation**: bcrypt 72B limit. **Assert hash non-empty + starts `$2b$` before SQL**. Script in original doc line 337-349. `.env` `ADMIN_PASSWORD` bootstrap-only.

**Cloudflare DNS**: token at `/opt/stack/secrets/cloudflare.ini`, zone `cf223b3e0b2adcd4876cea779b041a1c`. All subdomains A → `64.83.47.108`, DNS-only (not proxied).

## Syncing Upstream

```bash
git fetch upstream && git checkout theme/monet-purple && git rebase upstream/main
```

Likely conflicts: `frontend/tailwind.config.js`, `frontend/src/views/HomeView.vue`, `backend/internal/service/setting_service.go`.

**Verified conflict set (2026-06-08 merge of v0.1.135, 156 commits)** — the recurring ones to expect:
- `backend/internal/pkg/openai/constants.go` — keep our removal of unprovided models (gpt-5.2, gpt-5.3-codex-spark, gpt-image-1/1.5); adopt any new upstream model.
- `frontend/src/views/user/UsageView.vue` — keep `￥` symbol (Currency policy) + upstream's `?? 0` nil guards.
- `backend/internal/service/wire.go` + `backend/cmd/server/wire.go` — keep our OIDC/OAuth providers; merge both shutdown steps.

### Wire / OIDC scheduler footgun (hit 2026-06-08)

**`wire_gen.go` is committed and CI/Docker do NOT regenerate it** — it must merge correctly or the image build ships wrong wiring. After resolving the hand-written `wire.go` conflicts, **regenerate rather than hand-merge** the generated files:

```bash
brew install go   # need 1.26.x to match backend/go.mod
go install github.com/google/wire/cmd/wire@v0.7.0
cd backend && GOFLAGS=-mod=mod wire ./cmd/server/   # then: go build ./... && go test ./...
```

**The trap**: `ProvideOIDCKeyRotationScheduler` calls `Start()` on construction (spawns 2 goroutines). Wire only instantiates a provider if something *consumes* it — its only consumer is `provideCleanup` (which holds it to call `Stop()`). The OIDC scheduler dependency lives in the **hand-written `cmd/server/wire.go` `provideCleanup` signature**. If a merge drops that param (or it was ever a drifted manual edit in `wire_gen.go`), `wire` regenerates without the scheduler → **OIDC key rotation never starts** and the build still passes silently. Keep `oidcKeyRotation *service.OIDCKeyRotationScheduler` in `provideCleanup` and verify it appears in regenerated `wire_gen.go`.

### codex-auto-review pricing (canonical = our values, not upstream's)

`backend/resources/model-pricing/model_prices_and_context_window.json` + `pricing_service_test.go` carry conflicting `codex-auto-review` rates. **Ours are canonical**: input `2.5e-6`, output `1.5e-5`, cache-read `2.5e-7` (see commit `5655a6d7`; the synced entry's own `_flex`/`_batches` tiers confirm these). Upstream periodically doubles them via bulk re-sync — revert both the JSON entry and the test assertions to our values.

### Merged interfaces break our test stubs

Upstream + our OIDC work extend repo interfaces (e.g. `DeleteWithAudit`, `ListClientsWithBackchannelLogout`). Production code builds, but our test stubs across `service`/`handler`/`middleware` need the new methods added or `go test` fails to compile. Add them mirroring the sibling stub methods.

## Common Ops

```bash
# Status
ssh ssh-tokyo 'cd /opt/stack && docker compose ps sub2api sub2api-postgres sub2api-redis'

# Logs
ssh ssh-tokyo 'cd /opt/stack && docker compose logs --tail=50 sub2api'

# Backup
ssh ssh-tokyo 'docker exec sub2api-postgres pg_dump -U sub2api sub2api > /opt/stack/backups/sub2api-db-$(date +%F).sql'

# Restart after config change
ssh ssh-tokyo 'cd /opt/stack && docker compose restart sub2api'

# Pull latest image
ssh ssh-tokyo 'docker pull ghcr.io/ranshen1209/sakrylle-api:purple && cd /opt/stack && docker compose up -d sub2api'
```


## Sakrylle Docs Governance

- `sakrylle-docs/` is tracked in this repository and is the **canonical source of truth** for Sakrylle API/OIDC/RP integration, product planning, program management, and brand-system docs.
- Current center-doc layout:
  - `sakrylle-docs/00-overview/` — executive summary and repository inventory.
  - `sakrylle-docs/10-platform-identity/` — OAuth/OIDC current state, architecture, RP integration guide, commercial boundaries, configuration isolation.
  - `sakrylle-docs/20-products/` — product-specific research/plans for CLI, Studio, Web, Chat, Image.
  - `sakrylle-docs/30-program-management/` — roadmap, risk register, decision log, implementation checklist.
  - `sakrylle-docs/40-brand-system/` — design/brand system.
- Root-level numbered docs in `sakrylle-docs/` are compatibility stubs only. Update the canonical files under the structured directories, not the stubs.
- Product repositories' `oidc-docs/` directories are local notes only. They must link back here instead of duplicating platform protocols, claims boundaries, roadmap, risk register, or design system docs.
- When changing OAuth/OIDC behavior, scopes, claims, client registration, configuration isolation, branding rules, or product rollout status, update `sakrylle-docs/` in the same change or explicitly state why docs were not changed.
