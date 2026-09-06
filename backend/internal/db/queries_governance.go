package db

import (
	"context"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// GovernanceEventRow mirrors one governance_events row (migration 043).
type GovernanceEventRow struct {
	ID          int64     `json:"id"`
	ChainID     int64     `json:"chain_id"`
	BlockNumber int64     `json:"block_number"`
	TxHash      string    `json:"tx_hash"`
	LogIndex    int32     `json:"log_index"`
	Contract    string    `json:"contract"`
	Event       string    `json:"event"`
	Actor       string    `json:"actor"`
	Subject     string    `json:"subject"`
	Extra       string    `json:"extra"`
	BlockTime   time.Time `json:"block_time"`
}

// InsertGovernanceEvent appends one manager/upgrade event. Idempotent on
// (tx_hash, log_index) so cmd/reindexgov can replay history safely. Returns
// true when a new row was written (false = duplicate, already recorded).
func (q *Q) InsertGovernanceEvent(ctx context.Context, e GovernanceEventRow) (bool, error) {
	tag, err := q.writer().Exec(ctx,
		`INSERT INTO governance_events(chain_id, block_number, tx_hash, log_index, contract, event, actor, subject, extra, block_time)
		 VALUES($1,$2,$3,$4,$5,$6,NULLIF($7,''),NULLIF($8,''),NULLIF($9,''),$10)
		 ON CONFLICT (tx_hash, log_index) DO NOTHING`,
		e.ChainID, e.BlockNumber, strings.ToLower(e.TxHash), e.LogIndex,
		strings.ToLower(e.Contract), e.Event, strings.ToLower(e.Actor), strings.ToLower(e.Subject), e.Extra, e.BlockTime)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

// ListGovernanceEvents returns the newest events first (block, then log index).
func (q *Q) ListGovernanceEvents(ctx context.Context, chainID int64, limit int) ([]GovernanceEventRow, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := q.reader().Query(ctx,
		`SELECT id, chain_id, block_number, tx_hash, log_index, contract, event,
		        COALESCE(actor,''), COALESCE(subject,''), COALESCE(extra,''), block_time
		 FROM governance_events WHERE chain_id=$1
		 ORDER BY block_number DESC, log_index DESC LIMIT $2`, chainID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]GovernanceEventRow, 0, limit)
	for rows.Next() {
		var r GovernanceEventRow
		if err := rows.Scan(&r.ID, &r.ChainID, &r.BlockNumber, &r.TxHash, &r.LogIndex, &r.Contract, &r.Event,
			&r.Actor, &r.Subject, &r.Extra, &r.BlockTime); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// LatestImplementations returns the newest `Upgraded` subject (implementation
// address) per core proxy, from the governance log. Empty when no upgrade has
// been indexed yet (a fresh deploy emits Upgraded from the proxy constructor,
// so after cmd/reindexgov every proxy has one).
func (q *Q) LatestImplementations(ctx context.Context, chainID int64) (map[string]string, error) {
	rows, err := q.reader().Query(ctx,
		`SELECT DISTINCT ON (contract) contract, COALESCE(subject,'')
		 FROM governance_events
		 WHERE chain_id=$1 AND event='Upgraded'
		 ORDER BY contract, block_number DESC, log_index DESC`, chainID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var c, s string
		if err := rows.Scan(&c, &s); err != nil {
			return nil, err
		}
		out[c] = s
	}
	return out, rows.Err()
}

// ListSecurityAlertRecipients returns every known wallet that has not opted
// out of security notifications (profiles.security_alerts, default true).
// Capped so a governance event can never fan out unboundedly.
func (q *Q) ListSecurityAlertRecipients(ctx context.Context, limit int) ([]string, error) {
	if limit <= 0 || limit > 5000 {
		limit = 2000
	}
	rows, err := q.reader().Query(ctx,
		`SELECT u.address FROM users u
		 LEFT JOIN profiles p ON p.address = u.address
		 WHERE COALESCE(p.security_alerts, true)
		 ORDER BY u.last_seen_at DESC LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var a string
		if err := rows.Scan(&a); err != nil {
			return nil, err
		}
		out = append(out, strings.ToLower(strings.TrimSpace(a)))
	}
	return out, rows.Err()
}

// GetSecurityAlerts reads the opt-out flag for one wallet (true when no profile row).
func (q *Q) GetSecurityAlerts(ctx context.Context, addr string) (bool, error) {
	var on bool
	err := q.reader().QueryRow(ctx,
		`SELECT security_alerts FROM profiles WHERE address=$1`, strings.ToLower(addr)).Scan(&on)
	if err == pgx.ErrNoRows {
		return true, nil
	}
	return on, err
}

// SetSecurityAlerts writes the opt-out flag, creating the profile row if needed.
func (q *Q) SetSecurityAlerts(ctx context.Context, addr string, on bool) error {
	_, err := q.writer().Exec(ctx,
		`INSERT INTO profiles(address, security_alerts, updated_at) VALUES($1,$2,now())
		 ON CONFLICT(address) DO UPDATE SET security_alerts=EXCLUDED.security_alerts, updated_at=now()`,
		strings.ToLower(addr), on)
	return err
}
