# Sakrylle OAuth 2.0 v2 — Device Flow CLI Guide

> Companion to [`OAUTH_V2_INTEGRATION.md`](./OAUTH_V2_INTEGRATION.md). For the
> error vocabulary used in polling responses, see
> [`OAUTH_V2_ERROR_REFERENCE.md`](./OAUTH_V2_ERROR_REFERENCE.md).

This document targets developers writing **CLI tools, shell automations, TV
apps, and other clients that have no embedded browser**. Sakrylle implements
RFC 8628 (OAuth 2.0 Device Authorization Grant) for these.

---

## When to use Device Flow

Use Device Flow when **all** of the following are true:

- Your client cannot reliably open the system browser at the user's
  consent moment (CLI on a remote shell, TV, kiosk).
- Your client can display a 14-character user code prominently and direct
  the user to a URL on a different device.
- Your client can poll an HTTP endpoint at a server-controlled interval.

If your client runs locally with a working browser (desktop app, mobile),
**Authorization Code + PKCE** with a loopback / custom-scheme redirect is
the better choice. Device Flow trades polling cost and a UX detour for
not needing in-process browser handling.

Sakrylle gates Device Flow per client (`device_flow_enabled` on the client
row). Email `support@sakrylle.com` to enable it for your client.

---

## Endpoint cheatsheet

| Method | Path | Purpose |
|---|---|---|
| POST | `/oauth/device/code` | Create a device authorization request. |
| GET  | `/oauth/device` | Verification page; user types the code here. |
| POST | `/api/v1/oauth/device/approve` | User approves a pending request (JWT-authenticated). |
| POST | `/api/v1/oauth/device/deny` | User denies a pending request. |
| POST | `/oauth/token` (grant `urn:ietf:params:oauth:grant-type:device_code`) | Client polls for the access token. |

All form posts require
`Content-Type: application/x-www-form-urlencoded`. JSON is rejected with
`invalid_request`.

---

## End-to-end with curl

### 1. Create a device authorization request

```bash
RESP=$(curl -sS https://sub.sakrylle.com/oauth/device/code \
  -H "Content-Type: application/x-www-form-urlencoded" \
  -d "client_id=YOUR_CLIENT_ID" \
  -d "scope=profile:read messages:create offline_access")
echo "$RESP" | jq
```

Sample 200 response:

```json
{
  "device_code": "DcXkP2...",
  "user_code": "SKRY-BCDF-G2346",
  "verification_uri": "https://sub.sakrylle.com/oauth/device",
  "verification_uri_complete": "https://sub.sakrylle.com/oauth/device?user_code=SKRY-BCDF-G2346",
  "expires_in": 600,
  "interval": 5
}
```

Important fields:

- `device_code` — opaque, treated as a secret. Used in subsequent
  `/oauth/token` polls. Never log this verbatim.
- `user_code` — 14 chars with `SKRY-` prefix. Show this to the user
  prominently. Display **both** `verification_uri` and `user_code`; treat
  `verification_uri_complete` as a convenience link.
- `expires_in` — total lifetime of the authorization in seconds (default
  600). Once expired, polling returns `expired_token`.
- `interval` — initial polling interval in seconds. Slow down on demand.

### 2. Display the user code

```text
Visit https://sub.sakrylle.com/oauth/device on any device, then enter:

    SKRY-BCDF-G2346

The code expires in 10 minutes.
```

The user opens that URL in a browser, signs into Sakrylle, types the code,
and approves the requested scopes. Approval calls
`POST /api/v1/oauth/device/approve` server-side; your CLI does **not**
talk to that endpoint.

### 3. Poll the token endpoint

