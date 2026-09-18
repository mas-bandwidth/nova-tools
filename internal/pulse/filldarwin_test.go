package pulse

// The darwin edges the schema dogfood found on the M2 Air, 2026-09-18: `nova-pulse fill`
// could not serve a darwin bench BY CONSTRUCTION. The seams took a bench NAME, so the ssh
// went to the name and the capacity formula was Linux's /proc, whatever the registry row
// said the machine was. These tests hold the shape that fixes it: the seams take the
// registry ROW, a card may name the os it needs, and --dry-run reads capacity and launches
// nothing.

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/fleet"
)

// airRow is the Air's real row shape, as queue/control/machines.tsv carries it.
const airRow = "air\tglenn@100.117.59.68\tdarwin/arm64\tbench,runner\tswarm-air\t8\tallow-shared=2026-09-18 the Air is a bench and a runner while the fleet is small\n"

// mixedRegistry writes a registry with one linux bench and the Air's darwin row beside it.
func mixedRegistry(t *testing.T, dir string) string {
	t.Helper()
	body := "# name\tssh\tos/arch\troles\tseat\tcores\tnotes\n" +
		"hulk\thulk\tlinux/x64\tbench\tswarm-hulk\t64\t-\n" + airRow
	path := filepath.Join(dir, "machines.tsv")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// rowCap records the registry row every capacity read was handed, by machine name.
type rowCap struct {
	n    int
	rows map[string]fleet.Machine
}

func (c *rowCap) Capacity(m fleet.Machine) (int, error) {
	if c.rows == nil {
		c.rows = map[string]fleet.Machine{}
	}
	c.rows[m.Name] = m
	return c.n, nil
}

// rowLauncher records the row and the card of every launch, in order.
type rowLauncher struct {
	rows  []fleet.Machine
	cards []string
}

func (l *rowLauncher) Launch(m fleet.Machine, card string) error {
	l.rows = append(l.rows, m)
	l.cards = append(l.cards, filepath.Base(card))
	return nil
}

// TestTheCapacitySeamIsHandedTheRegistryRowNotTheBenchName: the ssh target and the os are
// facts of the registry, and the seam cannot know either from a bare name. `air` is not a
// hostname; `glenn@100.117.59.68` is.
func TestTheCapacitySeamIsHandedTheRegistryRowNotTheBenchName(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeCard(t, ready, "card-001.md", "a card\n")
	cap := &rowCap{n: 4}
	l := &rowLauncher{}
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: ready, Launched: launched,
		Machines: mixedRegistry(t, dir),
		Benches:  []string{"air"}, Once: true,
		Stdout: &out, Stderr: &errb,
		Capacity: cap, Launcher: l,
	})
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
	}
	row, ok := cap.rows["air"]
	if !ok {
		t.Fatalf("the capacity seam was never handed the air row: %v", cap.rows)
	}
	if row.SSH != "glenn@100.117.59.68" {
		t.Errorf("capacity ssh target = %q, want the registry's ssh column", row.SSH)
	}
	if row.OS != "darwin" || row.Arch != "arm64" {
		t.Errorf("capacity row os/arch = %s/%s, want darwin/arm64", row.OS, row.Arch)
	}
	if row.Seat != "swarm-air" {
		t.Errorf("capacity row seat = %q, want swarm-air", row.Seat)
	}
	if len(l.rows) != 1 || l.rows[0].SSH != "glenn@100.117.59.68" {
		t.Fatalf("the launcher was handed %v, want the air row", l.rows)
	}
}

// TestADarwinCardOnlyLandsOnADarwinBench: a card's `os:` line is a requirement, not a
// preference. The linux bench is named first and has capacity for both, and the darwin card
// still waits for the darwin row.
func TestADarwinCardOnlyLandsOnADarwinBench(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeCard(t, ready, "card-001.md", "os: darwin\n\nbuild the darwin leg\n")
	writeCard(t, ready, "card-002.md", "a Go card that runs anywhere\n")
	l := &rowLauncher{}
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: ready, Launched: launched,
		Machines: mixedRegistry(t, dir),
		Benches:  []string{"hulk", "air"}, Once: true,
		Stdout: &out, Stderr: &errb,
		Capacity: &rowCap{n: 10}, Launcher: l,
	})
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
	}
	got := map[string]string{}
	for i, card := range l.cards {
		got[card] = l.rows[i].Name
	}
	if got["card-001.md"] != "air" {
		t.Errorf("the darwin card went to %q, want air", got["card-001.md"])
	}
	if got["card-002.md"] != "hulk" {
		t.Errorf("the card with no os went to %q, want the first bench hulk", got["card-002.md"])
	}
}

