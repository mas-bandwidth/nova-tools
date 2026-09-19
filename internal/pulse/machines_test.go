package pulse

// The lock (Glenn 2026-09-18): runner hosts are CI-only. No card, no probe and no load is
// placed on a machine that serves the merge group's shards. These are the tests of the
// guard where it bites: the fill's bench list, the two seams that actually reach a machine,
// and the single-bench fleet verbs.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/fleet"
)

// machinesFile writes a small registry: every name in benches is a bench, every name in
// runners is a CI-only runner host.
func machinesFile(t *testing.T, dir string, benches, runners []string) string {
	t.Helper()
	var b strings.Builder
	b.WriteString("# name\tssh\tos/arch\troles\tseat\tcores\tnotes\n")
	for _, name := range benches {
		b.WriteString(name + "\t" + name + "\tlinux/x64\tbench\tswarm-" + name + "\t64\t-\n")
	}
	for _, name := range runners {
		b.WriteString(name + "\t" + name + "\tdarwin/amd64\trunner\t-\t8\tCI-only\n")
	}
	path := filepath.Join(dir, "machines.tsv")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestFillRefusesARunnerHostAndLaunchesNothing: a fill naming batman launches nothing at
// all -- not even the cards the benches beside it could have taken -- and says why on one
// line with the remedy.
func TestFillRefusesARunnerHostAndLaunchesNothing(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeCard(t, ready, "card-001.md", "a card\n")
	l := &laneLauncher{}
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: ready, Launched: launched,
		Machines: machinesFile(t, dir, []string{"hulk"}, []string{"batman"}),
		Benches:  []string{"hulk", "batman"},
		Once:     true,
		Stdout:   &out,
		Stderr:   &errb,
		Capacity: laneCap{"hulk": 10, "batman": 10},
		Launcher: l,
	})
	if code != 2 {
		t.Fatalf("fill exit = %d, want 2; stderr=%q", code, errb.String())
	}
	if len(l.calls) != 0 {
		t.Fatalf("a refused fill launched %d cards: %q", len(l.calls), l.calls)
	}
	line := strings.TrimSpace(errb.String())
	if !strings.HasPrefix(line, "FILL REFUSED bench=batman reason=runner-host remedy=\"") {
		t.Fatalf("the refusal line is %q", line)
	}
	if !strings.Contains(line, "hulk") {
		t.Errorf("the remedy names no bench to fill instead: %q", line)
	}
	if got := len(readyCards(ready)); got != 1 {
		t.Errorf("ready holds %d cards, want the one card untouched", got)
	}
}

// TestFillNamesEveryRefusedBench: two wrong names are two lines, so a person learns both at
// once rather than one per run.
func TestFillNamesEveryRefusedBench(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: ready, Launched: launched,
		Machines: machinesFile(t, dir, []string{"hulk"}, []string{"batman", "superman"}),
		Benches:  []string{"batman", "superman"},
		Once:     true,
		Stdout:   &out, Stderr: &errb,
		Capacity: laneCap{}, Launcher: &laneLauncher{},
	})
	if code != 2 {
		t.Fatalf("fill exit = %d, want 2", code)
	}
	for _, name := range []string{"bench=batman", "bench=superman"} {
		if !strings.Contains(errb.String(), name) {
			t.Errorf("stderr does not refuse %s: %q", name, errb.String())
		}
	}
}

// TestFillRefusesWithoutTheMachinesRegistry: with no registry the verb cannot tell a bench
// from a CI runner host, and the one thing it must never do is guess that.
func TestFillRefusesWithoutTheMachinesRegistry(t *testing.T) {
	dir := t.TempDir()
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: filepath.Join(dir, "ready"), Launched: filepath.Join(dir, "launched"),
		Benches: []string{"hulk"}, Once: true,
		Stdout: &out, Stderr: &errb,
		Capacity: laneCap{"hulk": 1}, Launcher: &laneLauncher{},
	})
	if code != 2 {
		t.Fatalf("fill exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "missing --machines") {
		t.Fatalf("stderr = %q, want the missing --machines refusal", errb.String())
	}
}

