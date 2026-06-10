# Async Image Bridge — Design Spec

**Date:** 2026-06-10
**Status:** Approved design, pending implementation plan
**Scope:** Sakrylle API gateway (fork of Wei-Shaw/sub2api), `backend/`

## 1. Problem

Users of the GPT-Image-2 image API (e.g. Infinite-Canvas, running on each user's own
machine) hit `Server disconnected without sending a response` on `/v1/images/edits`.

Root cause (verified from production logs): image generation/edit on the upstream
provider (`right.codes` and `12ai.org`, both `new-api` instances) is **synchronous** and
takes ~85–130 s. The upstream sits behind Cloudflare, whose free-plan **origin timeout is
100 s** → upstream returns **HTTP 524** once a render exceeds ~100 s. Our gateway then
fails over, but the image group has a single account → `no available accounts` → **502**
after ~126 s, surfaced to the client as a transport disconnect.

Latency cliff (measured): edits ≤98 s succeed; ≥~94 s trend to failure. The render time is
inherent to the model; swapping sync upstreams does not help (12ai sync edit measured at
94.5 s — same speed class).

## 2. Goal

Eliminate the 524 for GPT-Image-2 by routing the slow render **off the synchronous
connection** using `12ai.org`'s asynchronous task API, while keeping the client protocol
unchanged (still OpenAI-compatible sync `/v1/images/generations` and `/v1/images/edits`).

Verified live: `12ai.org` async task API (`https://cdn.12ai.org`) supports `gpt-image-2`
generation **and** edits, returns a task id in ~0.9 s, and a 112 s edit completed without
524 because no single HTTP hop is held >100 s.

## 3. Locked Decisions

| # | Decision |
|---|----------|
| Scope | Build the full feature at once: async bridge + request/response translation + size×quality billing + error handling. |
| Quality buckets | `low` / `medium` / `high`. `auto` or missing → rewritten to **`medium`** at the gateway (injected into both the upstream submit and the billing), removing the "auto silently renders high → undercharge" risk. |
| Billing approach (**A**) | Bridge **synthesizes** an `OpenAIUsage` from `(size, quality, reference images, prompt)` and feeds the **existing token-billing path**. Rates `¥8/1M` input, `¥48/1M` output, × group margin multiplier. Edits add image-input tokens. (Validated: `output_tokens × ¥48/1M (+ input × ¥8/1M)` reproduces the upstream net charge exactly — see §7.) |
| Image delivery | Gateway downloads the `img.12ai.org` URL and returns **`b64_json`** to the client (mirrors current sync behavior, hides upstream CDN, no dependence on 12ai image persistence). |
| Max wait | Poll up to **240 s**, then 504. `failed` / timeout / client-disconnect / partial-unfulfilled → **no billing**. Only delivered images are billed. |
| Margin | Group 21 `rate_multiplier` = **1.5** (+50% over upstream cost). |
| Architecture | **Dedicated async module** (isolated, unit-testable units). No new wire dependency. |

## 4. Architecture

New files under `backend/internal/service/`:

```
openai_images_async.go         # ForwardImagesAsync + AsyncImageClient (submit/poll/fetch)
openai_images_usage_synth.go   # size×quality → token table + Synthesize() (pure)
```

Branch point — in `forwardOpenAIImagesAPIKey`, immediately before the synchronous upstream
call at `service/openai_images.go:608` (`s.httpUpstream.Do(...)`):

```go
if account.IsAsyncImage() {            // reads credentials.async_enabled
    return s.ForwardImagesAsync(ctx, c, account, parsed, requestModel, startTime)
}
// else: existing synchronous path (unchanged)
```

Three independently testable units:

- **`AsyncImageClient`** — submit / poll / fetch only; inputs (base, key, body) → task
  status / image URL. Mockable over HTTP.
- **`UsageSynthesizer`** — pure `(size, quality, refImages, prompt, n) → OpenAIUsage`.
- **`RequestTranslator`** — OpenAI images request (JSON gen / multipart edit) → 12ai task
  submit body (reference images → base64 data-URI); `outputs[]` → `b64_json` OpenAI
  response.

