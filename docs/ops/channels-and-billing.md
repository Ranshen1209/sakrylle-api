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
| 8 Deepseek | `channel_mapped` | 6 Deepseek (0.7x), 9 Official (1.0x) | Both groups need `platform='anthropic'`. |
| 9 OpenAI GPT | `channel_mapped` | 3 Pro (0.4x), 4 Plus (0.25x), 10 Plus-Special (0.2x) | `codex-auto-review -> gpt-5.4` alias. |
| 10 GPT-Image-2-4K | `requested` | 11 GPT-Image-2-4K (1.0x) | `gpt-image-2-4k -> gpt-image-2-vip`, `￥0.18/call`. |
| 11 GPT-Image-2 | `channel_mapped` | 5 GPT-Image (1.0x) | `$0.10/call`, needs `allow_image_generation=true`. |

## Pricing Semantics

`channel_model_pricing` rows are per-token USD at official upstream rates. Final bill:

```text
tokens * price * groups.rate_multiplier
```

Change group multiplier for margin. Do not mutate pricing rows for margin; those rows are audit and reconciliation baselines.

Upstream v0.1.176 added `groups.model_pricing` (per-model overrides) and `groups.long_context_pricing_enabled` (default true). Do not use group `model_pricing` for Sakrylle margin; keep channel rows as the baseline and apply `rate_multiplier`. Leave long-context pricing enabled unless a specific group must ignore official long-context tiers.

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

3. `/v1/models` routes by `group.platform`; Deepseek groups must use `platform='anthropic'`. If set to `openai`, pricing lookup misses and the models list is empty while calls can still succeed.

Deepseek pricing rows store upstream promotional rates. Refresh them when promotion ends.

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

New image-only groups must be manually set:

```sql
UPDATE groups SET image_only = true WHERE id = ...;
```

Then restart `sub2api`.
