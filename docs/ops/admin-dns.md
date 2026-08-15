# Admin, DNS, And Certificate Ops

## Admin Password Rotation

`ADMIN_PASSWORD` in `.env` is bootstrap-only. Existing admin users are not
reseeded on restart.

When rotating directly in the database, remember bcrypt has a 72-byte input
limit. Assert that the generated hash is non-empty and starts with `$2b$` before
writing SQL. Never print or commit the password or hash.

## Current DNS State

The production origin is `154.44.8.202`. Current production A records for
`platform`, `ai1`, `api`, `oidc1`, `sub`, `automatic-delivery`, `doc`, `status`,
and `chat` under `sakrylle.com` point to that address and are DNS only.

Do not validate these records with an unqualified local `dig`: local Clash DNS
may return a `198.18.0.0/15` fake IP. Query from the production node, query an
authoritative nameserver, or inspect the Cloudflare API response.

Example server-side resolution check:

```bash
ssh ssh-sakrylle 'for host in platform ai1 api oidc1 sub automatic-delivery doc status chat; do printf "%s " "$host"; dig +short @1.1.1.1 "$host.sakrylle.com" A | paste -sd, -; done'
```

This checks public answers, not the Cloudflare `proxied` flag. Confirm DNS-only
state through the Cloudflare API or dashboard as well.

## Cloudflare Credentials And Change Procedure

Certbot's Cloudflare credentials file on the production node is:

```text
/opt/stack/secrets/cloudflare.ini
```

Required ownership and mode are `600 root:root`. The token has been verified for
Zone reads and DNS-record creation/deletion. It cannot read every zone setting;
in particular, do not infer or document the Cloudflare Zone SSL mode from this
token.

Before any DNS change:

1. Export or otherwise back up the current zone records to a root-only file on
   the production node. Confirm the backup is non-empty without printing token
   or secret values.
2. Record the exact record ID, name, type, value, TTL, and `proxied` state being
   changed.
3. If token permissions need verification, create a uniquely named, short-lived
   TXT record such as `_ops-permission-test-<timestamp>.sakrylle.com`.
4. Read the temporary TXT record back through the API and an authoritative DNS
   query.
5. Delete it immediately, then verify both the API and authoritative DNS no
   longer return it.
6. Apply the intended change and compare the result with the pre-change backup.

Keep backups and API output on the server. Do not place the token in command
arguments, shell history, chat, logs, or the repository. Never print
`cloudflare.ini`.

Verify credential file metadata without reading it:

```bash
ssh ssh-sakrylle 'stat -c "%a %U:%G %n" /opt/stack/secrets/cloudflare.ini'
```

## Certbot DNS-01 Renewal

Let's Encrypt issuance uses Cloudflare DNS-01, not HTTP-01. The renewal profile
contains:

```ini
authenticator = dns-cloudflare
dns_cloudflare_credentials = /etc/letsencrypt/cloudflare.ini
dns_cloudflare_propagation_seconds = 60
```

The root-owned credential path inside the Certbot environment is intentionally
different from the stack's source secret path.

The `admin` user's crontab runs daily:

```cron
17 3 * * * /opt/stack/scripts/renew-certs.sh >> /opt/stack/logs/certbot-renew.log 2>&1
```

After renewal, the script runs `nginx -t` and reloads Nginx. Validate the
installed automation without printing credentials:

```bash
ssh ssh-sakrylle 'crontab -l | grep -F "/opt/stack/scripts/renew-certs.sh"'
ssh ssh-sakrylle 'grep -E "nginx -t|nginx.*reload" /opt/stack/scripts/renew-certs.sh'
ssh ssh-sakrylle 'tail -n 100 /opt/stack/logs/certbot-renew.log'
ssh ssh-sakrylle 'docker exec nginx nginx -t'
ssh ssh-sakrylle 'openssl s_client -connect 127.0.0.1:8443 -servername api.sakrylle.com </dev/null 2>/dev/null | openssl x509 -noout -subject -issuer -dates'
```

A Let's Encrypt staging dry-run previously completed its DNS challenge but then
encountered an abnormal ACME authorization state. Do not report that dry-run as
fully successful. Production DNS-01 issuance did succeed. Future validation
should record the exact staging result separately from production certificate
status.

## Primary App Domain

`platform.sakrylle.com` was added on 2026-08-09 as the preferred unified ingress
for the full app, API, and OAuth/OIDC route surface. `ai1.sakrylle.com`,
`oidc1.sakrylle.com`, and `sub.sakrylle.com` remain online as aliases.

For compatibility, production still uses
`frontend_url=https://ai1.sakrylle.com` and
`oauth_issuer=https://oidc1.sakrylle.com`. Do not change the issuer merely to
rename the public entry point: strict OIDC clients reject discovery if the
configured issuer changes. Do not use `sub.sakrylle.com` in new health checks,
docs, or user-facing links.

## Historical: sub.sakrylle.com China Diagnosis

On 2026-06-15, testing indicated hostname-specific DNS poisoning and an
SNI-triggered TLS reset for `sub.sakrylle.com` from mainland China. Observed
forged answers included Facebook and Dropbox address ranges. Sibling domains on
the same origin worked in that incident, and failed connections did not reach
the server.

The incident's diagnostic sequence remains useful:

- compare the affected hostname with same-origin sibling domains
- compare networks and resolvers
- query authoritative DNS rather than a Clash fake-IP resolver
- test the known origin with `curl --resolve`
- correlate client failures with origin access logs

These are historical observations, not proof of the domain's current GFW
behavior. Re-test from the relevant networks before making a current claim.

`sub.sakrylle.com` was previously orange-cloud proxied as a mitigation. That is
historical configuration: it is now DNS only. Do not restore proxying as an
informal troubleshooting step, and do not state a current Cloudflare SSL mode
without a separately authorized, verifiable source.