Invariants: client protocol unchanged; billing still flows through the existing
`RecordUsage`; the bridge only produces an `OpenAIForwardResult` carrying a synthesized
`Usage`. The client↔gateway hop has no Cloudflare (our nginx 600 s / gateway 300 s), so a
240 s poll is fine and **no hop exceeds 100 s → no 524**.

### Data flow

```
client /v1/images/edits (multipart, sync, unchanged)
 → handler.Images (unchanged)
 → ForwardImages → forwardOpenAIImagesAPIKey
 → [branch] ForwardImagesAsync:
     1. normalize quality: auto/missing → medium
     2. translate → 12ai task: POST {async_base}/v1/task/submit
          input{prompt, size, quality, n, images:[refs as base64 data-URI], mask}
     3. poll GET /v1/task/{id} (3 s interval, ctx-cancelable, ≤240 s)
     4. completed → download outputs[] → encode b64_json
     5. synthesize OpenAIUsage(size,quality,refImages,prompt,n) → OpenAIForwardResult
 → return result (sync exit unchanged)
 → handler RecordUsage → token billing × group margin multiplier
```

## 5. Configuration

**① Account `credentials` (JSONB)** — async switch + endpoint:

```json
{
  "api_key": "sk-...",                     // key with async task permission
  "async_enabled": true,
  "async_base_url": "https://cdn.12ai.org",
  "base_url": "https://new.12ai.org/v1",   // sync endpoint kept (e.g. /v1/models)
  "poll_interval_ms": 3000,                // optional, default 3000
  "max_wait_ms": 240000                    // optional, default 240000
}
```

New helper `account.IsAsyncImage()` reads `async_enabled`.

**② Channel `channel_model_pricing` (existing, reused)** — rates:

```
billing_mode = token
input_price  = 8e-6      (¥8 /1M)
output_price = 48e-6     (¥48 /1M)
```

Synthesized image output tokens go into `OutputTokens` (`ImageOutputTokens = 0`), billed at
`output_price`.

**③ Channel `features_config` (JSONB, new key)** — token table for synthesis:

```json
{
  "async_image_synth": {
    "output_token_table": {
      "1K": {"low": 196, "medium": 1756, "high": 7023},
      "2K": {"low": "TBD", "medium": "TBD", "high": "TBD"},
      "4K": {"low": "TBD", "medium": "TBD", "high": "TBD"}
    },
    "ref_image_tokens": {"1K": 1024, "2K": "TBD", "4K": "TBD"}
  }
}
```

**④ Group `rate_multiplier`** — margin. Final bill = synthesized tokens × rate ×
multiplier. **Decided: `1.5`** (+50% over upstream cost) on group 21.

## 6. Usage Synthesis Algorithm

`UsageSynthesizer` is pure: `(size, quality, refImages, prompt, n) → OpenAIUsage`.

```
q    = normalizeQuality(quality)          # auto/"" → medium ; also injected into submit
tier = NormalizeImageBillingTier(size)    # 1K/2K/4K (reuse existing)

outPer = output_token_table[tier][q]
if outPer == 0: outPer = max(output_token_table[tier]); warn   # missing cell → highest in row (never undercharge)
outputTokens = outPer * n

textIn = estimateTextTokens(prompt)       # ceil(len/4); negligible at ¥8/1M

refTok = sum( ref_image_tokens[ NormalizeImageBillingTier(img.WxH) ] for img in refImages )
imageInAdjusted = round(refTok * 1.6)     # 1.6 = 12.8/8, compensates for no image-input price field

return OpenAIUsage{
    InputTokens:       textIn + imageInAdjusted,
    OutputTokens:      outputTokens,       # billed at output_price (¥48)
    ImageOutputTokens: 0,
    // cache fields 0
}
```

Then existing billing computes `InputTokens×8e-6 + OutputTokens×48e-6`, × group
`rate_multiplier`.

