-- Migration 167: Agiso Xianyu automatic delivery bridge order state
-- Created: 2026-06-15

CREATE TABLE IF NOT EXISTS agiso_orders (
    id BIGSERIAL PRIMARY KEY,
    biz_order_id VARCHAR(64) NOT NULL UNIQUE,
    item_id VARCHAR(64) NOT NULL DEFAULT '',
    buyer_id VARCHAR(128) NOT NULL DEFAULT '',
    payment_cents BIGINT NOT NULL DEFAULT 0,
    value NUMERIC(20,8) NOT NULL DEFAULT 0,
    redeem_code_id BIGINT NULL REFERENCES redeem_codes(id) ON DELETE SET NULL,
    code VARCHAR(64) NOT NULL DEFAULT '',
    detail_fetched BOOLEAN NOT NULL DEFAULT FALSE,
    code_minted BOOLEAN NOT NULL DEFAULT FALSE,
    msg_sent BOOLEAN NOT NULL DEFAULT FALSE,
    shipped BOOLEAN NOT NULL DEFAULT FALSE,
    status VARCHAR(20) NOT NULL DEFAULT 'partial',
    raw_json JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    delivered_at TIMESTAMPTZ NULL
);

CREATE INDEX IF NOT EXISTS idx_agiso_orders_status ON agiso_orders(status);
CREATE INDEX IF NOT EXISTS idx_agiso_orders_redeem_code_id ON agiso_orders(redeem_code_id);

COMMENT ON TABLE agiso_orders IS 'Agiso Xianyu automatic delivery idempotency and step state';
