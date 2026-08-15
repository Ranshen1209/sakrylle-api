# OpenAI Anthropic Messages Compatibility Design

## Goal

All OpenAI groups should accept Anthropic-format `POST /v1/messages` requests by default, so API keys created for OpenAI groups can be used by Anthropic-compatible clients without a per-group opt-in.

## Current Behavior

`/v1/messages` already auto-routes OpenAI groups to `OpenAIGatewayHandler.Messages`, which converts Anthropic Messages requests into OpenAI Responses requests. The handler currently rejects OpenAI groups when `group.allow_messages_dispatch` is false.

The default Claude-family mapping also still contains the retired `gpt-5.3-codex` Sonnet target. That makes the existing opt-in path fragile even when an admin enables the dispatch flag.

## Desired Behavior

OpenAI groups should treat Anthropic Messages compatibility as a default capability:

- OpenAI groups should not return `permission_error` solely because `allow_messages_dispatch` is false.
- Anthropic requests whose model is already a GPT/OpenAI model should pass through the OpenAI routing path without Claude-family remapping.
- Claude-family requests should default to supported GPT models:
  - Opus -> `gpt-5.5`
  - Sonnet -> `gpt-5.4`
  - Haiku -> `gpt-5.4-mini`
- Explicit `messages_dispatch_model_config` mappings should still override family defaults.
- Non-OpenAI group behavior should not change.
- `/v1/models` must continue using pricing/config-driven model visibility; do not reintroduce default model-list fallback.

## Design

Add a small helper in the OpenAI gateway handler, or equivalent focused service helper, that answers whether an API key group allows Anthropic Messages dispatch. It should return true for OpenAI groups by default and preserve the existing false behavior for nil or non-OpenAI groups. The handler should use that helper instead of checking `!group.AllowMessagesDispatch` directly.

Update the OpenAI Messages dispatch default model constants so Claude-family requests map to the new GPT targets. Keep the existing resolution order: exact model mapping first, configured family mapping second, built-in family default third, and no mapping for non-Claude models. This means a request with `model: "gpt-5.4"` is considered an OpenAI model request and continues through normal account selection.

Update admin UI defaults and placeholder text so new OpenAI groups no longer suggest or write `gpt-5.3-codex`. The UI toggle may remain visible for now as an admin affordance, but backend behavior is authoritative.

## Testing

Backend tests should cover:

- An OpenAI group with `AllowMessagesDispatch=false` is allowed past the local permission gate for `/v1/messages`.
- Non-OpenAI groups do not gain OpenAI Messages dispatch through this helper.
- Default model resolution returns `gpt-5.5` for Opus, `gpt-5.4` for Sonnet, and `gpt-5.4-mini` for Haiku.
- GPT/OpenAI model names return no dispatch mapping, so they are routed as requested.

Frontend tests should update the OpenAI Messages default config expectations from `gpt-5.3-codex` to `gpt-5.4`.

## Operational Notes

Existing OpenAI groups become compatible after deployment without database updates, assuming their channels/accounts can route and bill the mapped GPT models. Auth cache snapshots may still carry group fields, but the new behavior should be computed from group platform plus request model rather than requiring `allow_messages_dispatch=true` in the snapshot.
