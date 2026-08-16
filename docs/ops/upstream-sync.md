# Upstream Sync

## Basic Flow

```bash
git fetch upstream
git checkout theme/monet-purple
git merge --no-ff upstream/main
```

`theme/monet-purple` remains the upstream integration branch. After validation,
merge it into `main`; only successful builds of the reviewed `main` commit may
be deployed to production.

This long-lived fork must use a merge by default. Do not rebase it unless explicitly requested: replaying hundreds of local commits repeats equivalent conflicts and consumes excessive time and review tokens. Resolve each conflict once in the merge commit, preserving both new upstream behavior and the Sakrylle constraints below.

Likely conflicts:

- `frontend/tailwind.config.js`
- `frontend/src/views/HomeView.vue`
- `backend/internal/service/setting_service.go`

Verified recurring conflict set from the 2026-06-08 merge of v0.1.135:

- `backend/internal/pkg/openai/constants.go` — keep Sakrylle removal of unprovided models such as `gpt-5.2`, `gpt-5.3-codex-spark`, `gpt-image-1/1.5`; adopt genuinely new upstream models.
- `frontend/src/views/user/UsageView.vue` — keep `￥` symbol and upstream `?? 0` nil guards.
- `backend/internal/service/wire.go` and `backend/cmd/server/wire.go` — keep Sakrylle OIDC/OAuth providers and merge both shutdown steps.

v0.1.176 also conflicted generated Ent Group files (`group.go`, `mutation.go`, `migrate/schema.go`, `runtime/runtime.go`) because Sakrylle `image_only` and upstream `long_context_pricing_enabled` / `model_pricing` both shift field indices. Resolve schema first, then `go generate ./ent` from `backend/`; do not take `--theirs` on `mutation.go` (it drops Sakrylle OAuth entity types).

v0.1.177 conflicted `backend/internal/handler/openai_gateway_handler.go`: keep Sakrylle `allowsOpenAICompatibleMessagesDispatch` (OpenAI + Grok groups always dispatch) and take upstream `openAIResponsesRequiredCapabilityForRequest` for native compaction v2. Do not add unused `allowOpenAICompatibleMessagesDispatch`. Groups usage cells must stay `￥`; the new yesterday-cost row arrived with `$`. Filenames `222_channel_time_pricing.sql` and `222_group_usage_daily_rollups.sql` can coexist because migrations are keyed by filename. When `backend/go.mod` bumps a patch (for example 1.26.5 → 1.26.6), also bump `Dockerfile`, `backend/Dockerfile`, and `deploy/Dockerfile`; official `golang:*` images set `GOTOOLCHAIN=local` and will not auto-download.

## Wire / OIDC Scheduler Footgun

`wire_gen.go` is committed. CI and Docker builds do not regenerate it.

After changing hand-written Wire providers, regenerate generated files instead of hand-merging them:

```bash
brew install go
go install github.com/google/wire/cmd/wire@v0.7.0
cd backend
GOFLAGS=-mod=mod wire ./cmd/server/
go build ./...
go test ./...
```

The trap: `ProvideOIDCKeyRotationScheduler` calls `Start()` on construction and spawns 2 goroutines. Wire instantiates a provider only if something consumes it.

The only consumer is `provideCleanup`, which holds the scheduler so it can call `Stop()`. Keep this parameter in `backend/cmd/server/wire.go`:

```go
oidcKeyRotation *service.OIDCKeyRotationScheduler
```

Verify regenerated `wire_gen.go` still includes the scheduler. Otherwise OIDC key rotation silently never starts while the build still passes.

## codex-auto-review Pricing

Canonical Sakrylle rates:

- input `2.5e-6`
- output `1.5e-5`
- cache-read `2.5e-7`

Files:

- `backend/resources/model-pricing/model_prices_and_context_window.json`
- `pricing_service_test.go`

Upstream periodically doubles these through bulk re-sync. Revert both JSON entry and test assertions to Sakrylle values. Commit `5655a6d7` is the local reference point; synced `_flex` and `_batches` tiers also confirm these values.

## Test Stubs

Upstream plus Sakrylle OIDC work can extend repository interfaces, for example `DeleteWithAudit` or `ListClientsWithBackchannelLogout`.

Production code may build while tests fail because service/handler/middleware stubs lack new methods. Add missing methods mirroring sibling stub implementations.
