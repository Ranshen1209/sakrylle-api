-- Versioned time-of-day pricing for channel token price cards.
-- Existing channel_model_pricing fields remain the fallback whenever no
-- version is active, preserving all current channels by default.
CREATE TABLE IF NOT EXISTS channel_model_pricing_versions (
    id BIGSERIAL PRIMARY KEY,
    pricing_id BIGINT NOT NULL REFERENCES channel_model_pricing(id) ON DELETE CASCADE,
    effective_from TIMESTAMPTZ NOT NULL,
    effective_until TIMESTAMPTZ,
    timezone VARCHAR(64) NOT NULL DEFAULT 'Asia/Shanghai',
    default_multiplier DECIMAL(10,4) NOT NULL DEFAULT 1.0,
    input_price DECIMAL(20,12),
    output_price DECIMAL(20,12),
    cache_write_price DECIMAL(20,12),
    cache_read_price DECIMAL(20,12),
    image_input_price DECIMAL(20,12),
    image_output_price DECIMAL(20,12),
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT channel_model_pricing_versions_effective_range
        CHECK (effective_until IS NULL OR effective_until > effective_from),
    CONSTRAINT channel_model_pricing_versions_default_multiplier
        CHECK (default_multiplier >= 0),
    UNIQUE (pricing_id, effective_from)
);

CREATE INDEX IF NOT EXISTS idx_channel_model_pricing_versions_pricing_time
    ON channel_model_pricing_versions (pricing_id, effective_from, effective_until);

CREATE TABLE IF NOT EXISTS channel_pricing_time_windows (
    id BIGSERIAL PRIMARY KEY,
    version_id BIGINT NOT NULL REFERENCES channel_model_pricing_versions(id) ON DELETE CASCADE,
    label VARCHAR(50) NOT NULL DEFAULT '',
    weekdays SMALLINT NOT NULL DEFAULT 127,
    start_minute SMALLINT NOT NULL,
    end_minute SMALLINT NOT NULL,
    multiplier DECIMAL(10,4) NOT NULL DEFAULT 1.0,
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT channel_pricing_time_windows_weekdays CHECK (weekdays BETWEEN 1 AND 127),
    CONSTRAINT channel_pricing_time_windows_start CHECK (start_minute BETWEEN 0 AND 1439),
    CONSTRAINT channel_pricing_time_windows_end CHECK (end_minute BETWEEN 1 AND 1440),
    CONSTRAINT channel_pricing_time_windows_range CHECK (start_minute < end_minute),
    CONSTRAINT channel_pricing_time_windows_multiplier CHECK (multiplier >= 0)
);

CREATE INDEX IF NOT EXISTS idx_channel_pricing_time_windows_version
    ON channel_pricing_time_windows (version_id, sort_order, id);

COMMENT ON TABLE channel_model_pricing_versions IS '渠道模型价卡的定时生效版本；无版本生效时沿用 channel_model_pricing 固定价';
COMMENT ON TABLE channel_pricing_time_windows IS '价卡版本的日内倍率窗口；未命中窗口时使用版本 default_multiplier';
