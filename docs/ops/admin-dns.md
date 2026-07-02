# Admin And DNS Ops

## Admin Password Rotation

`ADMIN_PASSWORD` in `.env` is bootstrap-only. Existing admin users are not reseeded on restart.

When rotating directly in DB, remember bcrypt has a 72 byte input limit. Assert that the generated hash is non-empty and starts with `$2b$` before writing SQL.

## Cloudflare DNS

Cloudflare DNS token path:

```text
/opt/stack/secrets/cloudflare.ini
```

Zone:

```text
cf223b3e0b2adcd4876cea779b041a1c
```

Subdomains generally point A records to `154.36.159.42` and are DNS-only.

The `cloudflare.ini` token is DNS-only for certbot. Reading zone settings such as SSL mode needs a broader token.

## sub.sakrylle.com GFW Block

As of 2026-06-15, `sub.sakrylle.com` is blocked from mainland China by:

- per-hostname DNS poisoning
- SNI-based TLS reset for ClientHello containing `sub.sakrylle.com`

Observed forged IPs included Facebook/Dropbox ranges such as `69.63.186.31`, `108.160.169.55`, and `2a03:2880:...face:b00c`.

`api.sakrylle.com`, `ai1.sakrylle.com`, and the origin server are not blocked. Failed connections never reach the server.

Diagnosis trail:

- only `sub` failed while same-IP sibling domains worked
- cellular worked while this PC failed
- `dig` returned forged IPs
- `curl --resolve <realIP>` got TLS RST

## Primary App Domain

Mitigation applied: use `ai1.sakrylle.com` as the primary app domain. Keep production settings such as `frontend_url` on `https://ai1.sakrylle.com`; do not use `sub.sakrylle.com` in health checks, docs, user-facing links, or new configs.

## Cloudflare Proxy Exception

Historical mitigation: `sub.sakrylle.com` was orange-cloud proxied. This does not make it a supported app domain.

SSL/TLS mode is currently Full. Origin has a valid Let's Encrypt certificate; consider Full strict.

This helps clean-DNS users but does not fully restore direct China access because:

- GFW can still inject forged DNS before the real Cloudflare IP arrives over plain UDP/53
- plaintext SNI to Cloudflare edge can still be reset

Full direct-access fix for `sub.sakrylle.com` would require DoH plus ECH. Until then, use `ai1.sakrylle.com` for normal access.

Operator Loon rule:

```text
DOMAIN-SUFFIX,sakrylle.com,Proxies
```

Do not route direct.

To revert orange cloud, patch the `sub` A record with `proxied:false`; keep `ai1` as the app domain either way.