Edge cases:
- `n>1` → multiply output tokens; `partial_completed` → `n = delivered count`.
- Missing `(tier,quality)` cell → highest in that size row + warn (conservative).
- Reference image dimensions read via `image.DecodeConfig` on each multipart image.
- `×1.6` is a small correction (~¥0.005 per single-ref edit); displayed edit `InputTokens`
  is inflated 1.6× on the image portion only (cosmetic; ¥ is correct). May be dropped if a
  cleaner image-input price path is added later.

## 7. Billing Validation (measured)

1024×1024 sync usage (ground truth):

| quality | output_tokens | `output×¥48/1M (+in×¥8)` | dashboard net |
|---------|---------------|--------------------------|---------------|
| low     | 196           | ¥0.0095                  | —             |
| medium  | 1756          | ¥0.0843                  | ¥0.084 ✓      |
| high    | 7023          | ¥0.3371                  | ¥0.337 ✓      |

Confirms: real upstream billing is **pure token × rate**; the upstream UI's "尺寸倍率
(1.5/1.1)" is informational only — quality is fully captured in the token count. Synthesis
reproduces upstream cost exactly.

## 8. Lifecycle / Error Handling

```
submit ─▶ poll loop (3s, ≤240s) ─▶ fetch ─▶ b64 ─▶ bill
  │           │                      │
  └ non-200/  ├ failed   → error     └ download fail → error
    no id      ├ timeout → 504        (all: NO billing)
    → error    ├ disconnect → stop poll
    (no bill)  └ partial → deliver successful only
```

- **submit**: 30 s timeout; failure → OpenAI-format error, no billing. **No submit retry**
  (avoid double-charge / double-image).
- **poll**: GET every 3 s, 20 s per-poll timeout; `queued/in_progress` continue;
  `completed` fetch; `failed` return upstream error, no billing; `partial_completed`
  deliver successful, `n = success count`. Transient poll error (network/5xx) does not abort
  the task — only abort after N consecutive poll failures.
- **240 s timeout** → 504 + clear message, no billing. (Upstream's ¥2 hold refunds on its own
  completion; unrelated to our end user.)
- **client disconnect** (use request ctx, not detached) → stop polling, no billing. 12ai task
  continues upstream; we stop waiting.
- **billing only after image bytes delivered**: set `result.Usage = synthesized`,
  `ImageCount = delivered`, then existing `RecordUsage`. Error paths return a typed error that
  the handler maps to an error response **bypassing RecordUsage** (verify handler error branch
  during implementation).
- **concurrency**: one poll goroutine per in-flight request; submit obeys
  `account.Concurrency`; cheap-GET polls not subject to the heavy limit.
- **no new wire dependency**: new code is methods on `OpenAIGatewayService` + pure helpers.

## 9. Testing (TDD — tests first)

- **Pure unit**: `UsageSynthesizer` table-driven over 9 cells, n>1, edits with 1/many refs,
  missing-cell fallback, auto→medium, ×1.6. `RequestTranslator` golden tests (gen JSON,
  multipart edit with base64 + mask, outputs→b64).
- **`AsyncImageClient`** with `httptest`: submit success/fail; poll
  queued→in_progress→completed; failed; partial; transient-poll-error retry; 240 s timeout;
  ctx-cancel stops polling.
- **Billing integration**: synthesized result through `RecordUsage`, assert
  `cost = tokens × rate × multiplier` (1K high, margin 1.5 → ¥0.337×1.5 = ¥0.506); assert
  failed/timeout → zero billing.
- **Regression**: `async_enabled=false` leaves the sync image path unchanged.
- **Acceptance**: `go build ./... && go test ./...`; live 12ai end-to-end already verified.

## 10. Open Items (resolved during implementation)

- **2K / 4K token table calibration**: high-quality large sizes 524 on sync (no usage), so
  calibrate via async run + 12ai dashboard net charge (net ÷ ¥48/1M = effective output
  tokens), and `ref_image_tokens` for 2K/4K inputs.
- **Operational** (not code): the 12ai account holds ¥2 per async task until completion —
  upstream balance must cover concurrency; confirm failed tasks also refund the hold.
- Post-deploy: restart `sub2api` + redis cache invalidate per CLAUDE.md gotcha #3 after
  channel/pricing/group changes.
