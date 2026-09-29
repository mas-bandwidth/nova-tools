package fleet

import (
	"path/filepath"
	"strings"
	"testing"
)

const sampleRunnersTSV = `# THE RUNNERS TABLE: the GitHub Actions runners each machine carries for mas-bandwidth/nova-tools.
# host<TAB>count<TAB>labels<TAB>user<TAB>description
studio	4	studio,darwin-arm64	nova	FOUR since 2026-09-25 10:58 PM ET
batman	0	batman,darwin-x64	-	2019 iMac Pro. ZERO
space	4	space,spacebox	-	Bare-metal Linux, user ubuntu, 32 cores.
hetzner	4	space,hetzner	-	Bare-metal Linux, user nova.
hulk	0	space,hulk	-	Threadripper. ZERO
`

func TestReadRunnersTable(t *testing.T) {
	t.Parallel()

	tbl, err := ReadRunners(strings.NewReader(sampleRunnersTSV))
	if err != nil {
		t.Fatalf("ReadRunners failed: %v", err)
	}

	if len(tbl.Rows) != 5 {
		t.Fatalf("expected 5 rows, got %d", len(tbl.Rows))
	}
	if got := tbl.Count("studio"); got != 4 {
		t.Errorf("studio count = %d, want 4", got)
	}
	if got := tbl.Count("batman"); got != 0 {
		t.Errorf("batman count = %d, want 0", got)
	}
	if got := tbl.Count("space"); got != 4 {
		t.Errorf("space count = %d, want 4", got)
	}
	if got := tbl.Count("hetzner"); got != 4 {
		t.Errorf("hetzner count = %d, want 4", got)
	}
	if got := tbl.Count("hulk"); got != 0 {
		t.Errorf("hulk count = %d, want 0", got)
	}
	if got := tbl.Count("unknown"); got != 0 {
		t.Errorf("unknown count = %d, want 0", got)
	}
}

func TestSetCountExistingAndNew(t *testing.T) {
	t.Parallel()

	tbl, err := ReadRunners(strings.NewReader(sampleRunnersTSV))
	if err != nil {
		t.Fatalf("ReadRunners: %v", err)
	}

	// Update existing bench
	old := tbl.SetCount("space", 8)
	if old != 4 {
		t.Errorf("old count = %d, want 4", old)
	}
	if got := tbl.Count("space"); got != 8 {
		t.Errorf("new count = %d, want 8", got)
	}

	// Update non-existing bench
	oldNew := tbl.SetCount("vision", 2)
	if oldNew != 0 {
		t.Errorf("old count for vision = %d, want 0", oldNew)
	}
	if got := tbl.Count("vision"); got != 2 {
		t.Errorf("new count for vision = %d, want 2", got)
	}

	// Verify Save and re-read
	dir := t.TempDir()
	path := filepath.Join(dir, "runners.tsv")
	if err := tbl.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := ReadRunnersFile(path)
	if err != nil {
		t.Fatalf("ReadRunnersFile: %v", err)
	}
	if got := reloaded.Count("space"); got != 8 {
		t.Errorf("reloaded space = %d, want 8", got)
	}
	if got := reloaded.Count("vision"); got != 2 {
		t.Errorf("reloaded vision = %d, want 2", got)
	}
	if got := reloaded.Count("studio"); got != 4 {
		t.Errorf("reloaded studio = %d, want 4", got)
	}
}

func TestIsRunnerUnit(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		want bool
	}{
		{"nova-runner-1.service", true},
		{"nova-runner-16.service", true},
		{"com.nova.runner-1.plist", true},
		{"com.nova.runner-4.plist", true},
		{"space-nova-1.service", true},
		{"com.nova.loop.nova-sprint-bench-beat.plist", false},
		{"nova-sprint-table.service", false},
		{"node_exporter.service", false},
	}

	for _, c := range cases {
		if got := IsRunnerUnit(c.name); got != c.want {
			t.Errorf("IsRunnerUnit(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestCountOnlineRunners(t *testing.T) {
	t.Parallel()

	sample := PSSample{
		Units: []PSUnit{
			{Name: "nova-runner-1.service", State: UnitDeclared},
			{Name: "nova-runner-2.service", State: UnitDeclared},
			{Name: "nova-runner-3.service", State: UnitDeclared},
			{Name: "com.nova.other.plist", State: UnitDeclared},
		},
	}

	beatWithPS := map[string]string{
		"ps": sample.Encode(),
		"ci": "2",
	}

	// Should prefer counting runner units when present
	if got := CountOnlineRunners(beatWithPS); got != 3 {
		t.Errorf("CountOnlineRunners(beatWithPS) = %d, want 3", got)
	}

	// Should fall back to ci field when no units sample
	beatWithCIOnly := map[string]string{
		"ci": "4",
	}
	if got := CountOnlineRunners(beatWithCIOnly); got != 4 {
		t.Errorf("CountOnlineRunners(beatWithCIOnly) = %d, want 4", got)
	}

	// Empty beat
	if got := CountOnlineRunners(map[string]string{}); got != 0 {
		t.Errorf("CountOnlineRunners(empty) = %d, want 0", got)
	}
}

func TestRunnersStatusSummaryLine(t *testing.T) {
	t.Parallel()

	clean := RunnersStatusResult{
		TotalDeclared:   12,
		TotalRegistered: 12,
		TotalOnline:     12,
	}
	if want := "RUNNERS OK total_declared=12 total_registered=12 total_online=12"; clean.SummaryLine() != want {
		t.Errorf("clean summary = %q, want %q", clean.SummaryLine(), want)
	}

	drift := RunnersStatusResult{
		TotalDeclared:   12,
		TotalRegistered: 8,
		TotalOnline:     4,
		Drifting:        []string{"space", "hetzner"},
	}
	if want := "RUNNERS DRIFT total_declared=12 total_registered=8 total_online=4 drifting=space,hetzner"; drift.SummaryLine() != want {
		t.Errorf("drift summary = %q, want %q", drift.SummaryLine(), want)
	}
}