```bash
DEVICE_CODE="$(echo "$RESP" | jq -r .device_code)"
INTERVAL="$(echo "$RESP" | jq -r .interval)"
EXPIRES="$(echo "$RESP" | jq -r .expires_in)"

start=$(date +%s)
while :; do
  now=$(date +%s)
  if (( now - start > EXPIRES )); then
    echo "device code expired client-side"
    exit 1
  fi

  POLL=$(curl -sS https://sub.sakrylle.com/oauth/token \
    -H "Content-Type: application/x-www-form-urlencoded" \
    -d "grant_type=urn:ietf:params:oauth:grant-type:device_code" \
    -d "device_code=${DEVICE_CODE}" \
    -d "client_id=YOUR_CLIENT_ID")

  err=$(echo "$POLL" | jq -r '.error // empty')
  case "$err" in
    "")
      echo "$POLL" | jq
      break
      ;;
    authorization_pending)
      sleep "$INTERVAL"
      ;;
    slow_down)
      INTERVAL=$((INTERVAL + 5))
      sleep "$INTERVAL"
      ;;
    access_denied)
      echo "user denied authorization" >&2
      exit 1
      ;;
    expired_token)
      echo "device code expired server-side" >&2
      exit 1
      ;;
    *)
      echo "unexpected error: $POLL" >&2
      exit 1
      ;;
  esac
done
```

The successful poll returns the same body shape as the Authorization Code
flow:

```json
{
  "access_token": "sk_oauth_...",
  "token_type": "Bearer",
  "expires_in": 86400,
  "refresh_token": "rt_...",
  "refresh_token_expires_in": 2592000,
  "scope": "profile:read messages:create offline_access"
}
```

### 4. Use the access token

```bash
ACCESS=$(echo "$POLL" | jq -r .access_token)
curl -sS https://api.sakrylle.com/v1/models \
  -H "Authorization: Bearer ${ACCESS}"
```

---

## Python implementation

```python
import time
import sys
import requests

BASE = "https://sub.sakrylle.com"
CLIENT_ID = "YOUR_CLIENT_ID"
SCOPE = "profile:read messages:create offline_access"

def device_authorize() -> dict:
    r = requests.post(
        f"{BASE}/oauth/device/code",
        data={"client_id": CLIENT_ID, "scope": SCOPE},
        headers={"Content-Type": "application/x-www-form-urlencoded"},
        timeout=10,
    )
    r.raise_for_status()
    return r.json()

def poll_for_token(device_code: str, interval: int, expires_in: int) -> dict:
    deadline = time.monotonic() + expires_in
    while time.monotonic() < deadline:
        r = requests.post(
            f"{BASE}/oauth/token",
            data={
                "grant_type": "urn:ietf:params:oauth:grant-type:device_code",
                "device_code": device_code,
                "client_id": CLIENT_ID,
            },
            headers={"Content-Type": "application/x-www-form-urlencoded"},
            timeout=10,
        )
        body = r.json()
        if r.status_code == 200:
            return body
        err = body.get("error")
        if err == "authorization_pending":
            time.sleep(interval)
            continue
        if err == "slow_down":
            interval += 5
            time.sleep(interval)
            continue
        if err in ("access_denied", "expired_token"):
            raise RuntimeError(f"device flow ended: {err}")
        raise RuntimeError(f"unexpected error from /oauth/token: {body}")
    raise RuntimeError("device code expired before user approved")

def main() -> None:
    auth = device_authorize()
    print(f"Open: {auth['verification_uri']}")
    print(f"Code: {auth['user_code']}")
    print(f"(or use {auth['verification_uri_complete']})")
    tok = poll_for_token(auth["device_code"], auth["interval"], auth["expires_in"])
    print("access_token:", tok["access_token"])

if __name__ == "__main__":
    try:
        main()
    except Exception as exc:
        print(f"error: {exc}", file=sys.stderr)
        sys.exit(1)
```

---

## Polling state machine

