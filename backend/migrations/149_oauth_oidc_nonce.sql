-- 149_oauth_oidc_nonce.sql
--
-- OIDC HIGH-1: persist the authorize-request nonce so it can be echoed in the
-- id_token nonce claim (OIDC Core §3.1.3.7). Without storing it, a compliant
-- RP that enforces nonce (replay defense) rejects every id_token we issue.
--
-- The nonce is captured at /oauth/authorize, written to the authorize
-- transaction, copied into the one-shot code at approval, and read back at
-- token mint. Two columns, one per hop.
--
-- Both are nullable TEXT (no default): the RP may legitimately omit nonce, and
-- legacy rows predating this migration simply have NULL -> no nonce claim,
-- which is correct for a request that never supplied one.

ALTER TABLE oauth_authorize_transactions
    ADD COLUMN IF NOT EXISTS nonce TEXT;

ALTER TABLE oauth_codes
    ADD COLUMN IF NOT EXISTS nonce TEXT;
