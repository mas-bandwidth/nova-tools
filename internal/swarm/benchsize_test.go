package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// size-doubles-until-a-rule-breaks: a fake bench whose load crosses 1.25 x
// cores at W=16 records width=8; one whose throughput at 8 is under 1.5 x its
// throughput at 4 records width=4; one abstain at any round ends the doubling.
func TestSizeDoublesUntilARuleBreaks(t *testing.T) {
	windowsIsNotABench(t)
	cores := 16
	loadFor := func(w int) float64 {
		if w >= 16 {
			return 1.26 * float64(cores)
		}
		return float64(cores) * 0.5
	}
	cpmFor := func(w int) float64 { return float64(w) * 10 }
	rounds := []SizeRound{
		{W: 1, Cores: cores, Load: loadFor(1), CardsPerMin: cpmFor(1), PrevCardsPerMin: 0, Abstains: 0},
		{W: 2, Cores: cores, Load: loadFor(2), CardsPerMin: cpmFor(2), PrevCardsPerMin: cpmFor(1), Abstains: 0},
		{W: 4, Cores: cores, Load: loadFor(4), CardsPerMin: cpmFor(4), PrevCardsPerMin: cpmFor(2), Abstains: 0},
		{W: 8, Cores: cores, Load: loadFor(8), CardsPerMin: cpmFor(8), PrevCardsPerMin: cpmFor(4), Abstains: 0},
		{W: 16, Cores: cores, Load: loadFor(16), CardsPerMin: cpmFor(16), PrevCardsPerMin: cpmFor(8), Abstains: 0},
	}
	if got := MeasureWidthFromRounds(rounds); got != 8 {
		t.Fatalf("load crossing 1.25xcores at W=16 records width=8, got %d", got)
	}

	slow := []SizeRound{
		{W: 1, Cores: cores, Load: 1, CardsPerMin: 10, PrevCardsPerMin: 0, Abstains: 0},
		{W: 2, Cores: cores, Load: 1, CardsPerMin: 20, PrevCardsPerMin: 10, Abstains: 0},
		{W: 4, Cores: cores, Load: 2, CardsPerMin: 40, PrevCardsPerMin: 20, Abstains: 0},
		{W: 8, Cores: cores, Load: 4, CardsPerMin: 50, PrevCardsPerMin: 40, Abstains: 0},
	}
	if got := MeasureWidthFromRounds(slow); got != 4 {
		t.Fatalf("throughput at 8 under 1.5x throughput at 4 records width=4, got %d", got)
	}

	abstain := []SizeRound{
		{W: 1, Cores: cores, Load: 1, CardsPerMin: 10, PrevCardsPerMin: 0, Abstains: 0},
		{W: 2, Cores: cores, Load: 1, CardsPerMin: 20, PrevCardsPerMin: 10, Abstains: 1},
		{W: 4, Cores: cores, Load: 2, CardsPerMin: 40, PrevCardsPerMin: 20, Abstains: 0},
	}
	if got := MeasureWidthFromRounds(abstain); got != 1 {
		t.Fatalf("one abstain at W=2 ends the doubling at width=1, got %d", got)
	}
}

// size-records-width-with-version: the row gains width, measured and version,
// and a version unequal to the running tool's is re-measured by the adopt step.
func TestSizeRecordsWidthWithVersion(t *testing.T) {
	windowsIsNotABench(t)
	dir := t.TempDir()
	p := filepath.Join(dir, "benches.tsv")
	body := "name\thost\troot\tcores\tharness\tauth\twall\n" +
		"b2\tb2\t" + dir + "\t1-16\t/h\t/a\tnone\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cores, rows, err := RecordBenchWidth(p, "b2", 8, "2026-09-15T00:00:00Z", "abc12345")
	if err != nil {
		t.Fatalf("record width: %v", err)
	}
	if cores != 16 || rows != 1 {
		t.Fatalf("BENCH WIDTH wants cores=16 rows=1, got cores=%d rows=%d", cores, rows)
	}
	raw, _ := os.ReadFile(p)
	if !strings.Contains(string(raw), "\t8\t2026-09-15T00:00:00Z\tabc12345") {
		t.Fatalf("the row gains width, measured and version:\n%s", raw)
	}
	table, err := LoadBenchTable(p)
	if err != nil {
		t.Fatalf("re-read: %v", err)
	}
	if table[0].Width != 8 || table[0].Version != "abc12345" {
		t.Fatalf("width round-trips, got %+v", table[0])
	}
	if !WidthNeedsRemeasure(table[0], "different1") {
		t.Fatalf("a version unequal to the running tool's is re-measured by the adopt step")
	}
	if WidthNeedsRemeasure(table[0], "abc12345") {
		t.Fatalf("a matching version is not re-measured")
	}
}

// launch-fills-to-width: a bench with width=8, 16 cores and no load is given
// eight cards from a batch of twelve, and the four wait in the queue rather
// than a ninth slot.
func TestLaunchFillsToWidth(t *testing.T) {
	windowsIsNotABench(t)
	if got := PlanBenchLaunch(16, 0, 8, 12); got != 8 {
		t.Fatalf("width=8 with 12 queued launches 8, got %d", got)
	}
}

// launch-drops-down-under-load: the same bench at load 6 is given
// 16x1.5-6=18, capped at cores 16 and then at its width 8; at load 20 it is
// given four; at load 24 it is given none, its width unchanged on the row.
func TestLaunchDropsDownUnderLoad(t *testing.T) {
	windowsIsNotABench(t)
	if got := PlanBenchLaunch(16, 6, 8, 100); got != 8 {
		t.Fatalf("headroom 18 capped at cores 16 then width 8, got %d", got)
	}
	if got := PlanBenchLaunch(16, 20, 8, 100); got != 4 {
		t.Fatalf("at load 20 headroom is 4, got %d", got)
	}
	if got := PlanBenchLaunch(16, 24, 8, 100); got != 0 {
		t.Fatalf("at load 24 headroom is 0, got %d", got)
	}
	if got := Headroom(16, 24); got != 0 {
		t.Fatalf("headroom floors at 0, got %d", got)
	}
}
