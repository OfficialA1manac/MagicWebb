package auth

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ── Audit event types ────────────────────────────────────────────────────

const (
	EventLoginSuccess   = "login_success"
	EventLoginFailed    = "login_failed"
	EventRefreshSuccess = "refresh_success"
	EventRefreshFailed  = "refresh_failed"
)

// AuditEntry is a single row in auth_audit_log. All fields are required
// except Details which defaults to empty JSON object.
type AuditEntry struct {
	EventType  string `json:"event_type"`
	WalletAddr string `json:"wallet_addr"`
	IP         string `json:"ip"`
	UserAgent  string `json:"user_agent"`
	Outcome    string `json:"outcome"`  // "success" | "failure"
	Details    string `json:"details"`  // JSON-encoded map of extra context
}

// AuditLogger asynchronously persists auth audit entries without blocking
// the authentication critical path. Implementations must be goroutine-safe
// and must not return errors from Log() — fire-and-forget.
type AuditLogger interface {
	// Log enqueues an audit entry for async persistence. Never blocks.
	Log(entry AuditEntry)
	// Close drains the queue and shuts down the background worker.
	Close()
}

// PgAuditLogger writes audit entries to auth_audit_log via an internal
// channel, ensuring auth handlers (verify, refresh) never block on DB I/O.
// The internal buffer is 1024 entries; overflow entries are silently dropped
// (audit log is best-effort, not transactional).
type PgAuditLogger struct {
	pool *pgxpool.Pool
	ch   chan AuditEntry
	done chan struct{}
}

// NewPgAuditLogger starts a background goroutine that drains the audit
// channel and batch-inserts into auth_audit_log. The caller must call
// Close() before shutdown to flush remaining entries.
func NewPgAuditLogger(pool *pgxpool.Pool) *PgAuditLogger {
	l := &PgAuditLogger{
		pool: pool,
		ch:   make(chan AuditEntry, 1024),
		done: make(chan struct{}),
	}
	go l.worker()
	return l
}

func (l *PgAuditLogger) Log(entry AuditEntry) {
	select {
	case l.ch <- entry:
	default:
		// Buffer full — drop the entry silently. Audit is best-effort;
		// we must never block an auth handler on audit I/O.
	}
}

func (l *PgAuditLogger) Close() {
	close(l.ch)
	<-l.done
}

func (l *PgAuditLogger) worker() {
	defer close(l.done)

	ctx := context.Background()
	const insertSQL = `INSERT INTO auth_audit_log(event_type, wallet_addr, ip, user_agent, outcome, details)
		VALUES($1, $2, $3, $4, $5, $6)`

	for entry := range l.ch {
		// Best-effort insert with a short timeout so a hung DB doesn't
		// stall the worker indefinitely.
		insCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		_, _ = l.pool.Exec(insCtx, insertSQL,
			entry.EventType,
			entry.WalletAddr,
			entry.IP,
			entry.UserAgent,
			entry.Outcome,
			entry.Details,
		)
		cancel()
	}
}

// ── Convenience helpers ──────────────────────────────────────────────────

// AuditLoginSuccess logs a successful SIWE login (POST /auth/verify).
func AuditLoginSuccess(log AuditLogger, addr, ip, ua string) {
	log.Log(AuditEntry{
		EventType:  EventLoginSuccess,
		WalletAddr: addr,
		IP:         ip,
		UserAgent:  ua,
		Outcome:    "success",
		Details:    "{}",
	})
}

// AuditLoginFailed logs a failed SIWE login with a structured reason.
// reason should be a short machine-readable key (e.g. "invalid_signature",
// "domain_mismatch", "nonce_consumed").
func AuditLoginFailed(log AuditLogger, addr, ip, ua, reason string, extra map[string]string) {
	details := map[string]string{"reason": reason}
	for k, v := range extra {
		details[k] = v
	}
	b, _ := json.Marshal(details)
	log.Log(AuditEntry{
		EventType:  EventLoginFailed,
		WalletAddr: addr,
		IP:         ip,
		UserAgent:  ua,
		Outcome:    "failure",
		Details:    string(b),
	})
}

// AuditRefreshSuccess logs a successful token rotation (POST /auth/refresh).
func AuditRefreshSuccess(log AuditLogger, addr, ip, ua string) {
	log.Log(AuditEntry{
		EventType:  EventRefreshSuccess,
		WalletAddr: addr,
		IP:         ip,
		UserAgent:  ua,
		Outcome:    "success",
		Details:    "{}",
	})
}

// AuditRefreshFailed logs a failed token rotation with a structured reason.
func AuditRefreshFailed(log AuditLogger, addr, ip, ua, reason string, extra map[string]string) {
	details := map[string]string{"reason": reason}
	for k, v := range extra {
		details[k] = v
	}
	b, _ := json.Marshal(details)
	log.Log(AuditEntry{
		EventType:  EventRefreshFailed,
		WalletAddr: addr,
		IP:         ip,
		UserAgent:  ua,
		Outcome:    "failure",
		Details:    string(b),
	})
}
