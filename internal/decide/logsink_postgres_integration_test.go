//go:build postgres

// The real-Postgres half of the log's contract. It is behind a build tag AND a
// DSN in the environment, so the ordinary suite never reaches the network
// (Glenn's hard rule: unit tests test logic and mock every endpoint locally).
// Run it against the fleet's database with:
//
//	nova-secrets exec --store ~/nova-bench/secrets --as <seat> \
//	    --only DECIDE_TEST_PG -- \
//	    go test -tags postgres -run TestPostgresLogSink ./internal/decide/
//
// The fake is what makes the unit suite fast; this is what keeps the fake
// honest.
package decide

import (
	"context"
	"os"
	"testing"
	"time"
)

// DecideTestPGEnv names the DSN this integration test reads. It is a separate
// name from LogDSNEnv on purpose: a test must be pointed at a database
// deliberately, never inherit the one a tool is writing to.
const DecideTestPGEnv = "DECIDE_TEST_PG"

func TestPostgresLogSinkAgainstARealServer(t *testing.T) {
	dsn := os.Getenv(DecideTestPGEnv)
	if dsn == "" {
		t.Skipf("set $%s to a Postgres DSN to run the decision log against a real server", DecideTestPGEnv)
	}
	sink, err := OpenPostgresLog(dsn)
	if err != nil {
		t.Fatalf("open: %s", err)
	}
	// The pool is closed by a cleanup rather than a defer: a deferred close runs
	// when the test function returns, which is BEFORE every t.Cleanup, and the
	// row cleanup below needs the pool. Cleanups run last-registered-first, so
	// this one closes after the DELETE.
	t.Cleanup(func() { sink.Close() })

	ctx := context.Background()
	if err := sink.Migrate(ctx); err != nil {
		t.Fatalf("migrate: %s", err)
	}
	// Twice, because `log migrate` is safe to run on every start.
	if err := sink.Migrate(ctx); err != nil {
		t.Fatalf("second migrate: %s", err)
	}

	unit := "pg-soak-" + time.Now().UTC().Format("20060102150405.000000000")
	t.Cleanup(func() {
		if _, err := sink.db.Exec(`DELETE FROM decide_log WHERE unit_id = $1`, unit); err != nil {
			t.Errorf("the soak left its rows behind: %s", err)
		}
	})

	in := 937
	reported := Entry{
		Time:          time.Now().UTC().Format(time.RFC3339),
		Unit:          unit,
		Kind:          KindRebase,
		Evidence:      Unit{ID: unit, Kind: KindRebase, Files: 2, Packages: 1, Lanes: 1, LaneOwner: "decide"},
		RungTried:     "flash",
		Confidence:    0.94,
		Floor:         DefaultFloor,
		Source:        SourceRules,
		RowanPick:     "flash",
		Wait:          WaitNone,
		Outcome:       OutcomeOK,
		RungSucceeded: "flash",
		Calls:         1,
		TokensIn:      &in,
		// TokensOut stays nil: the presence rule's whole point.
	}
	if err := sink.Append(reported); err != nil {
		t.Fatalf("append: %s", err)
	}

	rows, err := sink.Entries()
	if err != nil {
		t.Fatalf("entries: %s", err)
	}
	var got *Entry
	for i := range rows {
		if rows[i].Unit == unit {
			got = &rows[i]
		}
	}
	if got == nil {
		t.Fatalf("the row that was written is not in the log (%d rows)", len(rows))
	}
	if got.TokensIn == nil || *got.TokensIn != 937 {
		t.Errorf("the reported counter did not survive: %v", got.TokensIn)
	}
	if got.TokensOut != nil {
		t.Errorf("an unreported counter must be SQL NULL, got %v", *got.TokensOut)
	}
	if got.Evidence.Files != 2 || got.Evidence.Packages != 1 || got.Evidence.LaneOwner != "decide" {
		t.Errorf("the size buckets and the lane did not survive: %+v", got.Evidence)
	}
	if got.RungTried != "flash" || got.RungSucceeded != "flash" || got.Outcome != OutcomeOK {
		t.Errorf("the rung and the outcome did not survive: %+v", got)
	}
	if got.Confidence != 0.94 || got.Floor != DefaultFloor {
		t.Errorf("the confidence and the floor did not survive: %+v", got)
	}

	// The summary is a projection of the rows, and it reads off the table.
	reg, err := LoadRegistry("")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Summarize(reg, rows); err != nil {
		t.Fatalf("summarize over the table: %s", err)
	}
}
