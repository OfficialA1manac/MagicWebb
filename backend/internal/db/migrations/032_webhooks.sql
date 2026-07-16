-- +goose Up
-- +goose StatementBegin

-- WH-3: Webhook configurations — users register URLs to receive marketplace
-- event notifications when listings, auctions, offers, or activity events
-- occur. Each config subscribes to one or more event types and includes an
-- optional HMAC secret for payload signing.
CREATE TABLE webhook_configs (
    id          BIGSERIAL       PRIMARY KEY,
    user_addr   TEXT            NOT NULL,
    url         TEXT            NOT NULL,
    secret      TEXT,                         -- HMAC secret for X-Webhook-Signature
    events      TEXT[]          NOT NULL DEFAULT '{}',  -- ["listing.created", ...]
    active      BOOLEAN         NOT NULL DEFAULT true,
    created_at  TIMESTAMPTZ     NOT NULL DEFAULT now(),
    UNIQUE(user_addr, url)
);

-- WH-3: Delivery audit log — records every webhook delivery attempt for
-- debugging and rate-limit enforcement. Successful deliveries are retained
-- for 30 days; failed deliveries for 90 days (for debugging).
CREATE TABLE webhook_delivery_log (
    id              BIGSERIAL       PRIMARY KEY,
    config_id       BIGINT          NOT NULL REFERENCES webhook_configs(id) ON DELETE CASCADE,
    event_type      TEXT            NOT NULL,   -- "listing.created", etc.
    status_code     INT,                        -- HTTP response code (NULL if network error)
    error_message   TEXT,                       -- error or response body snippet
    attempt_count   INT             NOT NULL DEFAULT 1,
    duration_ms     INT             NOT NULL DEFAULT 0,
    created_at      TIMESTAMPTZ     NOT NULL DEFAULT now()
);

CREATE INDEX idx_webhook_configs_user ON webhook_configs(user_addr) WHERE active = true;
CREATE INDEX idx_webhook_configs_event ON webhook_configs USING GIN(events) WHERE active = true;
CREATE INDEX idx_webhook_delivery_config ON webhook_delivery_log(config_id, created_at DESC);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS webhook_delivery_log;
DROP TABLE IF EXISTS webhook_configs;
-- +goose StatementEnd
