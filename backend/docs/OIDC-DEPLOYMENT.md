# OIDC Provider Deployment Guide

This guide covers deploying Sakrylle's OIDC provider implementation to production.

## Overview

Sakrylle acts as an OpenID Connect (OIDC) provider, issuing `id_token` JWTs to authorized OAuth clients. Key features:

- **Discovery**: RFC 8414 `.well-known/openid-configuration` metadata
- **JWKS**: Public key rotation with grace period
- **Signing algorithms**: RS256 (RSA-2048) and ES256 (NIST P-256)
- **Encrypted key storage**: AES-256-GCM with 256-bit KEK
- **Standard scopes**: `openid`, `profile`, `email`

## Prerequisites

### Required Software

```bash
# Check dependencies
command -v psql    # PostgreSQL client 12+
command -v openssl # OpenSSL 1.1.1+
command -v curl    # cURL 7.0+
command -v jq      # jq 1.6+
```

### Required Database Migrations

Ensure these migrations are applied:

- `150_grant_oidc_scopes.sql` — Seeds `oauth_scopes` table with OIDC scopes
- `151_oauth_client_signing_algorithm.sql` — Adds `id_token_signed_response_alg` column

Verify:
```bash
psql -U sub2api -d sub2api -c "\dt oidc_signing_keys"
psql -U sub2api -d sub2api -c "SELECT scope FROM oauth_scopes WHERE scope LIKE 'openid%';"
```

### Required Environment Variables

**Mandatory:**
```bash
# Database connection (standard PostgreSQL env vars)
POSTGRES_HOST=localhost
POSTGRES_PORT=5432
POSTGRES_USER=sub2api
POSTGRES_PASSWORD=<secure-password>
POSTGRES_DB=sub2api

# OIDC Key Encryption Key (generate once, never rotate)
OIDC_KEY_ENCRYPTION_KEY=<64-hex-char-string>

# Admin API token (for key generation API calls)
ADMIN_TOKEN=<jwt-token-from-login>
```

**Optional:**
```bash
# OIDC issuer URL (defaults to API_BASE_URL)
OIDC_ISSUER=https://api.sakrylle.com

# API base URL (used by scripts)
API_BASE_URL=https://api.sakrylle.com
```

## Deployment Checklist

### Phase 1: Pre-Deployment Validation

#### 1.1. Generate KEK (First-Time Only)

The Key Encryption Key (KEK) encrypts signing keys in the database. **Generate once and never rotate.**

```bash
# Generate 256-bit KEK
openssl rand -hex 32

# Add to .env (example)
echo "OIDC_KEY_ENCRYPTION_KEY=a3f8d9e2c4b7a1f6e8d3c9b2a5f7e1d4c8b3a6f9e2d5c1b8a4f7e3d6c2b9a5f8" >> .env
```

**Security requirements:**
- Exactly 64 hexadecimal characters (32 bytes)
- Store in `.env` with mode `0600`
- Back up securely (losing KEK = losing all signing keys)
- **Never** commit to version control

#### 1.2. Verify Database State

```bash
# Check migrations
psql -U sub2api -d sub2api <<SQL
SELECT tablename FROM pg_tables WHERE schemaname='public' AND tablename IN ('oidc_signing_keys', 'oauth_scopes');
SQL

# Expected output:
#    tablename
# ------------------
#  oauth_scopes
#  oidc_signing_keys
```

#### 1.3. Run Setup Script (Interactive)

```bash
cd /opt/sub2api/backend
./scripts/oidc-setup.sh
```

**What it does:**
1. Validates KEK presence and length
2. Tests PostgreSQL connection
3. Checks required tables exist
4. Checks for existing keys (prompts to delete if found)
5. Generates RS256 + ES256 key pairs via admin API
6. Validates discovery and JWKS endpoints

**Expected output:**
```
[INFO] OIDC Setup Script
[SUCCESS] OIDC_KEY_ENCRYPTION_KEY already set (64 chars)
[SUCCESS] PostgreSQL connection OK
[SUCCESS] All required tables exist
[SUCCESS] OIDC scopes registered (3/3)
[INFO] No existing keys found
[SUCCESS] Signing keys generated successfully
[SUCCESS] Discovery endpoint OK
[SUCCESS] JWKS endpoint OK (2 keys)
[SUCCESS] OIDC setup complete!
```

