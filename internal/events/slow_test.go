//go:build slow

// The tests of this package that cost more than the per-commit run can pay:
// over five seconds each on the Linux bench, or a deadline, wedge or wall-clock
// bound proved by waiting it out. They are behind the `slow` build tag, so
// go-test-cmd and go-test-internal do not build them, and
// .github/workflows/nightly-slow.yml (and `make test-slow`) runs them whole,
// every night. Each carries the measurement that moved it. Nothing here is
// skipped or weakened.

package events

import (
	"context"
	"database/sql"
	"testing"
)

// SLOW: 6.2 s on hetzner at dev 64b9bec48, over the five-second line.
func TestOpenDBUpgradesOldViews(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	reopen := func(t *testing.T, path string) foldState {
		t.Helper()
		db, err := OpenDB(ctx, path)
		if err != nil {
			t.Fatalf("reopening under the new schema: %v", err)
		}
		defer func() { _ = db.Close() }()
		return readFoldState(t, db.db)
	}

	t.Run("v2", func(t *testing.T) {
		path := oldFold(t, "schema-v2.sql", true)
		raw, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		if got := queryString(t, raw, `SELECT usd_per_landed FROM by_model_route`); got != "5" && got != "5.0" {
			t.Fatalf("before the upgrade by_model_route.usd_per_landed = %q, want the old number 5", got)
		}
		wantByKind := "rebase,1,1,0,1,0,0,NULL,NULL"
		if got := queryString(t, raw, `SELECT kind || ',' || decisions || ',' || units || ',' || stepped_up || ',' || escalated || ',' ||
			refused || ',' || calls || ',' || quote(tokens_in) || ',' || quote(tokens_out) FROM decisions_by_kind`); got != wantByKind {
			t.Fatalf("before the upgrade decisions_by_kind = %q, want %q", got, wantByKind)
		}
		wantDecision := queryString(t, raw, `SELECT quote(json_array(`+decisionRowColumns+`)) FROM decisions`)
		wantCounts := queryString(t, raw, `SELECT (SELECT count(*) FROM attempts) || ',' || (SELECT count(*) FROM reads) || ',' ||
			(SELECT count(*) FROM landings) || ',' || (SELECT count(*) FROM decisions)`)
		_ = raw.Close()

		got := reopen(t, path)
		want := foldState{
			versions: "1,2,3", counts: wantCounts, totals: threeCardBound, byModelRoute: threeCardBound,
			decision: wantDecision, byKind: wantByKind,
		}
		if got != want {
			t.Fatalf("after the upgrade\n got %+v\nwant %+v", got, want)
		}
		if again := reopen(t, path); again != want {
			t.Fatalf("a second reopen is not a no-op\n got %+v\nwant %+v", again, want)
		}
	})

	t.Run("v1", func(t *testing.T) {
		path := oldFold(t, "schema-v1.sql", false)
		raw, err := sql.Open("sqlite", path)
		if err != nil {
			t.Fatal(err)
		}
		if got := queryString(t, raw, `SELECT usd_per_landed FROM by_model_route`); got != "5" && got != "5.0" {
			t.Fatalf("before the upgrade by_model_route.usd_per_landed = %q, want the old number 5", got)
		}
		_ = raw.Close()
		got := reopen(t, path)
		want := foldState{
			versions: "1,2,3", counts: "3,0,1,0", totals: threeCardBound, byModelRoute: threeCardBound,
			decision: noRow, byKind: noRow,
		}
		if got != want {
			t.Fatalf("after the upgrade\n got %+v\nwant %+v", got, want)
		}
		if again := reopen(t, path); again != want {
			t.Fatalf("a second reopen is not a no-op\n got %+v\nwant %+v", again, want)
		}
	})
}