// TestFillRefusesAnUnreadableRegistry: the registry is read whole or not at all, and a file
// that does not parse stops the fill rather than filling the machines it managed to read.
func TestFillRefusesAnUnreadableRegistry(t *testing.T) {
	dir := t.TempDir()
	bad := filepath.Join(dir, "machines.tsv")
	if err := os.WriteFile(bad, []byte("hulk\thulk\tlinux/x64\tbench,runner\t-\t64\tshared\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: filepath.Join(dir, "ready"), Launched: filepath.Join(dir, "launched"),
		Machines: bad, Benches: []string{"hulk"}, Once: true,
		Stdout: &out, Stderr: &errb,
		Capacity: laneCap{"hulk": 1}, Launcher: &laneLauncher{},
	})
	if code != 2 {
		t.Fatalf("fill exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "allow-shared") {
		t.Fatalf("stderr = %q, want the shared-machine refusal", errb.String())
	}
}

// TestTheLaunchSeamRefusesARunnerHostOnItsOwn: the wrapper is the last gate before a card
// lands, and it asks the registry itself -- a bench name that arrives by some road the list
// check never saw is still refused.
func TestTheLaunchSeamRefusesARunnerHostOnItsOwn(t *testing.T) {
	dir := t.TempDir()
	reg, err := fleet.ReadRegistry(machinesFile(t, dir, []string{"hulk"}, []string{"batman"}))
	if err != nil {
		t.Fatal(err)
	}
	rec := &laneLauncher{}
	g := guardedLauncher{reg: reg, next: rec}
	if err := g.Launch("batman", "card-001.md"); err == nil {
		t.Fatal("the launcher put a card on a CI runner host")
	}
	if len(rec.calls) != 0 {
		t.Fatalf("the refused card still reached the launcher: %q", rec.calls)
	}
	if err := g.Launch("hulk", "card-001.md"); err != nil {
		t.Fatalf("the launcher refused a bench: %v", err)
	}
}

// TestTheCapacitySeamRefusesARunnerHostOnItsOwn: a capacity probe is an ssh to the machine,
// which is load, which is exactly what a runner host may not take.
func TestTheCapacitySeamRefusesARunnerHostOnItsOwn(t *testing.T) {
	dir := t.TempDir()
	reg, err := fleet.ReadRegistry(machinesFile(t, dir, []string{"hulk"}, []string{"batman"}))
	if err != nil {
		t.Fatal(err)
	}
	g := guardedCapacity{reg: reg, next: laneCap{"hulk": 7, "batman": 99}}
	if _, err := g.Capacity("batman"); err == nil {
		t.Fatal("the capacity probe reached a CI runner host")
	}
	n, err := g.Capacity("hulk")
	if err != nil || n != 7 {
		t.Fatalf("capacity on a bench = %d, %v; want 7, nil", n, err)
	}
}

// TestASingleBenchFleetVerbRefusesARunnerHost: the four single-bench verbs resolve their
// --bench through one function, so one test over `fleet standard` holds the road they share.
func TestASingleBenchFleetVerbRefusesARunnerHost(t *testing.T) {
	dir := t.TempDir()
	benches := filepath.Join(dir, "benches.tsv")
	if err := os.WriteFile(benches, []byte("batman\tbatman\t/Users/nova\t-\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := FleetStandard(FleetStandardInput{
		Benches:  benches,
		Machines: machinesFile(t, dir, []string{"hulk"}, []string{"batman"}),
		Name:     "batman",
		OS:       "darwin",
		Stdout:   &out, Stderr: &errb,
	})
	if code != 2 {
		t.Fatalf("fleet standard exit = %d, want 2", code)
	}
	line := strings.TrimSpace(out.String())
	if !strings.HasPrefix(line, "FLEET REFUSED bench=batman reason=runner-host remedy=\"") {
		t.Fatalf("the refusal line is %q", line)
	}
}

// TestASingleBenchFleetVerbWithoutARegistryKeepsItsOlderGuard: the narrowing, written down.
// The fleet admin verbs predate the registry; with no --machines they still refuse `studio`
// by name and a bench the benches file does not carry, and nothing else.
func TestASingleBenchFleetVerbWithoutARegistryKeepsItsOlderGuard(t *testing.T) {
	dir := t.TempDir()
	benches := filepath.Join(dir, "benches.tsv")
	if err := os.WriteFile(benches, []byte("studio\tstudio\t/Users/glenn\t-\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := FleetStandard(FleetStandardInput{
		Benches: benches, Name: "studio", OS: "darwin",
		Stdout: &out, Stderr: &errb,
	})
	if code != 2 {
		t.Fatalf("fleet standard exit = %d, want 2", code)
	}
	if !strings.Contains(out.String(), "FLEET REFUSED bench=studio") {
		t.Fatalf("stdout = %q, want the studio refusal", out.String())
	}
}

// TestLaunchRefusesARunnerHostBench: a launch naming batman hands the card to nova-swarm
// batch, whose ssh is exactly the reach a runner host may not take -- so the NAME is
// resolved against the machines registry before the batch is admitted, and the refused
// launch reaches no batch at all.
func TestLaunchRefusesARunnerHostBench(t *testing.T) {
	root := t.TempDir()
	argvLog := filepath.Join(root, "argv.log")
	fakeSwarm(t, argvLog)
	cards, _ := writeCards(t, root, 1)

	code, _, errb := runLaunch(t, LaunchInput{
		Cards: cards, Root: root, Slots: 2, Deadline: "600",
		Bench:    "batman",
		Machines: machinesFile(t, root, []string{"hulk"}, []string{"batman"}),
		Now:      func() time.Time { return time.Date(2026, 9, 19, 12, 0, 0, 0, time.UTC) },
	})
	line := strings.TrimSpace(errb)
	if !strings.HasPrefix(line, "PULSE REFUSED bench=batman reason=runner-host remedy=\"") {
		t.Fatalf("launch admitted a runner-host bench: exit=%d stderr=%q, want the PULSE REFUSED bench=batman reason=runner-host line", code, errb)
	}
	if code != 2 {
		t.Fatalf("launch exit = %d, want 2", code)
	}
	if raw, err := os.ReadFile(argvLog); err == nil && strings.TrimSpace(string(raw)) != "" {
		t.Fatalf("the refused launch still reached nova-swarm batch: %q", raw)
	}
}