**Common errors:**

| Error | Cause | Fix |
|-------|-------|-----|
| `Required environment variable not set: OIDC_KEY_ENCRYPTION_KEY` | KEK not in env | Export or add to `.env` |
| `Cannot connect to PostgreSQL` | Wrong DB credentials | Check `POSTGRES_*` vars |
| `Required table not found: oidc_signing_keys` | Migrations not applied | Run `atlas migrate apply` |
| `ADMIN_TOKEN not set` | No admin auth | Login: `POST /api/v1/auth/login` |
| `API call failed with HTTP 500` | Server error | Check sub2api logs |

### Phase 2: Production Deployment

#### 2.1. Update Environment (Production Mode)

For production, pre-set all variables in `/opt/stack/sub2api/.env`:

```bash
# Add to .env
OIDC_KEY_ENCRYPTION_KEY=<your-64-hex-kek>
OIDC_ISSUER=https://api.sakrylle.com
```

#### 2.2. Restart Service

```bash
# Docker Compose
cd /opt/stack
docker compose restart sub2api

# Wait for health check
sleep 5
curl -f https://sub.sakrylle.com/health || echo "Health check failed"
```

#### 2.3. Generate Keys (Production)

```bash
# Get admin token
ADMIN_TOKEN=$(curl -s -X POST https://sub.sakrylle.com/api/v1/auth/login \
  -H "Content-Type: application/json" \
  -d '{"email":"admin@sub2api.local","password":"<admin-password>"}' \
  | jq -r '.data.token')

export ADMIN_TOKEN

# Run setup in production mode (no prompts)
cd /opt/sub2api/backend
./scripts/oidc-setup.sh --production
```

