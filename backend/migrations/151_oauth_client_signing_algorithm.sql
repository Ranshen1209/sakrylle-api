-- Add signing_algorithm column to oauth_clients for per-client algorithm selection.
-- Defaults to RS256 for backward compatibility. ES256 is now supported.

ALTER TABLE oauth_clients
    ADD COLUMN IF NOT EXISTS signing_algorithm VARCHAR(16) NOT NULL DEFAULT 'RS256';

-- Validate signing_algorithm values
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'oauth_clients_signing_algorithm_check'
    ) THEN
        ALTER TABLE oauth_clients
            ADD CONSTRAINT oauth_clients_signing_algorithm_check
            CHECK (signing_algorithm IN ('RS256', 'ES256'));
    END IF;
END$$;

-- Add comment for documentation
COMMENT ON COLUMN oauth_clients.signing_algorithm IS
    'JWS algorithm for id_token signing: RS256 (RSA PKCS#1 v1.5 with SHA-256) or ES256 (ECDSA P-256 with SHA-256). Defaults to RS256 for backward compatibility.';
