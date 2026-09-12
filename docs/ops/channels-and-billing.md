# Channels And Billing

## Topology

6 channels serve 11 groups. All accounts are `apikey` type. All channels have `restrict_models=true` since 2026-06-01, so only `channel_model_pricing.models` pass; otherwise requests return 503.

Toggle:

```sql
UPDATE channels SET restrict_models = <bool> WHERE id = ...;
```

Then restart `sub2api`.

| Channel | billing_model_source | Groups | Key notes |
| --- | --- | --- | --- |
| 7 Claude | `channel_mapped` | 2 Kiro (0.4x), 7 Code (0.6x), 8 Special (0.25x) | Empty `model_mapping`. |
| 12 Claude Max | `channel_mapped` | 16 Claude-Max (2.0x, `claude_code_only`), 17 Claude-Max-C (2.2x) | `claude-fable-5` only; accounts 120/121 `GreenMountain-Claude-Max[-C]`; empty `model_mapping`; fable-5 is adaptive-thinking only. |
| 8 Deepseek | `channel_mapped` | 6 Deepseek (0.7x), 9 Official (1.0x) | Both groups use `platform='deepseek'`; account protocol may be OpenAI, Responses, or native Anthropic. |
| 9 OpenAI GPT | `channel_mapped` | 3 Pro (0.4x), 4 Plus (0.25x), 10 Plus-Special (0.2x) | `codex-auto-review -> gpt-5.4` alias. |
| 10 GPT-Image-2-4K | `requested` | 11 GPT-Image-2-4K (1.0x) | `gpt-image-2-4k -> gpt-image-2-vip`, `￥0.18/call`. |
| 11 GPT-Image-2 | `channel_mapped` | 5 GPT-Image (1.0x) | `$0.10/call`, needs `allow_image_generation=true`. |

## Pricing Semantics

`channel_model_pricing` rows keep the repository's per-token numeric unit at the official upstream rate. The Sakrylle UI displays that unit with `￥`; do not convert stored values when applying the DeepSeek card. Final bill:

```text
tokens * price * groups.rate_multiplier
```

Change group multiplier for margin. Do not mutate pricing rows for margin; those rows are audit and reconciliation baselines.

Upstream v0.1.176 added `groups.model_pricing` (per-model overrides) and `groups.long_context_pricing_enabled` (default true). Do not use group `model_pricing` for Sakrylle margin; keep channel rows as the baseline and apply `rate_multiplier`. Leave long-context pricing enabled unless a specific group must ignore official long-context tiers.

Upstream v0.1.177 made Codex OAuth fingerprint convergence opt-in (default off / passthrough). Accounts that never set `codex_fingerprint_mode` stop converging after this upgrade; only explicitly stored `device` / `session` / `full` keep converging. Compaction tests and routing now use native remote compaction v2.

### Versioned peak/off-peak channel pricing

Migration `222_channel_time_pricing.sql` adds scheduled versions to a token price card:

- `channel_model_pricing_versions` stores an effective range, IANA timezone, version base prices, and the multiplier used outside configured windows.
- `channel_pricing_time_windows` stores weekday masks and local minute ranges. Windows are left-closed and right-open: `09:00-12:00` includes 09:00 and excludes 12:00.
- Existing `channel_model_pricing` prices remain active whenever no version is effective, including before the first version and in any configured gap. This makes future price announcements safe to configure before their effective date.
- The handler captures the request start time at ingress; that timestamp selects the version and window. Account selection, retries, long streams, and asynchronous settlement do not change price after crossing a boundary.
- The resolved provider price is still multiplied by `groups.rate_multiplier`; the schedule does not replace the Sakrylle margin layer.

Do not model an upstream peak/off-peak policy with the group-level `peak_rate_*` fields. Those fields are a separate Sakrylle surcharge and would multiply the scheduled channel price again if enabled.

The admin UI is under Channel Management -> Channel Pricing -> Peak / Off-Peak Versions. Account-stats pricing rules and group model overrides intentionally do not expose this editor because their persistence tables do not own channel price-card versions.

From 2026-08-17 Beijing time, the backend automatically applies the official DeepSeek schedule to recognized V4 token cards when no explicit `TimeVersions` or legacy `TimePricing` exists. This applies both to channel price rows and to the built-in official fallback. The schedule needs no database migration, but it does not rewrite an existing `channel_model_pricing` row: the stored row remains the peak baseline and must be audited against the official card before rollout. Otherwise the automatic `0.5` multiplier will correctly halve a stale baseline. Explicit administrator schedules remain authoritative. The built-in rule is:

