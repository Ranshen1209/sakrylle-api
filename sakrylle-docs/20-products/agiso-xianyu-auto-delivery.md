# Agiso Xianyu Auto Delivery

This document captures the Sakrylle side of the Agiso-powered Xianyu automatic delivery bridge.

## Scope

- Receive signed Agiso webhook pushes for Xianyu payment and refund events.
- Mint exactly one Sakrylle balance redeem code per order.
- Send the redeem instruction back to the buyer through Agiso chat messaging.
- Mark the Agiso order shipped through Agiso's dummy send endpoint.

## Public Route

- `POST /integrations/agiso/delivery`
- `GET /health`

The route is protected only by Agiso push signature verification and does not use API-key auth.

## Environment Variables

- `AGISO_APP_SECRET`
- `AGISO_ACCESS_TOKEN`
- `AGISO_API_BASE`
- `AGISO_VALUE_MULTIPLIER`
- `AGISO_CODE_EXPIRES_DAYS`
- `AGISO_SELLER_ID`

## Operational Notes

- `AGISO_API_BASE` defaults to `https://gw-api.agiso.com/aldsIdle`.
- `AGISO_VALUE_MULTIPLIER` defaults to `1.0`.
- `AGISO_CODE_EXPIRES_DAYS` defaults to `0`.
- Raw webhook payloads are stored without the `sign` field.
- Duplicate pushes must remain idempotent at the order level.

## Verification Checklist

- Signature verification rejects tampered pushes.
- Duplicate `biz_order_id` pushes do not mint a second redeem code.
- Partial failures resume cleanly on the next push.
- Refund pushes disable unused redeem codes.
