-- OIDC session ID (sid) for back-channel logout session tracking.
-- The sid is generated at /oauth/authorize, stored through the code, and
-- included in id_tokens and logout_tokens so RPs can correlate sessions.

-- Add sid to authorize transactions (captured at consent time).
ALTER TABLE oauth_authorize_transactions
    ADD COLUMN IF NOT EXISTS sid TEXT;

-- Add sid to authorization codes (carried from the transaction at approval).
ALTER TABLE oauth_codes
    ADD COLUMN IF NOT EXISTS sid TEXT;
