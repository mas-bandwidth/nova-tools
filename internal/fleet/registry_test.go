package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// write puts one machines file in a temp directory and returns its path.
func write(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "machines.tsv")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o644))
	return path
}

// row builds one tab-separated line so a test reads as a table and not as escapes.
func row(fields ...string) string { return strings.Join(fields, "\t") + "\n" }

const benchRow = "hulk\thulk\tlinux/x64\tbench\tswarm-hulk\t64\t-\n"

func TestReadRegistryReadsEveryColumn(t *testing.T) {
	t.Parallel()

	path := write(t, "# a comment\n\n"+row("batman", "batman", "darwin/amd64", "runner", "-", "8", "2019 iMac Pro, six runners"))
	reg, err := ReadRegistry(path)
	require.NoError(t, err, "ReadRegistry: %v", err)
	m, ok := reg.Lookup("batman")
	require.True(t, ok, "batman is not in the registry")
	assert.Equal(t, "batman", m.SSH, "ssh/os/arch read back as %q/%q/%q", m.SSH, m.OS, m.Arch)
	assert.Equal(t, "darwin", m.OS, "ssh/os/arch read back as %q/%q/%q", m.SSH, m.OS, m.Arch)
	assert.Equal(t, "amd64", m.Arch, "ssh/os/arch read back as %q/%q/%q", m.SSH, m.OS, m.Arch)
	assert.Equal(t, "", m.Seat, "seat `-` read back as %q, want no seat", m.Seat)
	assert.Equal(t, 8, m.Cores, "cores read back as %d, want 8", m.Cores)
	assert.Equal(t, "2019 iMac Pro, six runners", m.Notes, "notes read back as %q", m.Notes)
	assert.True(t, m.HasRole(RoleRunner), "roles read back as %q, want runner and not bench", m.RoleList())
	assert.False(t, m.HasRole(RoleBench), "roles read back as %q, want runner and not bench", m.RoleList())
}

func TestReadRegistryRefusesAMachineThatIsBothRunnerAndBenchWithoutTheNote(t *testing.T) {
	t.Parallel()

	path := write(t, row("hulk", "hulk", "linux/x64", "bench,runner", "swarm-hulk", "64", "eight runners beside the cards"))
	reg, err := ReadRegistry(path)
	require.NoError(t, err, "a shared row without the note refused the whole registry: %v", err)
	_, ok := reg.Lookup("hulk")
	require.False(t, ok, "the poisoned row was in the bench set")
	got := len(reg.Machines())
	assert.Equal(t, 0, got, "the registry carries %d machines, want none: the poisoned row is not a machine", got)
	// The name stays taken: a second row under it is a duplicate, not a way round the lock.
	twice := write(t, row("hulk", "hulk", "linux/x64", "bench,runner", "swarm-hulk", "64", "no note")+
		row("hulk", "hulk2", "linux/x64", "bench", "swarm-hulk", "64", "-"))
	_, err = ReadRegistry(twice)
	assert.Error(t, err, "a second row under a lock-failed name was accepted")
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
	require.NoError(t, err, "one poisoned row among five refused the whole registry: %v", err)
	for _, name := range []string{"b1", "b2", "b3", "b4"} {
		_, ok := reg.Lookup(name)
		assert.True(t, ok, "neighbour %s is missing", name)
	}
	_, ok := reg.Lookup("hetzner")
	assert.False(t, ok, "the poisoned row was in the bench set")
	got := len(reg.Machines())
	assert.Equal(t, 4, got, "the registry carries %d machines, want the four neighbours", got)
}

func TestReadRegistryAcceptsTheSharedExceptionWithItsDateAndReason(t *testing.T) {
	t.Parallel()

	path := write(t, row("hulk", "hulk", "linux/x64", "bench,runner", "swarm-hulk", "64",
		"allow-shared=2026-09-18 the pull worker does not containerise cards yet"))
	reg, err := ReadRegistry(path)
	require.NoError(t, err, "the dated exception was refused: %v", err)
	m, _ := reg.Lookup("hulk")
	date, why, ok := m.AllowShared()
	assert.True(t, ok, "AllowShared read back %q/%q/%v", date, why, ok)
	assert.Equal(t, "2026-09-18", date, "AllowShared read back %q/%q/%v", date, why, ok)
	assert.Equal(t, "the pull worker does not containerise cards yet", why, "AllowShared read back %q/%q/%v", date, why, ok)
	assert.True(t, m.HasRole(RoleBench), "a shared machine is still a bench: roles %q", m.RoleList())
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
		if !assert.NoError(t, err, "%s: the whole registry was refused: %v", name, err) {
			continue
		}
		_, ok := reg.Lookup("hulk")
		assert.False(t, ok, "%s: %q was accepted as a bench", name, note)
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
		_, err := ReadRegistry(path)
		if assert.Error(t, err, "%s: accepted", name) {
			assert.Contains(t, err.Error(), "machines.tsv", "%s: refusal %q does not name the file", name, err)
		}
	}
}

func TestReadRegistryRefusesAnEmptyFileRatherThanPretendTheFleetIsEmpty(t *testing.T) {
	t.Parallel()

	_, err := ReadRegistry(write(t, "# only comments\n"))
	require.Error(t, err, "a registry with no machine was accepted")
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
	assert.Equal(t, "hulk,vision,threadripper-wsl,space", strings.Join(names, ","), "the benches are %v, want hulk, vision, threadripper-wsl, space in file order", names)
	got := len(reg.Machines())
	assert.Equal(t, 8, got, "the fleet has %d machines, want 8", got)
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
		if !assert.True(t, ok, "%s is not in the example", name) {
			continue
		}
		assert.Equal(t, roles, m.RoleList(), "%s has roles %q, want %q", name, m.RoleList(), roles)
		if m.HasRole(RoleBench) && m.HasRole(RoleRunner) {
			_, _, ok := m.AllowShared()
			assert.True(t, ok, "%s is shared with no dated exception", name)
		}
	}
	for _, name := range []string{"batman", "superman", "studio", "mini"} {
		m, _ := reg.Lookup(name)
		assert.False(t, m.HasRole(RoleBench), "the example lets a card reach %s", name)
	}
	// AND NO WINDOWS LINE. This is the ruling made mechanical rather than left in
	// the file's header comment: a Windows box joins this fleet through WSL2, as a
	// linux/x64 line with the Linux bench standard and ordinary Linux runner
	// labels, or it does not join.
	for _, m := range reg.Machines() {
		assert.NotEqual(t, "windows", m.OS, "%s is a windows machine in the registry; the native windows CI runners were dropped on 2026-09-18 (Glenn: \"WSL only from now on\") and a Windows box joins as a linux/x64 line under WSL2, like threadripper-wsl", m.Name)
	}
}

func TestAMissingFileIsARefusalThatNamesIt(t *testing.T) {
	t.Parallel()

	_, err := ReadRegistry(filepath.Join(t.TempDir(), "nowhere.tsv"))
	require.Error(t, err, "a missing machines file was accepted")
	assert.Contains(t, err.Error(), "nowhere.tsv", "refusal %q does not name the file", err)
}

// example reads the shipped fleet example the same way a verb does.
func example(t *testing.T) *Registry {
	t.Helper()
	reg, err := ReadRegistry(filepath.Join("testdata", "machines.tsv"))
	require.NoError(t, err, "the shipped example does not read: %v", err)
	return reg
}