| Field | Value |
| --- | --- |
| Effective from | `2026-08-17 00:00` |
| Timezone | `Asia/Shanghai` |
| Off-peak multiplier | `0.5` |
| Peak window 1 | Monday-Friday (`weekdays=31`), `09:00-12:00`, `1.0x` |
| Peak window 2 | Monday-Friday (`weekdays=31`), `14:00-18:00`, `1.0x` |
| `deepseek-v4-flash`, `deepseek-v4-flash-0731`, `deepseek-v4-flash-vision-exp` peak prices | cache read `￥0.10`, input/cache miss `￥3.00`, output `￥9.00` per MTok |
| `deepseek-v4-pro`, `deepseek-v4-pro-0813` peak prices | cache read `￥0.30`, input/cache miss `￥9.00`, output `￥27.00` per MTok |

The built-in V4 capability catalog follows the same announcement: 1M context, 384K maximum output, Anthropic/Responses/Chat endpoints, and assistant prefill for Flash, Pro, and Vision.

The Channel Pricing editor uses the same two Monday-Friday windows as the DeepSeek default when an administrator adds an explicit version. The user Available Channels popover and Model Plaza receive both the current resolved price and the schedule. Frontends must display the backend resolution instead of independently deciding whether the current instant is peak.

### September 2026 Flash update

