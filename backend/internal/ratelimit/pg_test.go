package ratelimit

import (
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pashagolub/pgxmock/v4"
)

// newPgMock builds a Postgres-backed Limiter over a mock pool without starting
// the background sweep goroutine.
func newPgMock(t *testing.T) (*Limiter, pgxmock.PgxPoolIface) {
	t.Helper()
	mock, err := pgxmock.NewPool()
	if err != nil {
		t.Fatalf("pgxmock.NewPool: %v", err)
	}
	t.Cleanup(mock.Close)
	return &Limiter{windows: make(map[string]*entry), pool: mock}, mock
}

// The regression that took the whole read API down: the first request of every
// window finds no counter row, and pgx reports that as ErrNoRows. Treating it
// as a DB failure returned 0 remaining, which made
// api.tieredRateLimitMiddleware 429 the request and short-circuit Allow(), so
// the row was never created and every /api/v1 + /graphql call 429'd forever.
func TestRemainingPgNoRowMeansFullBudget(t *testing.T) {
	l, mock := newPgMock(t)
	mock.ExpectQuery(`SELECT count FROM rate_limits`).
		WithArgs("api|1.2.3.4", pgxmock.AnyArg()).
		WillReturnError(pgx.ErrNoRows)

	if got := l.Remaining("api|1.2.3.4", 60, time.Minute); got != 60 {
		t.Fatalf("Remaining on a fresh window = %d, want 60 (full budget)", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// A real DB outage must still fail closed, matching allowPg's rejection.
func TestRemainingPgDBErrorFailsClosed(t *testing.T) {
	l, mock := newPgMock(t)
	mock.ExpectQuery(`SELECT count FROM rate_limits`).
		WithArgs("api|1.2.3.4", pgxmock.AnyArg()).
		WillReturnError(errors.New("connection refused"))

	if got := l.Remaining("api|1.2.3.4", 60, time.Minute); got != 0 {
		t.Fatalf("Remaining during a DB outage = %d, want 0 (fail closed)", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRemainingPgSubtractsStoredCount(t *testing.T) {
	l, mock := newPgMock(t)
	mock.ExpectQuery(`SELECT count FROM rate_limits`).
		WithArgs("api|1.2.3.4", pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(59))

	if got := l.Remaining("api|1.2.3.4", 60, time.Minute); got != 1 {
		t.Fatalf("Remaining with 59 spent = %d, want 1", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRemainingPgClampsAtZeroWhenOverLimit(t *testing.T) {
	l, mock := newPgMock(t)
	mock.ExpectQuery(`SELECT count FROM rate_limits`).
		WithArgs("api|1.2.3.4", pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(99))

	if got := l.Remaining("api|1.2.3.4", 60, time.Minute); got != 0 {
		t.Fatalf("Remaining past the limit = %d, want 0", got)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

// Replays the exact middleware sequence (Remaining, then Allow) across a cold
// window so the two halves cannot regress independently.
func TestPgMiddlewareSequenceAdmitsFirstRequest(t *testing.T) {
	l, mock := newPgMock(t)
	mock.ExpectQuery(`SELECT count FROM rate_limits`).
		WithArgs("api|1.2.3.4", pgxmock.AnyArg()).
		WillReturnError(pgx.ErrNoRows)
	mock.ExpectQuery(`INSERT INTO rate_limits`).
		WithArgs("api|1.2.3.4", pgxmock.AnyArg()).
		WillReturnRows(pgxmock.NewRows([]string{"count"}).AddRow(1))

	remaining := l.Remaining("api|1.2.3.4", 60, time.Minute)
	if remaining <= 0 {
		t.Fatalf("remaining = %d, so the middleware would 429 before ever calling Allow", remaining)
	}
	if !l.Allow("api|1.2.3.4", 60, time.Minute) {
		t.Fatal("Allow rejected the first request of a fresh window")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
