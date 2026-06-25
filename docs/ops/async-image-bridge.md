# Async Image Bridge

Group 21 / channel 13 / account 1131 routes `gpt-image-2-async` through 12ai async task API to avoid upstream sync renders hitting Cloudflare 524 after more than 100 seconds.

Client protocol is unchanged.

Implementation:

- `backend/internal/service/openai_images_async.go`
- `backend/internal/service/openai_images_usage_synth.go`
- branch in `openai_images.go` under `forwardOpenAIImagesAPIKey` when `account.IsAsyncImage()`

## Upstream Flow

- submit: `POST https://cdn.12ai.org/v1/task/submit`
- poll: `GET https://cdn.12ai.org/v1/task/{id}`
- download `outputs[]`
- return `b64_json`

No single hop stays open longer than 100 seconds.

## Account Credentials

Config lives in `accounts.credentials`, not channel `features_config`, because the account is threaded into `ForwardImages`.

```jsonc
"async_enabled": "true",
"async_base_url": "https://cdn.12ai.org",
"api_key": "<async-task-permission key>",
"async_image_host_suffix": "12ai.org",
"async_image_synth": {
  "output_token_table": {
    "1K": {"low":196,"medium":1756,"high":7023},
    "2K": {"low":397,"medium":3571,"high":14281},
    "4K": {"low":367,"medium":3299,"high":13195}
  },
  "ref_image_tokens": {"1K":1024, "2K":1521, "4K":1508},
  "image_input_ratio": 1.6
}
```

`async_enabled` must be the string `"true"` because `GetCredential` ignores JSON booleans.

## Billing

The async API returns no token usage. The gateway synthesizes `OpenAIUsage` from size, quality, reference images, and prompt, then feeds the existing token billing path.

Pricing setup:

- channel 13 token pricing: input `8e-6`, output `48e-6`
- synthesized image output tokens go into `OutputTokens`
- leave `image_output_price=0`
- image-input `￥12.8` has no dedicated pricing field, so bill it as reference tokens times `image_input_ratio`
- default `image_input_ratio=1.6`, derived from `12.8 / 8`
- margin comes from group 21 `rate_multiplier=1.5`

Final bill:

```text
synth_tokens * channel_price * group.rate_multiplier
```

## Model Mapping

Clients request `gpt-image-2-async`.

Channel 13 maps:

```json
{"gpt-image-2-async":"gpt-image-2"}
```

`billing_model_source=requested` is required so billing uses `gpt-image-2-async`, matching the pricing row. Do not use `channel_mapped`; it bills on mapped `gpt-image-2`, misses the row, and collides with channel 11 model plaza display.

## Quality And Calibration

`auto` or missing quality is forced to `medium` for both upstream submit and billing.

Calibrate `output_token_table` by running async generations and reading net charge from the 12ai dashboard:

```text
net / (￥48 / 1M) = effective output tokens
```

Missing table cell uses conservative row max and warns. `OutputTokens == 0` fails closed and does not deliver the image.

DB-only changes to pricing, mapping, billing source, or credentials require `docker compose restart sub2api`; `docker compose up -d` is a no-op when the image is unchanged and will not reload cache.

## Model Plaza Display

The plaza resolves pricing per group from the channel serving that group, so same-name models across channels no longer collide.

To display the image-input rate (`input_price * image_input_ratio`, for example `￥12.8/1M` base), `/channels/available` reads a display copy of `image_input_ratio` from channel 13 `features_config`. Keep this display copy in sync with the account credential value used for billing.
