package main

// The default card pool is the registry's, not a Go literal (#1476). Every row whose roles
// include `bench` AND whose notes carry `certified=<YYYY-MM-DD>` is in the pool the tick
// fills when the caller names no --bench; a bench that has not been certified is not, and a
// machine that is not a bench never was. A --bench still narrows to the names it carries.
//
// Every registry here is a fixture written into t.TempDir(). No test in this file reads the
// fleet's own queue/control/machines.tsv: a test that reads the live registry passes or
// fails on what the fleet did last night, which is not a test.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// poolRow is one fixture machine: its name, its roles field and its notes field. Its seat is
// `swarm-<name>`, because since #2014 a bench a card can run on is a bench whose row names
// the seat to run it under -- a row with no seat is refused by name and left out of the pool.
type poolRow struct{ name, roles, notes string }

// poolRegistry writes a fixture registry and answers its path.
func poolRegistry(t *testing.T, dir string, rows ...poolRow) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("# a fixture registry, never the fleet's own\n")
	for _, r := range rows {
		notes := r.notes
		if notes == "" {
			notes = "-"
		}
		b.WriteString(strings.Join([]string{r.name, r.name, "linux/x64", r.roles, "swarm-" + r.name, "8", notes}, "\t"))
		b.WriteString("\n")
	}
	path := filepath.Join(dir, "machines.tsv")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// fillTickOnce runs one tick with a fixed capacity of zero: no ssh, no card, one FILL line
// naming exactly the benches the pool resolved to.
func fillTickOnce(t *testing.T, dir, machines string, extra ...string) (int, string, string) {
	t.Helper()
	args := append([]string{
		"fill",
		"--ready", filepath.Join(dir, "ready"),
		"--launched", filepath.Join(dir, "launched"),
		"--machines", machines,
		"--capacity", "0",
		"--once",
	}, extra...)
	var out, errb bytes.Buffer
	code := run(args, &out, &errb, time.Now().UTC())
	return code, strings.TrimSpace(out.String()), strings.TrimSpace(errb.String())
}

// TestFillPoolIsEveryCertifiedBench: a bench certified tonight is filled the moment its row
// says so, and no edit to this package is needed to get it there.
func TestFillPoolIsEveryCertifiedBench(t *testing.T) {
	dir := t.TempDir()
	machines := poolRegistry(t, dir,
		poolRow{"bench-old", "bench", "certified=2026-09-01 the first one"},
		poolRow{"bench-new", "bench", "certified=2026-09-18 provisioned and certified tonight"},
		poolRow{"bench-raw", "bench", "provisioned, no certification run yet"},
		poolRow{"ci-host", "runner", "CI only"},
	)
	code, line, errb := fillTickOnce(t, dir, machines)
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb)
	}
	for _, want := range []string{"bench-old:launched=0", "bench-new:launched=0"} {
		if !strings.Contains(line, want) {
			t.Errorf("FILL line = %q, wants %s", line, want)
		}
	}
	for _, never := range []string{"bench-raw", "ci-host"} {
		if strings.Contains(line, never) {
			t.Errorf("FILL line = %q, must not name %s", line, never)
		}
	}
}

// TestFillBenchStillNarrows: --bench takes one name out of the pool and fills that.
func TestFillBenchStillNarrows(t *testing.T) {
	dir := t.TempDir()
	machines := poolRegistry(t, dir,
		poolRow{"bench-old", "bench", "certified=2026-09-01 the first one"},
		poolRow{"bench-new", "bench", "certified=2026-09-18 certified tonight"},
	)
	code, line, errb := fillTickOnce(t, dir, machines, "--bench", "bench-new")
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb)
	}
	if !strings.Contains(line, "bench-new:launched=0") {
		t.Errorf("FILL line = %q, wants bench-new", line)
	}
	if strings.Contains(line, "bench-old") {
		t.Errorf("FILL line = %q, must name only the bench that was asked for", line)
	}
}

// TestFillRefusesAnEmptyPool: a registry naming no certified bench is a refusal with the
// remedy on it, not a silent tick that fills nothing.
func TestFillRefusesAnEmptyPool(t *testing.T) {
	dir := t.TempDir()
	machines := poolRegistry(t, dir,
		poolRow{"bench-raw", "bench", "provisioned, no certification run yet"},
		poolRow{"ci-host", "runner", "CI only"},
	)
	code, line, errb := fillTickOnce(t, dir, machines)
	if code != 2 {
		t.Fatalf("fill exit = %d, want 2; stdout=%q stderr=%q", code, line, errb)
	}
	if !strings.Contains(errb, "certified=") {
		t.Errorf("refusal = %q, wants the remedy naming certified=<YYYY-MM-DD>", errb)
	}
}