The [current official CNY price card](https://api-docs.deepseek.com/zh-cn/quick_start/pricing/)
was checked on 2026-09-12. Flash peak prices are input/cache miss **￥2**, output
**￥8**, and cache read **￥0.04** per MTok; off-peak is half. `deepseek-flash`
(V4.1 Flash), `deepseek-v4-flash`, and `deepseek-v4-flash-vision-exp` share this
card and native vision capability. The existing dated Flash alias follows the
same compatibility card. The new card supersedes the historical Flash numbers
above; Pro remains input **￥9**, output **￥27**, cache read **￥0.30** at peak.

The September 10 news item announced Pro retirement on September 14, but the
live pricing page now explicitly keeps Pro service and billing unchanged.
Sakrylle therefore does not adopt upstream's automatic Pro-to-Flash billing
switch. Built-in baselines and model metadata use the published CNY numbers
directly, without FX conversion. Explicit group/channel prices and channel time
versions retain precedence. The predeployment database audit found no persisted
DeepSeek channel price rows, so no production price migration was required.

Account-level `model_mapping` lives in `accounts.credentials.model_mapping` (jsonb), not `accounts.extra`. Empty or absent mapping passes all models.

## codex-auto-review Two-Gate Rule

`codex-auto-review` was remapped mini -> `gpt-5.4` on 2026-06-01, roughly 3.3x cost. Channel 9 needs three pieces:

1. Pricing visibility: `codex-auto-review` in the `gpt-5.4` pricing row's `models` array.
2. Channel mapping: `channels.model_mapping = {"openai":{"codex-auto-review":"gpt-5.4"}}`.
3. Account mapping: accounts 111/112 must include `"codex-auto-review":"gpt-5.4"`.

Account selection runs before channel mapping. If an account has non-empty mapping but misses the key, it is filtered out and returns 503. GPT-Plus-Special group 10 is the canary because only account 111 is available there.

Failure modes:

- Drop account mapping key from 111 -> immediate 503.
- Drop channel mapping -> coderelay 400.
- Drop pricing alias -> `/v1/models` hides it.

`gpt-5.3-codex` was removed on 2026-06-01 after OpenAI retired it.

## GPT-Image-2-4K Uses requested Billing

Do not change channel 10 to `channel_mapped`.

The image billing path bills the first candidate only. With `channel_mapped`, the first candidate is mapped `gpt-image-2-vip`, which misses the `gpt-image-2-4k` pricing row and becomes $0 usage. `requested` bills on the client-visible model name:

- pricing row `["gpt-image-2-4k"]` bills correctly
- `model_mapping` still rewrites upstream to `gpt-image-2-vip`
- `/v1/models` exposes only `gpt-image-2-4k`

## Deepseek Ops Traps

1. New channel/group requires `docker compose restart sub2api`, otherwise cache miss can hit legacy `CalculateCost` fallback and become unbilled.
2. After API key `user_id` or `group_id` changes, invalidate Redis auth cache:

   ```bash
   docker exec sub2api-redis redis-cli PUBLISH auth:cache:invalidate '<full-key-string>'
   ```

3. `/v1/models` routes by `group.platform`; Deepseek groups must use `platform='deepseek'`. The account protocol selects the upstream surface (`/v1`, `/responses`, or `/anthropic`), while the group platform selects DeepSeek pricing and scheduling.

Deepseek V4 pricing rows inherit the official schedule automatically when they do not have an explicit schedule. Before deploying this change, inspect channel 8's persisted V4 rows and update input/cache-read/output to the peak values in the table above through Channel Pricing. The application deliberately does not overwrite administrator-owned pricing data. Add a future version when a promotion starts or ends, or when DeepSeek announces a replacement policy, instead of editing the active baseline at the boundary.

`deepseek-v4-flash-vision-exp` converts image dimensions to prompt tokens upstream, so those tokens are billed with the model's normal input/cache card. Chat Completions, Responses, and native Anthropic requests accept their documented URL, data URL/base64, inline file-data, and uploaded `file_id` forms; the gateway converts image blocks, structured tool outputs, and developer/user roles while enforcing each target protocol's allowed roles. Settlement uses the upstream completion usage, including image prompt tokens and cache hit/miss buckets.

Preflight token-count endpoints are not settlement records. For CN/custom relays, `/responses/input_tokens` and an OpenAI-dispatched `/messages/count_tokens` can fall back to the local text estimator. An external image URL or historical `file_id` does not expose its dimensions to that estimator, and DeepSeek does not publish the exact image-token formula as a local SDK API. The eventual billed request remains exact because it uses upstream usage.

The DeepSeek Files API is exposed at `/v1/files` and `/files`. It is optional for URL/data-URL images, but required for uploaded `file_id` references and is the practical path for reuse or payloads beyond the inline image limit. Its inventory is scoped by group and user, and local list/retrieve/delete plus model references fail closed for unknown or cross-tenant IDs instead of exposing a shared upstream API key's aggregate files. Records without `expires_after` remain persistent; expiring uploads are removed after their deadline, and successful DELETE or upstream 404 also clears the binding. The caller's headers select the response family. Fixed accounts keep their configured upstream protocol and base URL, while adaptive accounts follow the incoming OpenAI or Anthropic family; the gateway converts file uploads and objects between families as needed and adds `anthropic-beta: files-api-2025-04-14` on native requests. OAuth-issued clients need `messages:create` or `responses:create` for these file routes. The deployment-level `gateway.deepseek_files` policy defaults each group/user tenant to 1,000 files and 2 GiB, and each shared upstream account to 10,000 files and 25 GiB; tenant ceilings must stay strictly below the corresponding account ceilings. These are local admission limits for tenant isolation and shared-account capacity protection, not discovered provider quotas, and changing them requires a deployment configuration rollout rather than a frontend setting.

## GPT-Plus-Special

Group 10 moved from `ai.centos.hk` to 95 OAuth accounts, then to single API key account 111 `GPT-Plus-Special-Coderelay` on 2026-06-01.

Pricing and routing are on channel 9; credentials are on bound accounts. Never delete channel 9 pricing rows, because that makes all GPT groups bill $0 and empties `/v1/models`.

Unlike Pro and Plus, group 10 has no empty-mapping fallback account. Missing explicit mapping key causes 503. Fix by binding an empty-mapping fallback account.

## Text GPT Groups Need allow_image_generation=true

Symptoms: GPT-Pro unavailable, clients get 403 `Image generation is not enabled for this group`, but `status.sakrylle.com` stays green.

Root cause: Codex declares the `image_generation` tool on every `/v1/responses` request, including pure coding chat. The image-generation intent gate blocks before upstream if group `allow_image_generation=false`.

Fix:

```sql
UPDATE groups SET allow_image_generation = true WHERE id IN (3,4,14);
```

Then restart `sub2api`.

No free image risk: these groups use channel 9 with `restrict_models=true` and text-only pricing. Real image requests still fail routing/pricing.

`groups.image_only` (migration 164, 2026-06-13) separates "image dedicated group" from "text group that must allow Codex tool declarations":

- true image groups: 5, 11, 21 -> `image_only=true`
- text groups such as 3/14 -> `image_only=false`

Create or edit image-only groups in Admin -> Groups -> Image Generation Pricing and enable **Image-only group**. The switch is independent from **Allow image generation** and updates the group cache through the normal admin path.

For emergency DB repair only:

```sql
UPDATE groups SET image_only = true WHERE id = ...;
```

Direct SQL bypasses cache invalidation, so restart `sub2api` afterward.
