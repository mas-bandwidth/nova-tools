package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// size-doubles-until-a-rule-breaks at the verb: a fake bench whose load
// crosses 1.25 x cores at W=16 records width=8 on the row and prints one
// BENCH WIDTH line. The rounds are fakes; no ssh runs.
func TestBenchSizeRecordsWidth8(t *testing.T) {
	windowsIsNotABench(t)
	dir := t.TempDir()
	table := filepath.Join(dir, "benches.tsv")
	body := "name\thost\troot\tcores\tharness\tauth\twall\n" +
		"b2\tlocal\t" + dir + "\t1-16\t" + filepath.Join(dir, "harness") + "\t" + filepath.Join(dir, "auth") + "\tnone\n"
	if err := os.WriteFile(table, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	oldRound := benchSizeRound
	oldNow := benchNow
	oldVer := benchSizeVersion
	defer func() { benchSizeRound, benchNow, benchSizeVersion = oldRound, oldNow, oldVer }()
	benchSizeRound = func(row *swarm.Bench, w int, prev float64) (swarm.SizeRound, error) {
		load := 4.0
		if w >= 16 {
			load = 1.26 * 16
		}
		return swarm.SizeRound{W: w, Cores: 16, Load: load,
			CardsPerMin: float64(w) * 10, PrevCardsPerMin: prev, Abstains: 0}, nil
	}
	benchNow = func() time.Time { return time.Date(2026, 9, 15, 0, 0, 0, 0, time.UTC) }
	benchSizeVersion = func() string { return "abc12345devel" }
	var out, errb bytes.Buffer
	if code := cmdBenchSize([]string{"--benches", table, "--bench", "b2"}, &out, &errb); code != 0 {
		t.Fatalf("size exits 0, got %d: %s", code, errb.String())
	}
	if !strings.Contains(out.String(), "BENCH WIDTH bench=b2 width=8 cores=16 rows=1") {
		t.Errorf("one BENCH WIDTH line:\n%s", out.String())
	}
	raw, _ := os.ReadFile(table)
	if !strings.Contains(string(raw), "\t8\t2026-09-15T00:00:00Z\tabc12345") {
		t.Errorf("the row gains width, measured and version:\n%s", raw)
	}
	if _, err := os.Stat(table + ".measured"); err != nil {
		t.Errorf("the measured table sits beside the row: %v", err)
	}
}
