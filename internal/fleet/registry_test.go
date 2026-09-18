package fleet

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// write puts one machines file in a temp directory and returns its path.
func write(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "machines.tsv")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// row builds one tab-separated line so a test reads as a table and not as escapes.
func row(fields ...string) string { return strings.Join(fields, "\t") + "\n" }

const benchRow = "hulk\thulk\tlinux/x64\tbench\tswarm-hulk\t64\t-\n"

func TestReadRegistryReadsEveryColumn(t *testing.T) {
	path := write(t, "# a comment\n\n"+row("batman", "batman", "darwin/amd64", "runner", "-", "8", "2019 iMac Pro, six runners"))
	reg, err := ReadRegistry(path)
	if err != nil {
		t.Fatalf("ReadRegistry: %v", err)
	}
	m, ok := reg.Lookup("batman")
	if !ok {
		t.Fatal("batman is not in the registry")
	}
	if m.SSH != "batman" || m.OS != "darwin" || m.Arch != "amd64" {
		t.Errorf("ssh/os/arch read back as %q/%q/%q", m.SSH, m.OS, m.Arch)
	}
	if m.Seat != "" {
		t.Errorf("seat `-` read back as %q, want no seat", m.Seat)
	}
	if m.Cores != 8 {
		t.Errorf("cores read back as %d, want 8", m.Cores)
	}
	if m.Notes != "2019 iMac Pro, six runners" {
		t.Errorf("notes read back as %q", m.Notes)
	}
	if !m.HasRole(RoleRunner) || m.HasRole(RoleBench) {
		t.Errorf("roles read back as %q, want runner and not bench", m.RoleList())
	}
}

func TestReadRegistryRefusesAMachineThatIsBothRunnerAndBenchWithoutTheNote(t *testing.T) {
	path := write(t, row("hulk", "hulk", "linux/x64", "bench,runner", "swarm-hulk", "64", "eight runners beside the cards"))
	_, err := ReadRegistry(path)
	if err == nil {
		t.Fatal("a shared runner+bench with no allow-shared note was accepted")
	}
	for _, want := range []string{"hulk", "line 1", "allow-shared"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not name %q", err, want)
		}
	}
}

func TestReadRegistryAcceptsTheSharedExceptionWithItsDateAndReason(t *testing.T) {
	path := write(t, row("hulk", "hulk", "linux/x64", "bench,runner", "swarm-hulk", "64",
		"allow-shared=2026-09-18 the pull worker does not containerise cards yet"))
	reg, err := ReadRegistry(path)
	if err != nil {
		t.Fatalf("the dated exception was refused: %v", err)
	}
	m, _ := reg.Lookup("hulk")
	date, why, ok := m.AllowShared()
	if !ok || date != "2026-09-18" || why != "the pull worker does not containerise cards yet" {
		t.Errorf("AllowShared read back %q/%q/%v", date, why, ok)
	}
	if err := reg.RequireBench("hulk"); err != nil {
		t.Errorf("a shared machine is still a bench: %v", err)
	}
}

func TestReadRegistryRefusesASharedNoteWithNoDateAndOneWithNoReason(t *testing.T) {
	for name, note := range map[string]string{
		"no date":   "allow-shared=the pull worker does not containerise cards yet",
		"no reason": "allow-shared=2026-09-18",
		"bad date":  "allow-shared=18-09-2026 the pull worker does not containerise cards yet",
	} {
		path := write(t, row("hulk", "hulk", "linux/x64", "bench,runner", "swarm-hulk", "64", note))
		if _, err := ReadRegistry(path); err == nil {
			t.Errorf("%s: %q was accepted", name, note)
		}
	}
}

func TestReadRegistryRefusesWhatItCannotRead(t *testing.T) {
	cases := map[string]string{
		"six fields":      "hulk\thulk\tlinux/x64\tbench\tswarm-hulk\t64\n",
		"unknown role":    row("hulk", "hulk", "linux/x64", "bench,builder", "swarm-hulk", "64", "-"),
		"no role":         row("hulk", "hulk", "linux/x64", "", "swarm-hulk", "64", "-"),
		"repeated role":   row("hulk", "hulk", "linux/x64", "bench,bench", "swarm-hulk", "64", "-"),
		"no name":         row("", "hulk", "linux/x64", "bench", "swarm-hulk", "64", "-"),
		"no ssh":          row("hulk", "", "linux/x64", "bench", "swarm-hulk", "64", "-"),
		"os without arch": row("hulk", "hulk", "linux", "bench", "swarm-hulk", "64", "-"),
		"cores in words":  row("hulk", "hulk", "linux/x64", "bench", "swarm-hulk", "sixty-four", "-"),
		"cores at zero":   row("hulk", "hulk", "linux/x64", "bench", "swarm-hulk", "0", "-"),
		"twice named":     benchRow + row("hulk", "hulk2", "linux/x64", "bench", "swarm-hulk", "64", "-"),
	}
	for name, body := range cases {
		path := write(t, body)
		if _, err := ReadRegistry(path); err == nil {
			t.Errorf("%s: accepted", name)
		} else if !strings.Contains(err.Error(), "machines.tsv") {
			t.Errorf("%s: refusal %q does not name the file", name, err)
		}
	}
}

