-- 146_oauth_authorize_transaction_user_id.sql
--
-- §10.3 / §18.7: bind oauth_authorize_transactions to the authenticated user.
--
-- Without this column the approve POST cannot prove the JWT subject matches
-- the user that opened /authorize, so a leaked or replayed transaction_id +
-- CSRF cookie pair could be approved by another logged-in user. The service
-- layer's ApproveAuthorization now compares userIDFromJWT against this row.
--
-- Idempotent. Defaults zero-fills any pre-existing rows; the rendering window
-- is only 10 minutes so production rows old enough to lack a user_id will
-- have already expired by the time this runs.

ALTER TABLE oauth_authorize_transactions
    ADD COLUMN IF NOT EXISTS user_id BIGINT NOT NULL DEFAULT 0;

-- Drop the default once back-fill is no longer needed; new rows always
-- write user_id from the service layer.
ALTER TABLE oauth_authorize_transactions
    ALTER COLUMN user_id DROP DEFAULT;

CREATE INDEX IF NOT EXISTS idx_oauth_authorize_transactions_user_id
    ON oauth_authorize_transactions(user_id);
