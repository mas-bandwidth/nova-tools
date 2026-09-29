package table_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/redis/go-redis/v9"
)

func TestDiffCellsNamesTheCell(t *testing.T) {
	t.Parallel()

	golden := table.DefectGolden()
	if got := table.DiffCells(golden, golden); got != "" {
		t.Fatalf("equal tables differ: %s", got)
	}
	cases := []struct{ file, want string }{
		{strings.Replace(golden, "pipeline s1 "+table.RetiredPipeline, "pipeline s1 ready=5 waiting=0", 1), "cell pipeline s1 REFUSED: missing from the file"},
		{strings.Replace(golden, "friend:eight | up | 8 | 0 | 8 |", "friend:eight | up | 8 | 1 | 8 |", 1), "cell friend:eight ready:"},
		{strings.Replace(golden, "proc reconciler down age=-1s why=missing: pass", "proc reconciler up age=-1s why=missing: pass", 1), "cell proc reconciler state:"},
		{strings.Replace(golden, "bench:b3 | down | 1 | 0 | 0 | 0 | 0 | 0 | 0 | down: beat\n", "", 1), "cell bench:b3 up: missing from the file"},
		{golden + "RED two writers: bench:b1:width\n", "cell RED 1: in the file"},
	}
	for _, c := range cases {
		if got := table.DiffCells(c.file, golden); !strings.HasPrefix(got, c.want) {
			t.Errorf("got %q, want prefix %q", got, c.want)
		}
	}
	older := "name | up\nproc reconciler up age=3s\n"
	if got := table.DiffCells(older, "name | up\nproc reconciler up age=5s\n"); got != "" {
		t.Fatalf("a proc age 2 s on is the next tick, not a mismatch: %s", got)
	}
	if got := table.DiffCells(older, "name | up\nproc reconciler up age=6s\n"); !strings.HasPrefix(got, "cell proc reconciler age:") {
		t.Fatalf("a proc age 3 s on: %q", got)
	}
}

// TestCheckSetsCleanAndDrift verifies that CheckSets recounts every cell in one
// pipeline, returns ok when table matches sets, and reports DRIFT when an underlying
// set differs (#4341).
func TestCheckSetsCleanAndDrift(t *testing.T) {
	t.Parallel()

	client, _, _ := sprintStore(t)
	ctx, now := context.Background(), table.SprintFixtureNow()

	// 1. Clean check: all cells match
	res, err := table.CheckSets(ctx, client, "fix", now)
	if err != nil {
		t.Fatalf("CheckSets failed: %v", err)
	}
	if res.HasDrift() {
		t.Fatalf("clean store reported drift: driftCount=%d", res.DriftCount)
	}
	if len(res.Cells) == 0 {
		t.Fatal("expected cells to be checked, got 0")
	}

	for _, l := range res.Lines() {
		if !strings.HasSuffix(l, ", ok") {
			t.Errorf("line does not end in ', ok': %s", l)
		}
		c, err := table.ParseCheckLine(l)
		if err != nil {
			t.Errorf("ParseCheckLine failed on %q: %v", l, err)
		}
		if c.Drift {
			t.Errorf("expected no drift in parsed line: %s", l)
		}
		if c.Line() != l {
			t.Errorf("re-formatted line mismatch: got %q, want %q", c.Line(), l)
		}
	}

	// Capture a snapshot of the clean table before modifying the sets.
	r := table.NewSprintReader(client, table.SprintFixtureConfig())
	snap, err := r.Read(ctx, now)
	if err != nil {
		t.Fatalf("r.Read failed: %v", err)
	}

	// 2. Inject drift in a stream set: add an extra card to ws:swarm: cards:ready
	if err := client.ZAdd(ctx, "ws:swarm: cards:ready", redis.Z{Score: 9999, Member: "injected-card"}).Err(); err != nil {
		t.Fatal(err)
	}

	resDrift, err := table.CheckSnapshot(ctx, client, snap)
	if err != nil {
		t.Fatalf("CheckSnapshot after drift failed: %v", err)
	}
	if !resDrift.HasDrift() {
		t.Fatal("expected drift to be detected, got HasDrift() = false")
	}

	foundStreamDrift := false
	for _, l := range resDrift.Lines() {
		if strings.HasPrefix(l, "ws:swarm: cards:ready, ") {
			foundStreamDrift = true
			if !strings.HasSuffix(l, ", DRIFT") {
				t.Errorf("expected DRIFT for ws:swarm: cards:ready, got: %s", l)
			}
			c, err := table.ParseCheckLine(l)
			if err != nil {
				t.Fatalf("ParseCheckLine failed on %q: %v", l, err)
			}
			if !c.Drift {
				t.Errorf("expected drift in parsed line: %s", l)
			}
		}
	}
	if !foundStreamDrift {
		t.Error("ws:swarm: cards:ready cell check was not found in output")
	}

	// 3. Inject drift in a worker set: add an extra card to bench:hetzner:cards:working
	if err := client.ZAdd(ctx, "bench:hetzner:cards:working", redis.Z{Score: 8888, Member: "extra-worker-card"}).Err(); err != nil {
		t.Fatal(err)
	}

	resWorkerDrift, err := table.CheckSnapshot(ctx, client, snap)
	if err != nil {
		t.Fatalf("CheckSnapshot after worker drift failed: %v", err)
	}
	foundWorkerDrift := false
	for _, l := range resWorkerDrift.Lines() {
		if strings.HasPrefix(l, "bench:hetzner:cards:working, ") {
			foundWorkerDrift = true
			if !strings.HasSuffix(l, ", DRIFT") {
				t.Errorf("expected DRIFT for bench:hetzner:cards:working, got: %s", l)
			}
			c, err := table.ParseCheckLine(l)
			if err != nil {
				t.Fatalf("ParseCheckLine failed on %q: %v", l, err)
			}
			if !c.Drift {
				t.Errorf("expected drift in parsed line: %s", l)
			}
		}
	}
	if !foundWorkerDrift {
		t.Error("bench:hetzner:cards:working cell check was not found in output")
	}
}

func TestParseCheckLine(t *testing.T) {
	t.Parallel()

	// Normal line
	c, err := table.ParseCheckLine("bench:hetzner:cards:ready, 1, 1, ok")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Cell != "bench:hetzner:cards:ready" || c.Table != 1 || c.Sets != 1 || c.Drift {
		t.Errorf("unexpected parsed CellCheck: %+v", c)
	}

	// Stream with commas in name and DRIFT verdict
	c, err = table.ParseCheckLine("ws:fleet, ci, secrets, jev:working, 2, 5, DRIFT")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.Cell != "ws:fleet, ci, secrets, jev:working" || c.Table != 2 || c.Sets != 5 || !c.Drift {
		t.Errorf("unexpected parsed CellCheck: %+v", c)
	}

	// Malformed lines
	malformed := []string{
		"not-enough-parts",
		"cell, 1, 2, invalid-verdict",
		"cell, not-int, 2, ok",
		"cell, 1, not-int, ok",
	}
	for _, s := range malformed {
		if _, err := table.ParseCheckLine(s); err == nil {
			t.Errorf("expected error for malformed line %q, got nil", s)
		}
	}
}
