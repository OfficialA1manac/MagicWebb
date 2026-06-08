-- +goose Up
-- +goose StatementBegin
-- Shared fixed-window rate limiter. Atomic UPSERT increments the per-(key,window)
-- counter; one row per active window. Swept hourly by the backend.
CREATE TABLE rate_limits (
    rl_key       TEXT        NOT NULL,
    window_start TIMESTAMPTZ NOT NULL,
    count        INT         NOT NULL DEFAULT 0,
    PRIMARY KEY (rl_key, window_start)
);
-- Service-role only.
ALTER TABLE rate_limits ENABLE ROW LEVEL SECURITY;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS rate_limits;
-- +goose StatementEnd
