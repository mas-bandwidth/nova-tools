package events

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The $/landed lower bound (nova-tools #3159): a fold figure is exact only when every card
// behind it is priced; below that it prints `>=<x> coverage=<p>% (<priced>/<cards>)`.

const (
	threeCardBound = ">=5.00 coverage=66.67% (2/3)"
	threeCardExact = "=6.00 coverage=100.00% (3/3)"
)

// loadSQL runs one testdata SQL file against a fold file's connection.
func loadSQL(t *testing.T, db *sql.DB, name string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(context.Background(), string(b)); err != nil {
		t.Fatalf("loading %s: %v", name, err)
	}
}

// noRow is what queryString reads when the query returns no row at all.
const noRow = "(no row)"

// queryString reads one text cell; a NULL reads as the dash.
func queryString(t *testing.T, db *sql.DB, query string) string {
	t.Helper()
	var s sql.NullString
	err := db.QueryRowContext(context.Background(), query).Scan(&s)
	if errors.Is(err, sql.ErrNoRows) {
		return noRow
	}
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	if !s.Valid {
		return "-"
	}
	return s.String
}

func TestFold20260922UsdPerLandedIsLowerBound(t *testing.T) {
	t.Parallel()
	db := openFold(t, "fold-2026-09-22.sqlite")
	loadSQL(t, db.db, "fold-2026-09-22.sql")

	// The fixture is the shape the spec names: 1902 cards, 10 unpriced, 11 landed, $619.03.
	for query, want := range map[string]string{
		`SELECT cards FROM totals`:                                        "1902",
		`SELECT priced_cards FROM totals`:                                 "1892",
		`SELECT landed FROM totals`:                                       "11",
		`SELECT printf('%.2f', usd) FROM totals`:                          "619.03",
		`SELECT count(*) FROM attempts WHERE usd IS NULL AND kind = 'ok'`: "10",
	} {
		if got := queryString(t, db.db, query); got != want {
			t.Errorf("%s = %s, want %s", query, got, want)
		}
	}
	got := queryString(t, db.db, `SELECT 'usd_per_landed' || usd_per_landed FROM totals`)
	if want := "usd_per_landed>=56.28 coverage=99.47% (1892/1902)"; got != want {
		t.Fatalf("the 2026-09-22 fold's $/landed\n got %q\nwant %q", got, want)
	}
}

func TestFoldUsdPerLandedExactOnlyAtFullCoverage(t *testing.T) {
	t.Parallel()

	t.Run("bound below 100%", func(t *testing.T) {
		db := openFold(t, "bound.sqlite")
		loadSQL(t, db.db, "three-card.sql")
		for _, q := range []string{
			`SELECT usd_per_landed FROM totals`,
			`SELECT usd_per_landed FROM by_model_route WHERE model = 'fable' AND route = 'studio'`,
		} {
			if got := queryString(t, db.db, q); got != threeCardBound {
				t.Errorf("%s = %q, want %q", q, got, threeCardBound)
			}
		}
		if got := queryString(t, db.db, `SELECT cards || '/' || priced_cards FROM by_model_route`); got != "3/2" {
			t.Errorf("by_model_route cards/priced_cards = %s, want 3/2", got)
		}
	})

	t.Run("exact at 100%", func(t *testing.T) {
		db := openFold(t, "exact.sqlite")
		loadSQL(t, db.db, "three-card-priced.sql")
		for _, q := range []string{
			`SELECT usd_per_landed FROM totals`,
			`SELECT usd_per_landed FROM by_model_route WHERE model = 'fable' AND route = 'studio'`,
		} {
			if got := queryString(t, db.db, q); got != threeCardExact {
				t.Errorf("%s = %q, want %q", q, got, threeCardExact)
			}
		}
	})

	// A queued row never carries usd; the card is priced because attempt 1 has a priced row.
	t.Run("queued and ok at one attempt is priced", func(t *testing.T) {
		db := openFold(t, "queued.sqlite")
		if _, err := db.db.Exec(`INSERT INTO attempts (event_id, label, attempt, model, route, kind, usd) VALUES
			('1-0', 'c1', 1, 'fable', 'studio', 'queued', NULL),
			('2-0', 'c1', 1, 'fable', 'studio', 'ok', 2.00),
			('3-0', 'c2', 1, 'fable', 'studio', 'ok', 3.00);
			INSERT INTO landings (event_id, label, kind) VALUES ('4-0', 'c1', 'landed');`); err != nil {
			t.Fatal(err)
		}
		want := "=5.00 coverage=100.00% (2/2)"
		for _, q := range []string{`SELECT usd_per_landed FROM totals`, `SELECT usd_per_landed FROM by_model_route`} {
			if got := queryString(t, db.db, q); got != want {
				t.Errorf("%s = %q, want %q", q, got, want)
			}
		}
	})

	// Attempt 2 was never priced, so the card's cost is unknown: the figure is a bound.
	t.Run("an attempt with only NULL rows is unpriced", func(t *testing.T) {
		db := openFold(t, "attempt2.sqlite")
		if _, err := db.db.Exec(`INSERT INTO attempts (event_id, label, attempt, model, route, kind, usd) VALUES
			('1-0', 'c1', 1, 'fable', 'studio', 'ok', 2.00),
			('2-0', 'c1', 2, 'fable', 'studio', 'queued', NULL),
			('3-0', 'c1', 2, 'fable', 'studio', 'fail', NULL),
			('4-0', 'c2', 1, 'fable', 'studio', 'ok', 3.00);
			INSERT INTO landings (event_id, label, kind) VALUES ('5-0', 'c2', 'landed');`); err != nil {
			t.Fatal(err)
		}
		want := ">=5.00 coverage=50.00% (1/2)"
		for _, q := range []string{`SELECT usd_per_landed FROM totals`, `SELECT usd_per_landed FROM by_model_route`} {
			if got := queryString(t, db.db, q); got != want {
				t.Errorf("%s = %q, want %q", q, got, want)
			}
		}
	})

	t.Run("nothing landed or nothing priced is the dash", func(t *testing.T) {
		db := openFold(t, "dash.sqlite")
		if _, err := db.db.Exec(`INSERT INTO attempts (event_id, label, attempt, kind, usd) VALUES ('1-0', 'c1', 1, 'ok', 2.00)`); err != nil {
			t.Fatal(err)
		}
		if got := queryString(t, db.db, `SELECT usd_per_landed FROM totals`); got != "-" {
			t.Errorf("nothing landed: usd_per_landed = %q, want the dash", got)
		}
		if _, err := db.db.Exec(`UPDATE attempts SET usd = NULL; INSERT INTO landings (event_id, label, kind) VALUES ('2-0', 'c1', 'landed')`); err != nil {
			t.Fatal(err)
		}
		if got := queryString(t, db.db, `SELECT usd_per_landed FROM totals`); got != "-" {
			t.Errorf("nothing priced: usd_per_landed = %q, want the dash", got)
		}
	})
}

// foldState is what an upgrade must keep: the versions, every table's row count, the views'
// figures and the decision row itself.
type foldState struct {
	versions, counts, totals, byModelRoute, decision, byKind string
}

func readFoldState(t *testing.T, db *sql.DB) foldState {
	t.Helper()
	return foldState{
		versions: queryString(t, db, `SELECT group_concat(version) FROM (SELECT version FROM schema_version ORDER BY version)`),
		counts: queryString(t, db, `SELECT (SELECT count(*) FROM attempts) || ',' || (SELECT count(*) FROM reads) || ',' ||
			(SELECT count(*) FROM landings) || ',' || (SELECT count(*) FROM decisions)`),
		totals:       queryString(t, db, `SELECT usd_per_landed FROM totals`),
		byModelRoute: queryString(t, db, `SELECT usd_per_landed FROM by_model_route`),
		decision:     queryString(t, db, `SELECT quote(json_array(`+decisionRowColumns+`)) FROM decisions`),
		byKind: queryString(t, db, `SELECT kind || ',' || decisions || ',' || units || ',' || stepped_up || ',' || escalated || ',' ||
			refused || ',' || calls || ',' || quote(tokens_in) || ',' || quote(tokens_out) FROM decisions_by_kind`),
	}
}

// decisionRowColumns is every column of the decisions table, so the row compares byte for byte.
const decisionRowColumns = `event_id, label, bench, model, route, pr, head, at, day, unit_id, kind, files, packages,
	lanes, lane, rung_tried, height, confidence, floor, stepped_up, escalated, designated, source, rowan_pick,
	reason, wait, awaiting_termination, refusal, outcome, rung_succeeded, calls, tokens_in, tokens_out, usd,
	usage_failed`

// oldFold builds a fold file under an old schema with the three-card fixture, the way a live
// file looks before its first open under this change.
func oldFold(t *testing.T, schema string, decision bool) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "old.sqlite")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()
	loadSQL(t, raw, schema)
	loadSQL(t, raw, "three-card.sql")
	if decision {
		if _, err := raw.Exec(`INSERT INTO decisions (event_id, label, unit_id, kind, escalated) VALUES ('9-0', 'c1', 'u1', 'rebase', 1)`); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

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

// TestFoldFixturesAreTheOldSchemas pins the two schema fixtures to the commits the spec names
// by their version lines, so a fixture cannot drift into the new schema by an edit.
func TestFoldFixturesAreTheOldSchemas(t *testing.T) {
	t.Parallel()
	for name, want := range map[string][]string{
		"schema-v1.sql": {"VALUES (1)"},
		"schema-v2.sql": {"VALUES (1)", "VALUES (2)", "CREATE TABLE IF NOT EXISTS decisions"},
	} {
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		for _, w := range want {
			if !strings.Contains(string(b), w) {
				t.Errorf("%s lacks %q", name, w)
			}
		}
		if strings.Contains(string(b), "VALUES (3)") {
			t.Errorf("%s carries version 3; it must be the old schema verbatim", name)
		}
	}
}
