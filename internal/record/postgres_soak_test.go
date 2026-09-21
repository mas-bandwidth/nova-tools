package record_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/record"
)

// TestPostgresStoreSoak is the real-Postgres half of the storage contract. It is the soak
// the card names: with RECORD_TEST_PG set to a DSN it runs the same insert-idempotent and
// filter promises against a live server; with the variable unset it is skipped, so the unit
// suite never reaches the network. The fake store is what makes the unit tests fast; this is
// what keeps the fake honest.
func TestPostgresStoreSoak(t *testing.T) {
	dsn := os.Getenv("RECORD_TEST_PG")
	if dsn == "" {
		t.Skip("set RECORD_TEST_PG to a Postgres DSN to run the storage soak")
	}
	ctx := context.Background()
	store, err := record.OpenPostgres(dsn)
	if err != nil {
		t.Fatalf("open postgres: %s", err)
	}
	defer store.Close()
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %s", err)
	}
	if err := store.Migrate(ctx); err != nil {
		t.Fatalf("second migrate: %s", err)
	}
	id := "soak-" + time.Now().UTC().Format("20060102150405.000000000")
	row := record.Row{
		StreamID: id, Label: "soak", Bench: "space", Exit: 1,
		ResultLine: "soak red", JobPath: "/jobs/soak", Commit: "cafe", Branch: "b",
		RecordedAt: time.Now().UTC(),
	}
	first, err := store.Insert(ctx, row)
	if err != nil {
		t.Fatalf("insert: %s", err)
	}
	second, err := store.Insert(ctx, row)
	if err != nil {
		t.Fatalf("second insert: %s", err)
	}
	if !first || second {
		t.Fatalf("inserted flags = %v, %v; want true, false", first, second)
	}
	rows, err := store.List(ctx, record.Filter{Bench: "space", Failed: true})
	if err != nil {
		t.Fatalf("list: %s", err)
	}
	for _, got := range rows {
		if got.StreamID == id {
			return
		}
	}
	t.Fatalf("the soaked row %s is not in the filtered list", id)
}