```text
                ┌─────────────────────┐
                │ create device code  │
                │ POST /device/code   │
                └──────────┬──────────┘
                           │ device_code, user_code, interval, expires_in
                           ▼
                ┌─────────────────────┐
       ┌────────│ poll /oauth/token   │◄─────┐
       │        └──────────┬──────────┘      │
       │  200              │  HTTP 400       │
       │  (success)        ▼                 │
       │           ┌───────────────┐         │
       │           │ inspect error │         │
       │           └───┬───────────┘         │
       │               │                     │
       │ ┌─────────────┴────────┬───────────┐│
       │ ▼                      ▼           ▼│
       │ authorization_pending  slow_down   access_denied / expired_token
       │ sleep(interval)        interval+=5 EXIT
       │                        sleep(interval)
       │                        │
       └────────────────────────┘
```

Terminal states (one of):
- `200 OK` with token body → use the access token; rotate via refresh.
- `access_denied` → user explicitly denied; do not retry.
- `expired_token` → device code lifetime exceeded; create a new one.
- Network or unexpected error → abort and surface the error.

---

## Gotchas

### Always poll on `interval`, not faster

Polling faster than `interval` returns `slow_down` and increases the
required interval by 5 seconds (sticky). A misbehaving client can run its
poll budget into the ground in seconds; respect the interval.

### `verification_uri_complete` is convenience only

Even when the response includes `verification_uri_complete`, **always**
display the plain `verification_uri` and `user_code`. Some users will
type the code on a different device than the one with the link.

### `device_code` is a secret

Treat the opaque `device_code` like a refresh token: never log it, never
echo it. The `user_code` is short-lived and not a credential, but should
still not be persisted past the flow.

### Token storage on CLI clients

Refresh tokens on a CLI client must live in OS-managed secure storage:

| Platform | Preferred | Fallback |
|---|---|---|
| macOS | Keychain | `~/.config/sakrylle/credentials` mode 0600, dir mode 0700 |
| Windows | Credential Manager | `%APPDATA%\Sakrylle\credentials` ACL'd to user |
| Linux | Secret Service (`libsecret`) | `~/.config/sakrylle/credentials` mode 0600 |

If using a file fallback, the parent directory must be `0700` and the file
itself `0600`. Do not put the refresh token in shell history, exported
environment variables, or world-readable config.

### Refresh rotation rules apply

Once the device flow yields tokens, refresh rotation works exactly as in
the Authorization Code flow:

- Each `/oauth/token` refresh returns a new access + refresh pair.
- The old refresh token is single-use; replay triggers family revocation.
- `refresh_token_expires_in` is family-anchored, not extended on each
  rotation.
- Concurrent refreshes for the same grant must be serialized client-side.

See [`OAUTH_V2_INTEGRATION.md` §5](./OAUTH_V2_INTEGRATION.md#5-refresh-token-rotation).

### Don't log the access token either

`sk_oauth_...` tokens carry the user's full per-grant authority. Use a
mask in any debug output — e.g. `sk_oauth_***...***xyz` (first 9 + last 4).

---

## Failure handling decision table

| Server response | Retry? | Wait | Notes |
|---|---|---|---|
| `200 OK` | n/a | n/a | Success, exit polling. |
| `authorization_pending` | yes | `interval` | Expected during normal user action. |
| `slow_down` | yes | `interval += 5` | Server-asked backoff; sticky. |
| `access_denied` | no | n/a | User denied; show friendly message. |
| `expired_token` | no | n/a | Start a fresh device flow. |
| `invalid_grant` | no | n/a | `device_code` consumed or wrong client; abort. |
| `invalid_client` | no | n/a | Client credentials/config issue; abort. |
| 5xx | yes | exponential | Cap at 60s; abort after total wallclock > `expires_in`. |
| Network error | yes | exponential | Same as 5xx. |

---

## CSRF on the verification page

The user-facing `/oauth/device` form uses a CSRF cookie that the consent
page reads on submission. CLI clients never see this; it's a server-side
detail of the user's browser session. If a user reports the verification
page rejecting their code with `csrf token mismatch`, ask them to reload
the page and re-enter the code (cookie expiry).