Production mode differences:
- **No interactive prompts** (fails if preconditions not met)
- **Requires pre-set KEK** (won't generate)
- **Fails if keys exist** (use `oidc-key-rotate.sh` instead)

### Phase 3: Verification

#### 3.1. Run Verification Script

```bash
# Local
./scripts/oidc-verify.sh --issuer=https://api.sakrylle.com --verbose

# Remote (from any machine)
curl -fsSL https://raw.githubusercontent.com/Ranshen1209/sub2api/theme/monet-purple/backend/scripts/oidc-verify.sh | \
  bash -s -- --issuer=https://api.sakrylle.com
```

**Expected output:**
```
╔════════════════════════════════════════╗
║  OIDC Production Verification Script  ║
╚════════════════════════════════════════╝

[✓] KEK configured (64 chars)
[✓] Discovery metadata valid
[✓] JWKS valid (2/2 keys usable)
[✓] Authorization endpoint reachable (HTTP 400)
[✓] Token endpoint reachable (correctly rejects invalid request)
[✓] OIDC scopes registered in database
[✓] Found 2 active signing keys in database

════════════════════════════════════════
[✓] All checks passed!

[INFO] OIDC provider is production-ready
```

#### 3.2. Manual Endpoint Tests

```bash
ISSUER="https://api.sakrylle.com"

# Discovery metadata
curl -s "$ISSUER/.well-known/openid-configuration" | jq '{
  issuer,
  authorization_endpoint,
  token_endpoint,
  jwks_uri,
  scopes_supported,
  id_token_signing_alg_values_supported
}'

# JWKS (public keys)
curl -s "$ISSUER/.well-known/jwks.json" | jq '.keys[] | {kid, kty, alg, use}'

# Authorization endpoint (should return 400 missing parameters)
curl -i "$ISSUER/oauth/authorize"

# Token endpoint (should return 400 invalid_request)
curl -i -X POST "$ISSUER/oauth/token"
```

#### 3.3. Database State Check

```bash
psql -U sub2api -d sub2api <<SQL
-- Active signing keys
SELECT kid, algorithm, created_at, deleted_at
FROM oidc_signing_keys
ORDER BY created_at DESC;

-- Current active KID
SELECT value FROM settings WHERE key = 'oidc_current_kid';

-- OIDC scopes
SELECT scope, description FROM oauth_scopes WHERE scope IN ('openid', 'profile', 'email');

-- OAuth clients with OIDC scopes
SELECT name, allowed_scopes
FROM oauth_clients
WHERE allowed_scopes::jsonb ?& ARRAY['openid', 'profile', 'email'];
SQL
```

Expected active keys count: **2** (RS256 + ES256)

### Phase 4: Client Integration Test

Test the full OAuth + OIDC flow with a real client:

```bash
# Example: Register test client
psql -U sub2api -d sub2api <<SQL
INSERT INTO oauth_clients (
  client_id,
  name,
  redirect_uris,
  allowed_scopes,
  pkce_required,
  default_group_id
) VALUES (
  'test-oidc-client',
  'Test OIDC Client',
  '["http://localhost:3000/callback"]'::jsonb,
  '["openid", "profile", "email"]'::jsonb,
  true,
  (SELECT id FROM groups WHERE name = 'Claude-Kiro' LIMIT 1)
);
SQL
```

**Authorization flow:**
1. Direct user to: `https://api.sakrylle.com/oauth/authorize?client_id=test-oidc-client&response_type=code&redirect_uri=http://localhost:3000/callback&scope=openid%20profile%20email&code_challenge=<pkce-challenge>&code_challenge_method=S256`
2. User approves
3. Exchange code for tokens: `POST /oauth/token` with `grant_type=authorization_code`
4. Receive `access_token`, `id_token`, `refresh_token`
5. Decode `id_token` (JWT) and verify signature against JWKS

**Validate `id_token`:**
```bash
# Decode JWT (without verification)
ID_TOKEN="<received-id-token>"
echo "$ID_TOKEN" | cut -d. -f2 | base64 -d 2>/dev/null | jq .

# Expected claims:
# {
#   "iss": "https://api.sakrylle.com",
#   "sub": "<user-id>",
#   "aud": "test-oidc-client",
#   "exp": <timestamp>,
#   "iat": <timestamp>,
#   "email": "user@example.com",
#   "email_verified": true,
#   "name": "User Name"
# }
```

## Key Rotation

Rotate signing keys periodically (recommended: quarterly) or immediately after compromise.

### Scheduled Rotation

```bash
# Rotate with 7-day grace period (default)
./scripts/oidc-key-rotate.sh

# Custom grace period
./scripts/oidc-key-rotate.sh --grace-period=14d

# Rotate specific algorithm
./scripts/oidc-key-rotate.sh --algorithm=rs256

# Non-interactive (for cron)
./scripts/oidc-key-rotate.sh --grace-period=7d --force
```

**What happens:**
1. Generates new RS256 + ES256 key pairs
2. Updates `oidc_current_kid` to new RS256 KID
3. Marks old keys with `deleted_at = NOW() + grace_period`
4. Deletes any keys past their deletion timestamp
5. Refreshes JWKS cache

**Grace period behavior:**
- Old keys remain in JWKS during grace period
- Existing `id_token`s signed with old keys stay valid
- New `id_token`s use new key
- After grace period, old keys auto-delete (run script again to purge)

### Emergency Rotation (Compromised Key)

```bash
# 1. Rotate immediately with minimal grace
./scripts/oidc-key-rotate.sh --grace-period=1h --force

# 2. Invalidate all OAuth tokens (nuclear option)
psql -U sub2api -d sub2api <<SQL
UPDATE api_keys SET disabled = true WHERE key_prefix = 'sk_oauth_';
SQL

# 3. Notify affected users
# (implement notification logic in backend)
```

### Rotation Verification

```bash
# Check JWKS contains new keys
curl -s https://api.sakrylle.com/.well-known/jwks.json | jq '.keys | length'
# Expected: 2 (if no old keys) or 4 (during grace period)

# Check database state
psql -U sub2api -d sub2api <<SQL
SELECT
  kid,
  algorithm,
  CASE
    WHEN deleted_at IS NULL THEN 'active'
    WHEN deleted_at > NOW() THEN 'grace period'
    ELSE 'expired'
  END AS status,
  deleted_at
FROM oidc_signing_keys
ORDER BY created_at DESC;
SQL
```

## Troubleshooting

### Issue: Discovery endpoint returns 404

**Symptoms:**
```bash
curl https://api.sakrylle.com/.well-known/openid-configuration
# 404 Not Found
```

**Diagnosis:**
- Route not registered in Gin router
- `oidc_provider_handler.go` not wired

**Fix:**
```bash
# Check routes
grep -r "openid-configuration" backend/internal/server/routes/
# Should find: oauth.go RegisterOAuthOIDCRoutes()

# Restart service
docker compose restart sub2api
```

### Issue: JWKS contains no keys

**Symptoms:**
```bash
curl https://api.sakrylle.com/.well-known/jwks.json
# {"keys": []}
```

**Diagnosis:**
```bash
# Check database
psql -U sub2api -d sub2api -c "SELECT COUNT(*) FROM oidc_signing_keys WHERE deleted_at IS NULL;"
# Expected: >0
```

**Fix:**
```bash
# Regenerate keys
./scripts/oidc-setup.sh

# Check current_kid
psql -U sub2api -d sub2api -c "SELECT value FROM settings WHERE key = 'oidc_current_kid';"
```

### Issue: `id_token` signature verification fails

**Symptoms:**
- Client reports "Invalid signature" on `id_token`
- JWT debugger (jwt.io) shows verification error

**Diagnosis:**
1. Check issuer URL matches:
   ```bash
   # In id_token
   echo "<id_token>" | cut -d. -f2 | base64 -d | jq -r .iss
   # In discovery
   curl -s https://api.sakrylle.com/.well-known/openid-configuration | jq -r .issuer
   # Must be identical
   ```

2. Check KID in token header exists in JWKS:
   ```bash
   # Token header
   echo "<id_token>" | cut -d. -f1 | base64 -d | jq -r .kid
   # JWKS
   curl -s https://api.sakrylle.com/.well-known/jwks.json | jq -r '.keys[].kid'
   ```

**Fix:**
- **Issuer mismatch**: Update `OIDC_ISSUER` env var to match token's `iss` claim
- **KID not found**: Run key rotation or check if key was prematurely deleted
- **Algorithm mismatch**: Ensure client uses RS256 or ES256 (not HS256)

### Issue: KEK error "failed to decrypt key"

**Symptoms:**
```
ERROR Failed to load OIDC signing key kid=<kid>: cipher: message authentication failed
```

**Cause:**
- `OIDC_KEY_ENCRYPTION_KEY` changed
- Database key encrypted with different KEK
- Corrupted encrypted key data

**Fix (if KEK lost):**
```bash
# 1. Mark all keys as deleted
psql -U sub2api -d sub2api -c "UPDATE oidc_signing_keys SET deleted_at = NOW();"

# 2. Generate new KEK
openssl rand -hex 32

# 3. Update .env with new KEK

# 4. Regenerate keys
./scripts/oidc-setup.sh

# 5. Restart service
docker compose restart sub2api
```

**Prevention:**
- Back up KEK in secure vault (1Password, AWS Secrets Manager)
- Document KEK location in runbook
- Never rotate KEK (rotate signing keys instead)

### Issue: High latency on JWKS endpoint

**Symptoms:**
- `/well-known/jwks.json` takes >500ms
- Database query logs show repeated `SELECT * FROM oidc_signing_keys`

**Diagnosis:**
- JWKS cache disabled or expired
- Too many keys in database (>10)

**Fix:**
```bash
# Check key count
psql -U sub2api -d sub2api -c "SELECT COUNT(*) FROM oidc_signing_keys;"

# Delete expired keys
psql -U sub2api -d sub2api -c "DELETE FROM oidc_signing_keys WHERE deleted_at IS NOT NULL AND deleted_at < NOW();"

# Restart to rebuild cache
docker compose restart sub2api
```

**Optimize:**
- Set `deleted_at` instead of keeping keys indefinitely
- Run `oidc-key-rotate.sh` periodically to prune expired keys
- Monitor `oidc_signing_keys` table size

## Rollback Plan

### Scenario: OIDC deployment breaks existing OAuth flows

**Symptoms:**
- `/oauth/token` returns 500
- Existing OAuth clients fail authorization

**Immediate rollback:**
```bash
# 1. Disable OIDC-specific routes (if isolated)
# Edit: backend/internal/server/routes/oauth.go
# Comment out: oidc_provider_handler routes

# 2. Restart service
docker compose restart sub2api

# 3. Monitor logs
docker compose logs -f sub2api | grep -i "oidc\|openid"
```

**Database rollback (if needed):**
```bash
# Soft-delete all signing keys (preserves data)
psql -U sub2api -d sub2api <<SQL
UPDATE oidc_signing_keys SET deleted_at = NOW();
DELETE FROM settings WHERE key = 'oidc_current_kid';
SQL
```

**Code rollback:**
```bash
# Revert to commit before OIDC merge
git checkout <pre-oidc-commit-hash>
docker compose build sub2api
docker compose up -d sub2api
```

### Scenario: Key rotation breaks all `id_token` validation

**Symptoms:**
- All clients report invalid signature after rotation
- JWKS missing old KIDs

**Recovery:**
```bash
# 1. Check JWKS
curl -s https://api.sakrylle.com/.well-known/jwks.json | jq '.keys[].kid'

# 2. Check database for prematurely deleted keys
psql -U sub2api -d sub2api <<SQL
SELECT kid, algorithm, deleted_at
FROM oidc_signing_keys
WHERE deleted_at BETWEEN (NOW() - INTERVAL '1 day') AND NOW()
ORDER BY created_at DESC;
SQL

# 3. Restore keys (if soft-deleted within grace period)
psql -U sub2api -d sub2api <<SQL
UPDATE oidc_signing_keys
SET deleted_at = NULL
WHERE kid IN ('<old-kid-1>', '<old-kid-2>');
SQL

# 4. Restart service
docker compose restart sub2api

# 5. Re-run rotation with proper grace period
./scripts/oidc-key-rotate.sh --grace-period=7d
```

## Monitoring

### Metrics to Track

1. **JWKS cache hit rate**
   - Log: `JWKS cache hit` vs `Building JWKS from database`
   - Target: >99% hit rate

2. **`id_token` issuance rate**
   - Log: `Issuing id_token for user=<id>, client=<client>`
   - Alert: Sudden drop (rotation issue?)

3. **Key decryption errors**
   - Log: `Failed to load OIDC signing key`
   - Alert: Any occurrence (KEK mismatch)

4. **Signing key age**
   - Query: `SELECT MAX(AGE(NOW(), created_at)) FROM oidc_signing_keys WHERE deleted_at IS NULL;`
   - Alert: >90 days (schedule rotation)

### Recommended Alerts

```yaml
# Prometheus-style rules
- alert: OIDCNoActiveSigningKeys
  expr: sub2api_oidc_signing_keys_active == 0
  for: 5m
  severity: critical

- alert: OIDCKeyDecryptionFailure
  expr: rate(sub2api_oidc_key_decrypt_errors[5m]) > 0
  severity: critical

- alert: OIDCKeyRotationOverdue
  expr: (time() - sub2api_oidc_newest_key_created_timestamp) > (90 * 86400)
  severity: warning
```

## Security Best Practices

1. **KEK Management**
   - Generate once, never rotate
   - Store in vault (not plain `.env` in prod)
   - Restrict file permissions: `chmod 0600 .env`
   - Back up to separate secure location

2. **Key Rotation Policy**
   - Rotate quarterly (every 90 days)
   - Use 7-day grace period minimum
   - Document rotation in runbook
   - Test rotation in staging first

3. **Access Control**
   - Restrict `/api/v1/admin/oidc/keys/*` to admin-only
   - Rate-limit discovery/JWKS endpoints (prevent DoS)
   - Log all key generation/deletion events

4. **Audit Trail**
   - Enable `created_at`, `updated_at`, `deleted_at` on `oidc_signing_keys`
   - Log `id_token` issuance (user ID, client ID, scopes)
   - Periodic JWKS export for forensics

5. **Disaster Recovery**
   - Back up KEK in vault
   - Document key rotation procedure
   - Test rollback plan in staging
   - Maintain runbook with contact info

## References

- [RFC 6749: OAuth 2.0 Authorization Framework](https://datatracker.ietf.org/doc/html/rfc6749)
- [RFC 7519: JSON Web Token (JWT)](https://datatracker.ietf.org/doc/html/rfc7519)
- [OpenID Connect Core 1.0](https://openid.net/specs/openid-connect-core-1_0.html)
- [RFC 8414: OAuth 2.0 Authorization Server Metadata](https://datatracker.ietf.org/doc/html/rfc8414)
- [RFC 7517: JSON Web Key (JWK)](https://datatracker.ietf.org/doc/html/rfc7517)
