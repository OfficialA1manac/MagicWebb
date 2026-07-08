-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS gas_alert_history (
    id            BIGSERIAL PRIMARY KEY,
    total_cost_wei   TEXT NOT NULL,          -- aggregate keeper gas cost that triggered the alert
    threshold_wei    TEXT NOT NULL,          -- configured threshold at time of alert
    cost_flr         DOUBLE PRECISION,       -- cost in native tokens (human-readable)
    threshold_flr    DOUBLE PRECISION,       -- threshold in native tokens (human-readable)
    currency         TEXT NOT NULL,          -- native currency symbol (e.g. C2FLR, FLR, SGB)
    discord_sent     BOOLEAN NOT NULL DEFAULT false,
    prometheus_sent  BOOLEAN NOT NULL DEFAULT false,
    email_sent       BOOLEAN NOT NULL DEFAULT false,
    discord_error    TEXT,                   -- non-empty when the Discord webhook call failed
    prometheus_error TEXT,                   -- non-empty when the Prometheus webhook call failed
    email_error      TEXT,                   -- non-empty when the SMTP email send failed
    created_at       TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- +goose StatementEnd

-- Index for listing alerts newest-first.
CREATE INDEX IF NOT EXISTS idx_gas_alert_history_created_at
    ON gas_alert_history (created_at DESC);

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS gas_alert_history;
-- +goose StatementEnd
