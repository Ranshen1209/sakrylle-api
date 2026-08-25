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

v0.1.178 adds upstream recurring daily `time_pricing` JSON beside Sakrylle's versioned `time_versions`. Keep both storage and API formats for compatibility, but reject a pricing row that enables both systems so multipliers cannot be applied twice. A nil `time_versions` update preserves existing versions unless it enables a recurring schedule, in which case the repository clears old versions transactionally; an explicit empty list also clears them. Use distinct frontend i18n keys for the recurring editor. Extend `allowsOpenAICompatibleMessagesDispatch` to Kimi, Zhipu, and DeepSeek while retaining the OpenAI/Grok default dispatch behavior, and do not reintroduce the singular wrapper. Regenerate `wire_gen.go` and verify both the CN balance checker and OIDC key rotation scheduler are consumed by cleanup.

v0.1.179 changes long-context billing activation from requiring both the group and account switches to allowing either switch. Before production deployment, explicitly review the existing group settings and preserve the intended Sakrylle billing policy. The release also adds channel fast/flex and context-range multipliers; keep `channel_model_pricing` as the upstream baseline and continue applying the Sakrylle margin through `groups.rate_multiplier`.

v0.1.180 was merged from the exact annotated release tag rather than the later `upstream/main` head. Keep upstream's multi-interval model-plaza pricing, plugin manager, and model-list response limit together with Sakrylle's versioned time pricing, `￥` display, image-input ratios, and model-specific pricing resolution. The user model-plaza cards must select absolute prices from `pricing.intervals`; the removed `long_context_*_multiplier` fields are not a compatibility source. Go moved to 1.27.0, so update every Docker builder and run golangci-lint v2.13 or later. Regenerate Wire and confirm plugin manager, CN balance checking, and OIDC key rotation all remain cleanup consumers. The 2026-08-24 production audit found long-context pricing enabled on every group; account-level OpenAI overrides remained 5 enabled and 1142 disabled, with Anthropic overrides unset, so no production setting migration was required.

v0.1.181 is compatibility fixes only: Gemini tool-schema sanitization, Grok official CLI User-Agent, Responses Lite `parallel_tool_calls` retention, and batch `status` stripping on rejected Responses input items. The annotated tag still said `0.1.180`; merge `upstream/main` so the follow-up VERSION sync to `0.1.181` is included. The only conflict was `backend/cmd/server/VERSION` — take upstream. Wire, Ent, frontend, and billing policy did not change. Align Sakrylle's Grok image-download path with `defaultGrokUpstreamUserAgent()`; leave the OAuth client `sub2api-grok-oauth/1.0` identity alone.

v0.1.182 fixes contradictory Anthropic cache-creation detail totals by capping the
5m/1h breakdown at the positive aggregate before pricing. Keep that normalization
inside the upstream baseline calculation; Sakrylle's margin still applies separately
through `groups.rate_multiplier`. The release also tightens Responses Lite tool-call
mode and numeric precision across HTTP and WebSocket paths, preserves image prompts
verbatim for OAuth image generation, routes Kimi Code K3 and Antigravity Sonnet 4.6,
resolves monitor facts to the concrete account platform, and refreshes the displayed
balance after payment fulfillment. Merge `upstream/main`, not only the annotated tag,
because the following commit synchronizes `VERSION` to `0.1.182`. In the recurring
`openai_images.go` conflict, retain both Sakrylle's Grok2API/XAI image-edit adapters
and upstream's verbatim-prompt instruction. No Ent or Wire regeneration is required
for this release.

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