func TestReadRegistryRefusesAnEmptyFileRatherThanPretendTheFleetIsEmpty(t *testing.T) {
	if _, err := ReadRegistry(write(t, "# only comments\n")); err == nil {
		t.Fatal("a registry with no machine was accepted")
	}
}

func TestRequireBenchRefusesARunnerHostWithItsReasonAndItsRemedy(t *testing.T) {
	reg := example(t)
	err := reg.RequireBench("batman")
	if err == nil {
		t.Fatal("batman, a CI-only runner host, was accepted as a bench")
	}
	var refusal *Refusal
	if !errors.As(err, &refusal) {
		t.Fatalf("RequireBench returned %T, want *fleet.Refusal", err)
	}
	if refusal.Reason != ReasonRunnerHost {
		t.Errorf("reason is %q, want %q", refusal.Reason, ReasonRunnerHost)
	}
	line := refusal.Line("FILL")
	if !strings.HasPrefix(line, "FILL REFUSED bench=batman reason=runner-host remedy=\"") {
		t.Errorf("the refusal line is %q", line)
	}
	if strings.Count(line, "\n") != 0 {
		t.Errorf("the refusal line is more than one line: %q", line)
	}
	if !strings.Contains(line, "hulk") {
		t.Errorf("the remedy names no bench to use instead: %q", line)
	}
}

func TestRequireBenchRefusesTheCoordinationHostAndAnUnknownMachine(t *testing.T) {
	reg := example(t)
	for name, want := range map[string]string{
		"studio": ReasonRunnerHost, // the Studio serves the lisp shards as well as coordinating
		"nobody": ReasonUnknown,
	} {
		err := reg.RequireBench(name)
		var refusal *Refusal
		if !errors.As(err, &refusal) {
			t.Fatalf("%s: RequireBench returned %v", name, err)
		}
		if refusal.Reason != want {
			t.Errorf("%s: reason is %q, want %q", name, refusal.Reason, want)
		}
	}
}

func TestRequireBenchAcceptsEveryBenchInTheFleet(t *testing.T) {
	reg := example(t)
	for _, name := range []string{"hulk", "vision", "space"} {
		if err := reg.RequireBench(name); err != nil {
			t.Errorf("%s is a bench, and was refused: %v", name, err)
		}
	}
}

func TestWithRoleAndMachinesReadInFileOrder(t *testing.T) {
	reg := example(t)
	var names []string
	for _, m := range reg.WithRole(RoleBench) {
		names = append(names, m.Name)
	}
	if strings.Join(names, ",") != "hulk,vision,space,air" {
		t.Errorf("the benches are %v, want hulk, vision, space, air in file order", names)
	}
	if got := len(reg.Machines()); got != 8 {
		t.Errorf("the fleet has %d machines, want 8", got)
	}
	if reg.WithRole("builder") != nil {
		t.Error("an unknown role listed machines")
	}
}

// TestTheExampleIsTheFleetWeHave holds the shipped example against the fleet as it stands
// on 2026-09-18, so the file cannot drift into a shape the lock does not hold: the three
// benches, and every runner host CI-only.
func TestTheExampleIsTheFleetWeHave(t *testing.T) {
	reg := example(t)
	want := map[string]string{
		"studio":   "coordination,runner",
		"hulk":     "bench,runner",
		"vision":   "bench,runner",
		"space":    "bench,runner,services",
		"mini":     "runner",
		"batman":   "runner",
		"superman": "runner",
		"air":      "bench,runner",
	}
	for name, roles := range want {
		m, ok := reg.Lookup(name)
		if !ok {
			t.Errorf("%s is not in the example", name)
			continue
		}
		if m.RoleList() != roles {
			t.Errorf("%s has roles %q, want %q", name, m.RoleList(), roles)
		}
		if m.HasRole(RoleBench) && m.HasRole(RoleRunner) {
			if _, _, ok := m.AllowShared(); !ok {
				t.Errorf("%s is shared with no dated exception", name)
			}
		}
	}
	for _, name := range []string{"batman", "superman", "studio", "mini"} {
		if err := reg.RequireBench(name); err == nil {
			t.Errorf("the example lets a card reach %s", name)
		}
	}
}

func TestAMissingFileIsARefusalThatNamesIt(t *testing.T) {
	_, err := ReadRegistry(filepath.Join(t.TempDir(), "nowhere.tsv"))
	if err == nil {
		t.Fatal("a missing machines file was accepted")
	}
	if !strings.Contains(err.Error(), "nowhere.tsv") {
		t.Errorf("refusal %q does not name the file", err)
	}
}

// example reads the shipped fleet example the same way a verb does.
func example(t *testing.T) *Registry {
	t.Helper()
	reg, err := ReadRegistry(filepath.Join("testdata", "machines.tsv"))
	if err != nil {
		t.Fatalf("the shipped example does not read: %v", err)
	}
	return reg
}
