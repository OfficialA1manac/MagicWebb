-- 028_auth_audit_log
-- Captures every authentication event (login success, login failure, token
-- refresh) for incident response, brute-force detection, and compliance.
-- Designed for append-only writes with fire-and-forget async insertion so
-- audit logging never blocks the auth critical path.
--
-- Indexes:
--   wallet_addr + created_at: enables incident response queries ("all events
--     for 0xABC in the last 24h") and brute-force pattern detection.
--   created_at: supports retention policies (DELETE WHERE created_at < ...).
--   outcome: enables dashboard queries like "failed logins in the last hour".
CREATE TABLE IF NOT EXISTS auth_audit_log (
    id           BIGSERIAL PRIMARY KEY,
    event_type   TEXT        NOT NULL,  -- 'login_success', 'login_failed', 'refresh_success', 'refresh_failed'
    wallet_addr  TEXT        NOT NULL,  -- lowercased 0x-prefixed Ethereum address
    ip           TEXT        NOT NULL,  -- client IP from Fly-Client-IP / X-Forwarded-For
    user_agent   TEXT        NOT NULL DEFAULT '',
    outcome      TEXT        NOT NULL,  -- 'success', 'failure'; derived from event_type
    details      JSONB       NOT NULL DEFAULT '{}',  -- structured reason (e.g. {"reason":"invalid_signature","domain":"...")
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Incident response: query all events for a wallet in a time window.
CREATE INDEX IF NOT EXISTS idx_auth_audit_wallet_time
    ON auth_audit_log (wallet_addr, created_at DESC);

-- Retention policy / time-range dashboard queries.
CREATE INDEX IF NOT EXISTS idx_auth_audit_created_at
    ON auth_audit_log (created_at DESC);

-- Dashboard: count failures grouped by event_type in a time window.
CREATE INDEX IF NOT EXISTS idx_auth_audit_outcome
    ON auth_audit_log (outcome, created_at DESC);
