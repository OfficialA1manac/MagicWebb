package db

import (
	"context"
	"testing"
	"time"

	"github.com/pashagolub/pgxmock/v4"
)

// The badge's stickiness lives entirely in this UPDATE: `verified` is derived
// from an EXISTS over nft_metadata rather than passed in by the sweeper. If the
// EXISTS clause is ever dropped and replaced by a caller-supplied bool, an IPFS
// gateway outage becomes a site-wide badge outage. Lock it in.
func TestSetCollectionVerificationDerivesVerifiedFromMetadata(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	q := New(mock)

	mock.ExpectExec(`UPDATE collections c\s+SET standard_verified = \$2,\s+verification_checked_at = now\(\),\s+verified = \$2 AND EXISTS \(\s+SELECT 1 FROM nft_metadata m WHERE m\.collection = c\.address\s+\)`).
		WithArgs("0xcoll", true).
		WillReturnResult(pgxmock.NewResult("UPDATE", 1))

	if err := q.SetCollectionVerification(context.Background(), "0xcoll", true); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// Never-checked collections must sort ahead of merely stale ones, otherwise a
// large backlog of 24h-old rows starves brand-new collections of their first
// probe forever.
func TestListCollectionsForVerificationOrdersNullsFirst(t *testing.T) {
	mock, _ := pgxmock.NewPool()
	defer mock.Close()
	q := New(mock)

	cutoff := time.Now().Add(-24 * time.Hour)
	mock.ExpectQuery(`SELECT address FROM collections\s+WHERE verification_checked_at IS NULL\s+OR verification_checked_at < \$1\s+ORDER BY verification_checked_at NULLS FIRST\s+LIMIT \$2`).
		WithArgs(cutoff, 25).
		WillReturnRows(pgxmock.NewRows([]string{"address"}).AddRow("0xa").AddRow("0xb"))

	got, err := q.ListCollectionsForVerification(context.Background(), cutoff, 25)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0] != "0xa" {
		t.Fatalf("got %v, want [0xa 0xb]", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
