package db

import (
	"context"
	"strings"
	"testing"
	"time"
)

// Migrate is bounded by MIGRATE_TIMEOUT so a hung migration (lock held by a
// sibling instance, unreachable database) fails the boot with an actionable
// message instead of wedging the rolling deploy forever.
//
// A nanosecond timeout expires before the first dial, so the test is
// deterministic — no real database, no network wait.
func TestMigrate_TimeoutProducesClearError(t *testing.T) {
	err := Migrate(context.Background(),
		"postgres://user:pass@127.0.0.1:1/db?sslmode=disable", time.Nanosecond)
	if err == nil {
		t.Fatal("Migrate with an expired deadline must fail")
	}
	if !strings.Contains(err.Error(), "MIGRATE_TIMEOUT") {
		t.Fatalf("timeout error must mention MIGRATE_TIMEOUT for the operator; got: %v", err)
	}
}
