package auth

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// ── Audit event types ────────────────────────────────────────────────────

const (
	EventLoginSuccess   = "login_success"
	EventLoginFailed    = "login_failed"
	EventRefreshSuccess = "refresh_success"
	EventRefreshFailed  = "refresh_failed"
	EventLogoutSuccess  = "logout_success"
	EventLogoutFailed   = "logout_failed"
)

// AuditEntry is a single row in auth_audit_log. All fields are required
// except Details which defaults to empty JSON object.
type AuditEntry struct {
	EventType  string `json:"event_type"`
	WalletAddr string `json:"wallet_addr"`
	IP         string `json:"ip"`
	UserAgent  string `json:"user_agent"`
	Outcome    string `json:"outcome"` // "success" | "failure"
	Details    string `json:"details"` // JSON-encoded map of extra context
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
	pool      *pgxpool.Pool
	ch        chan AuditEntry
	done      chan struct{}
	quit      chan struct{}
	closeOnce sync.Once
}

// auditRetention is how long auth audit rows (wallet_addr + ip + user_agent —
// personal data under GDPR/CCPA) are kept before the retention sweeper
// deletes them. 90 days balances incident-response needs against data
// minimization; migration 028's created_at index makes the delete cheap.
const auditRetention = "90 days"

// NewPgAuditLogger starts a background goroutine that drains the audit
// channel and batch-inserts into auth_audit_log. The caller must call
// Close() before shutdown to flush remaining entries.
func NewPgAuditLogger(pool *pgxpool.Pool) *PgAuditLogger {
	l := &PgAuditLogger{
		pool: pool,
		ch:   make(chan AuditEntry, 1024),
		done: make(chan struct{}),
		quit: make(chan struct{}),
	}
	go l.worker()
	go l.retentionSweeper()
	return l
}

// retentionSweeper enforces the documented retention window: rows older than
// auditRetention are deleted shortly after boot and then once a day. Without
// this the table grows without bound and retains identifiable data forever.
func (l *PgAuditLogger) retentionSweeper() {
	sweep := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, _ = l.pool.Exec(ctx,
			`DELETE FROM auth_audit_log WHERE created_at < now() - interval '`+auditRetention+`'`)
	}
	first := time.NewTimer(time.Minute)
	defer first.Stop()
	daily := time.NewTicker(24 * time.Hour)
	defer daily.Stop()
	for {
		select {
		case <-l.quit:
			return
		case <-first.C:
			sweep()
		case <-daily.C:
			sweep()
		}
	}
}

func (l *PgAuditLogger) Log(entry AuditEntry) {
	// Apply the documented Details default. details is JSONB NOT NULL
	// (migration 028); sending "" explicitly bypasses the column default and
	// fails the insert with an invalid-JSON error.
	if entry.Details == "" {
		entry.Details = "{}"
	}

	// After Close, drop entries instead of sending. The send channel is
	// never closed (a send on a closed channel would panic inside an auth
	// handler racing shutdown); shutdown is signalled via quit only.
	select {
	case <-l.quit:
		return
	default:
	}
	select {
	case l.ch <- entry:
	default:
		// Buffer full — drop the entry silently. Audit is best-effort;
		// we must never block an auth handler on audit I/O.
	}
}

func (l *PgAuditLogger) Close() {
	l.closeOnce.Do(func() { close(l.quit) })
	<-l.done
}

func (l *PgAuditLogger) worker() {
	defer close(l.done)

	for {
		select {
		case entry := <-l.ch:
			l.insert(entry)
		case <-l.quit:
			// Drain whatever is already buffered, then stop.
			for {
				select {
				case entry := <-l.ch:
					l.insert(entry)
				default:
					return
				}
			}
		}
	}
}

// insert writes one audit row. Best-effort with a short timeout so a hung DB
// doesn't stall the worker indefinitely.
func (l *PgAuditLogger) insert(entry AuditEntry) {
	const insertSQL = `INSERT INTO auth_audit_log(event_type, wallet_addr, ip, user_agent, outcome, details)
		VALUES($1, $2, $3, $4, $5, $6)`

	insCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, _ = l.pool.Exec(insCtx, insertSQL,
		entry.EventType,
		entry.WalletAddr,
		entry.IP,
		entry.UserAgent,
		entry.Outcome,
		entry.Details,
	)
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

// AuditLogoutSuccess logs a completed logout (POST /auth/logout) — the
// refresh-token family was revoked and cookies cleared.
func AuditLogoutSuccess(log AuditLogger, addr, ip, ua string) {
	log.Log(AuditEntry{
		EventType:  EventLogoutSuccess,
		WalletAddr: addr,
		IP:         ip,
		UserAgent:  ua,
		Outcome:    "success",
		Details:    "{}",
	})
}

// AuditLogoutFailed logs a logout whose family revocation failed — the
// session cookies were cleared client-side but the refresh family may
// still be valid server-side. Security-relevant: a stolen refresh token
// could still mint access tokens until the family is revoked.
func AuditLogoutFailed(log AuditLogger, addr, ip, ua, reason string, extra map[string]string) {
	details := map[string]string{"reason": reason}
	for k, v := range extra {
		details[k] = v
	}
	b, _ := json.Marshal(details)
	log.Log(AuditEntry{
		EventType:  EventLogoutFailed,
		WalletAddr: addr,
		IP:         ip,
		UserAgent:  ua,
		Outcome:    "failure",
		Details:    string(b),
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