// TestADarwinCardWaitsWhenNoNamedBenchRunsDarwin: it stays ready, it is not launched on a
// linux bench, and one line says why rather than leaving it to sit there unread.
func TestADarwinCardWaitsWhenNoNamedBenchRunsDarwin(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeCard(t, ready, "card-001.md", "LEG: darwin/arm64\n\nthe darwin leg\n")
	l := &rowLauncher{}
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: ready, Launched: launched,
		Machines: mixedRegistry(t, dir),
		Benches:  []string{"hulk"}, Once: true,
		Stdout: &out, Stderr: &errb,
		Capacity: &rowCap{n: 10}, Launcher: l,
	})
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if len(l.cards) != 0 {
		t.Fatalf("a darwin card was launched on a linux bench: %q", l.cards)
	}
	if got := len(readyCards(ready)); got != 1 {
		t.Fatalf("ready holds %d cards, want the darwin card left where it was", got)
	}
	line := out.String()
	if !strings.Contains(line, "FILL WAITING") || !strings.Contains(line, "os=darwin") {
		t.Fatalf("stdout = %q, want a FILL WAITING line naming the os", line)
	}
	if !strings.Contains(line, "hulk=linux") {
		t.Errorf("the waiting line does not say what the named benches run: %q", line)
	}
}

// TestCardOSReadsTheLegAndTheOSLine: `os: darwin` and `LEG: darwin/arm64` both say darwin;
// a card that names neither runs anywhere, and `LEGS:` is prose.
func TestCardOSReadsTheLegAndTheOSLine(t *testing.T) {
	dir := t.TempDir()
	for _, c := range []struct{ body, want string }{
		{"os: darwin\n", "darwin"},
		{"OS: Darwin\n", "darwin"},
		{"LEG: darwin/arm64\n", "darwin"},
		{"LEG: linux\n", "linux"},
		{"a card with no os at all\n", ""},
		{"LEGS: the nine legs of the table\n", ""},
		{"most operating systems are fine\n", ""},
	} {
		path := filepath.Join(dir, "c.md")
		if err := os.WriteFile(path, []byte(c.body), 0o644); err != nil {
			t.Fatal(err)
		}
		if got := cardOS(path); got != c.want {
			t.Errorf("cardOS(%q) = %q, want %q", c.body, got, c.want)
		}
	}
}

// TestDryRunReadsCapacityAndLaunchesNothing: the one real probe a person runs against a
// bench they have just added. It reaches the machine for capacity and for nothing else: no
// card moves, no launcher is called, and the line carries the number, the os and the ssh
// target so the row that produced it can be checked.
func TestDryRunReadsCapacityAndLaunchesNothing(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	writeCard(t, ready, "card-001.md", "a card\n")
	l := &rowLauncher{}
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: ready, Launched: launched,
		Machines: mixedRegistry(t, dir),
		Benches:  []string{"air"}, Once: true, DryRun: true,
		Stdout: &out, Stderr: &errb,
		Capacity: &rowCap{n: 4}, Launcher: l,
	})
	if code != 0 {
		t.Fatalf("fill exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if len(l.cards) != 0 {
		t.Fatalf("a dry run launched %q", l.cards)
	}
	if got := len(readyCards(ready)); got != 1 {
		t.Fatalf("a dry run moved a card: ready holds %d", got)
	}
	line := strings.TrimSpace(out.String())
	for _, want := range []string{"FILL DRY", "air:capacity=4", "os=darwin", "ssh=glenn@100.117.59.68", "take=4"} {
		if !strings.Contains(line, want) {
			t.Errorf("the dry-run line %q does not carry %q", line, want)
		}
	}
}

// TestDryRunSaysWhyItCouldNotRead: an unreachable bench in a dry run is the whole point of
// the dry run, so the reason is on the line and the exit is 1.
func TestDryRunSaysWhyItCouldNotRead(t *testing.T) {
	dir := t.TempDir()
	var out, errb bytes.Buffer
	code := Fill(FillInput{
		Ready: filepath.Join(dir, "ready"), Launched: filepath.Join(dir, "launched"),
		Machines: mixedRegistry(t, dir),
		Benches:  []string{"air"}, Once: true, DryRun: true,
		Stdout: &out, Stderr: &errb,
		Capacity: deadCapacity{err: fmt.Errorf("exit status 255: ssh: connect to host 100.117.59.68 port 22: Connection refused")},
		Launcher: &rowLauncher{},
	})
	if code != 1 {
		t.Fatalf("a dry run nobody could reach exited %d, want 1", code)
	}
	if !strings.Contains(errb.String(), "Connection refused") {
		t.Fatalf("stderr = %q, want the ssh reason", errb.String())
	}
}
