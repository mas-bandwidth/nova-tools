package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
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
	t.Parallel()

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
	t.Parallel()

	path := write(t, row("hulk", "hulk", "linux/x64", "bench,runner", "swarm-hulk", "64", "eight runners beside the cards"))
	reg, err := ReadRegistry(path)
	if err != nil {
		t.Fatalf("a shared row without the note refused the whole registry: %v", err)
	}
	if _, ok := reg.Lookup("hulk"); ok {
		t.Fatal("the poisoned row was in the bench set")
	}
	if got := len(reg.Machines()); got != 0 {
		t.Errorf("the registry carries %d machines, want none: the poisoned row is not a machine", got)
	}
	// The name stays taken: a second row under it is a duplicate, not a way round the lock.
	twice := write(t, row("hulk", "hulk", "linux/x64", "bench,runner", "swarm-hulk", "64", "no note")+
		row("hulk", "hulk2", "linux/x64", "bench", "swarm-hulk", "64", "-"))
	if _, err := ReadRegistry(twice); err == nil {
		t.Error("a second row under a lock-failed name was accepted")
	}
}

// TestReadRegistryDoesNotRefuseTheFleetForOnePoisonedSharedRow is the registry half of
// #2031: one bench,runner row without allow-shared= must not make ReadRegistry fail the
// whole file. The four neighbours still load; the poisoned name is not a bench.
func TestReadRegistryDoesNotRefuseTheFleetForOnePoisonedSharedRow(t *testing.T) {
	t.Parallel()

	var body strings.Builder
	for _, name := range []string{"b1", "b2", "b3", "b4"} {
		body.WriteString(row(name, name, "linux/x64", "bench", "swarm-"+name, "64", "-"))
	}
	body.WriteString(row("hetzner", "hetzner", "linux/x64", "bench,runner", "swarm-hetzner", "64", "added with no allow-shared note"))
	reg, err := ReadRegistry(write(t, body.String()))
	if err != nil {
		t.Fatalf("one poisoned row among five refused the whole registry: %v", err)
	}
	for _, name := range []string{"b1", "b2", "b3", "b4"} {
		if _, ok := reg.Lookup(name); !ok {
			t.Errorf("neighbour %s is missing", name)
		}
	}
	if _, ok := reg.Lookup("hetzner"); ok {
		t.Error("the poisoned row was in the bench set")
	}
	if got := len(reg.Machines()); got != 4 {
		t.Errorf("the registry carries %d machines, want the four neighbours", got)
	}
}

func TestReadRegistryAcceptsTheSharedExceptionWithItsDateAndReason(t *testing.T) {
	t.Parallel()

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
	if !m.HasRole(RoleBench) {
		t.Errorf("a shared machine is still a bench: roles %q", m.RoleList())
	}
}

func TestReadRegistryRefusesASharedNoteWithNoDateAndOneWithNoReason(t *testing.T) {
	t.Parallel()

	for name, note := range map[string]string{
		"no date":   "allow-shared=the pull worker does not containerise cards yet",
		"no reason": "allow-shared=2026-09-18",
		"bad date":  "allow-shared=18-09-2026 the pull worker does not containerise cards yet",
	} {
		path := write(t, row("hulk", "hulk", "linux/x64", "bench,runner", "swarm-hulk", "64", note))
		reg, err := ReadRegistry(path)
		if err != nil {
			t.Errorf("%s: the whole registry was refused: %v", name, err)
			continue
		}
		if _, ok := reg.Lookup("hulk"); ok {
			t.Errorf("%s: %q was accepted as a bench", name, note)
		}
	}
}

func TestReadRegistryRefusesWhatItCannotRead(t *testing.T) {
	t.Parallel()

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
	t.Parallel()

	if _, err := ReadRegistry(write(t, "# only comments\n")); err == nil {
		t.Fatal("a registry with no machine was accepted")
	}
}

func TestMachinesReadInFileOrder(t *testing.T) {
	t.Parallel()

	reg := example(t)
	var names []string
	for _, m := range reg.Machines() {
		if m.HasRole(RoleBench) {
			names = append(names, m.Name)
		}
	}
	if strings.Join(names, ",") != "hulk,vision,threadripper-wsl,space" {
		t.Errorf("the benches are %v, want hulk, vision, threadripper-wsl, space in file order", names)
	}
	if got := len(reg.Machines()); got != 8 {
		t.Errorf("the fleet has %d machines, want 8", got)
	}
}

// TestRefusalLineIsOneLineWithItsReasonAndItsRemedy holds the line a verb prints for a
// machine the registry does not carry.
func TestRefusalLineIsOneLineWithItsReasonAndItsRemedy(t *testing.T) {
	t.Parallel()

	r := &Refusal{Name: "nobody", Reason: ReasonUnknown, Remedy: "add it, or name a machine the registry carries"}
	line := r.Line("CERTIFY")
	assert.True(t, strings.HasPrefix(line, "CERTIFY REFUSED bench=nobody reason=unknown-machine remedy=\""), "the refusal line is %q", line)
	assert.NotContains(t, line, "\n", "the refusal line is more than one line")
}

// TestTheExampleIsTheFleetWeHave holds the shipped example against the fleet as it stands
// on 2026-09-18, so the file cannot drift into a shape the lock does not hold: the four
// benches, and every runner host CI-only.
//
// threadripper-wsl is the fleet's Windows box and it is a LINUX line, which is the whole
// point of it (Glenn 2026-09-18: "drop the native windows CI runners. WSL only from now
// on."). WSL2 is what the cards and the CI runners see, so it takes the Linux bench
// standard and the ordinary Linux runner labels, and no line in this file says `windows`.
func TestTheExampleIsTheFleetWeHave(t *testing.T) {
	t.Parallel()

	reg := example(t)
	want := map[string]string{
		"studio":           "coordination,runner",
		"hulk":             "bench,runner",
		"vision":           "bench,runner",
		"threadripper-wsl": "bench,runner",
		"space":            "bench,services",
		"mini":             "runner",
		"batman":           "runner",
		"superman":         "runner",
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
		if m, _ := reg.Lookup(name); m.HasRole(RoleBench) {
			t.Errorf("the example lets a card reach %s", name)
		}
	}
	// AND NO WINDOWS LINE. This is the ruling made mechanical rather than left in
	// the file's header comment: a Windows box joins this fleet through WSL2, as a
	// linux/x64 line with the Linux bench standard and ordinary Linux runner
	// labels, or it does not join.
	for _, m := range reg.Machines() {
		if m.OS == "windows" {
			t.Errorf("%s is a windows machine in the registry; the native windows CI runners were dropped on 2026-09-18 (Glenn: \"WSL only from now on\") and a Windows box joins as a linux/x64 line under WSL2, like threadripper-wsl", m.Name)
		}
	}
}

func TestAMissingFileIsARefusalThatNamesIt(t *testing.T) {
	t.Parallel()

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
