package fleet

// The writing side of the registry: `Add`, `Set`, `Save`, and the two columns the wake
// registry folded in. Every test here is one of the mistakes a hand edit made or could
// have made on 2026-09-18.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// header is the comment block a real machines file carries. A writer that eats it eats the
// lock, so every write test keeps it in the fixture.
const header = "# The fleet's machines.\n# name<TAB>ssh<TAB>os/arch<TAB>roles<TAB>seat<TAB>cores<TAB>notes<TAB>provider<TAB>mac\n\n"

// draft is one whole machine, so a test names only the field it is about.
func draft(name string) Draft {
	return Draft{
		Name: name, SSH: name, OSArch: "linux/x64", Roles: "bench",
		Seat: "swarm-" + name, Cores: 8, Notes: "-", Provider: ProviderSelf, MAC: "-",
	}
}

func readBack(t *testing.T, path string) *Registry {
	t.Helper()
	reg, err := ReadRegistry(path)
	if err != nil {
		t.Fatalf("the file this verb wrote does not read: %v", err)
	}
	return reg
}

func TestAddWritesTheRowAndKeepsTheHeaderAndTheOtherMachines(t *testing.T) {
	path := write(t, header+benchRow)
	reg := readBack(t, path)
	next, m, err := reg.Add(draft("vision"))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	if err := next.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if m.Name != "vision" || m.Cores != 8 {
		t.Errorf("the added machine read back as %+v", m)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	body := string(raw)
	if !strings.HasPrefix(body, header) {
		t.Errorf("the write ate the file's header:\n%s", body)
	}
	after := readBack(t, path)
	if len(after.Machines()) != 2 {
		t.Errorf("the file has %d machines, want hulk and vision", len(after.Machines()))
	}
	if _, ok := after.Lookup("hulk"); !ok {
		t.Error("the write lost hulk")
	}
	// Nine columns, always: the reader takes seven for one release, the writer never writes
	// a column a reader has to guess at.
	row := after.Machines()[1]
	if got := len(strings.Split(row.Row(), "\t")); got != machineFieldsMax {
		t.Errorf("the written row has %d columns, want %d", got, machineFieldsMax)
	}
}

func TestAddRefusesADuplicateNameAndWritesNothing(t *testing.T) {
	path := write(t, header+benchRow)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, _, addErr := readBack(t, path).Add(draft("hulk"))
	if addErr == nil {
		t.Fatal("a second hulk was accepted")
	}
	for _, want := range []string{"hulk", "one line per machine"} {
		if !strings.Contains(addErr.Error(), want) {
			t.Errorf("refusal %q does not carry %q", addErr, want)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("a refused add still changed the file:\n%s", after)
	}
}

func TestAddRefusesAnUnknownRole(t *testing.T) {
	d := draft("vision")
	d.Roles = "bench,builder"
	_, _, err := readBack(t, write(t, header+benchRow)).Add(d)
	if err == nil {
		t.Fatal("an unknown role was accepted")
	}
	if !strings.Contains(err.Error(), "builder") || !strings.Contains(err.Error(), RoleCoordination) {
		t.Errorf("refusal %q does not name the typo and the roles", err)
	}
}

func TestAddRefusesASharedRowWithoutTheDatedException(t *testing.T) {
	d := draft("vision")
	d.Roles = "bench,runner"
	d.Notes = "four CI runners beside the cards"
	_, _, err := readBack(t, write(t, header+benchRow)).Add(d)
	if err == nil {
		t.Fatal("a bench+runner row with no dated exception was accepted")
	}
	if !strings.Contains(err.Error(), allowSharedPrefix) {
		t.Errorf("refusal %q does not name the note it wants", err)
	}
}

func TestAddRefusesAProviderOutsideTheSet(t *testing.T) {
	d := draft("vision")
	d.Provider = "nebulous"
	_, _, err := readBack(t, write(t, header+benchRow)).Add(d)
	if err == nil {
		t.Fatal("a provider outside the set was accepted")
	}
	for _, want := range []string{"nebulous", ProviderSelf, "hetzner"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not carry %q", err, want)
		}
	}
}

func TestSetChangesOnlyTheColumnsItNames(t *testing.T) {
	path := write(t, header+benchRow)
	notes := "allow-shared=2026-09-18 eight CI runners beside the cards"
	roles := "bench,runner"
	next, m, err := readBack(t, path).Set("hulk", Change{Notes: &notes, Roles: &roles})
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := next.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if m.Notes != notes || m.RoleList() != "bench,runner" {
		t.Errorf("the changed machine read back as %+v", m)
	}
	after, _ := readBack(t, path).Lookup("hulk")
	if after.SSH != "hulk" || after.Seat != "swarm-hulk" || after.Cores != 64 {
		t.Errorf("set rewrote a column it was not given: %+v", after)
	}
	if after.Provider != ProviderSelf {
		t.Errorf("provider read back as %q, want the default %q", after.Provider, ProviderSelf)
	}
}

func TestSetRefusesAMachineTheFileDoesNotName(t *testing.T) {
	notes := "-"
	_, _, err := readBack(t, write(t, header+benchRow)).Set("batman", Change{Notes: &notes})
	if err == nil {
		t.Fatal("a machine not in the file was changed")
	}
	for _, want := range []string{"batman", "hulk"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not carry %q", err, want)
		}
	}
}

func TestSetRefusesAChangeThatNamesNoColumn(t *testing.T) {
	_, _, err := readBack(t, write(t, header+benchRow)).Set("hulk", Change{})
	if err == nil {
		t.Fatal("a set naming no column was accepted")
	}
	if !strings.Contains(err.Error(), "--notes") {
		t.Errorf("refusal %q does not list the columns it takes", err)
	}
}

func TestSetRefusesARowTheReaderWouldRefuseAndWritesNothing(t *testing.T) {
	path := write(t, header+benchRow)
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	roles := "bench,runner" // no dated exception in the notes
	if _, _, err := readBack(t, path).Set("hulk", Change{Roles: &roles}); err == nil {
		t.Fatal("a shared row with no dated exception was written")
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("a refused set still changed the file:\n%s", after)
	}
}

func TestSaveIsAtomicAndLeavesNoTemporaryFileBehind(t *testing.T) {
	path := write(t, header+benchRow)
	next, _, err := readBack(t, path).Add(draft("vision"))
	if err != nil {
		t.Fatal(err)
	}
	if err := next.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != filepath.Base(path) {
			t.Errorf("Save left %q beside the registry", e.Name())
		}
	}
}

// The wake column.

func TestTheMacColumnCarriesTheAddressAndTheLanBench(t *testing.T) {
	path := write(t, header+benchRow+
		row("batman", "batman", "darwin/amd64", "runner", "-", "8", "CI-only", "self", "00:00:5E:00:53:01@hulk"))
	m, ok := readBack(t, path).Lookup("batman")
	if !ok {
		t.Fatal("batman is not in the registry")
	}
	if !m.Sleeps() {
		t.Fatal("a machine with a wake address does not say it sleeps")
	}
	// The address is normalised to what net.ParseMAC read, so one file cannot carry two
	// spellings of one machine's hardware.
	if m.MAC != "00:00:5e:00:53:01" || m.LAN != "hulk" {
		t.Errorf("the wake column read back as %q@%q", m.MAC, m.LAN)
	}
	if m.MACField() != "00:00:5e:00:53:01@hulk" {
		t.Errorf("the wake column writes back as %q", m.MACField())
	}
}

func TestAMacWithNoLanBenchIsRefused(t *testing.T) {
	_, err := ReadRegistry(write(t, benchRow+
		row("batman", "batman", "darwin/amd64", "runner", "-", "8", "-", "self", "00:00:5e:00:53:01")))
	if err == nil {
		t.Fatal("a wake address with nowhere to broadcast from was accepted")
	}
	if !strings.Contains(err.Error(), "lan-bench") {
		t.Errorf("refusal %q does not name what is missing", err)
	}
}

func TestALanBenchTheFileDoesNotNameIsRefused(t *testing.T) {
	_, err := ReadRegistry(write(t, benchRow+
		row("batman", "batman", "darwin/amd64", "runner", "-", "8", "-", "self", "00:00:5e:00:53:01@nowhere")))
	if err == nil {
		t.Fatal("a lan-bench no machine in the file names was accepted")
	}
	for _, want := range []string{"nowhere", "hulk"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not carry %q", err, want)
		}
	}
}

func TestAWakeAddressThatIsNotSixBytesIsRefused(t *testing.T) {
	_, err := ReadRegistry(write(t, benchRow+
		row("batman", "batman", "darwin/amd64", "runner", "-", "8", "-", "self", "not-an-address@hulk")))
	if err == nil {
		t.Fatal("a wake address that is not a hardware address was accepted")
	}
	if !strings.Contains(err.Error(), "d0:81:7a:d8:3a:ec") {
		t.Errorf("refusal %q does not show the shape it wants", err)
	}
}

// The migration: seven, eight and nine columns in one file, and the old CSV folded in.

func TestTheReaderTakesSevenEightAndNineColumnLines(t *testing.T) {
	path := write(t, strings.Join([]string{
		"seven\tseven\tlinux/x64\tbench\t-\t8\t-",
		"eight\teight\tlinux/x64\tbench\t-\t8\t-\thetzner",
		"nine\tnine\tdarwin/amd64\trunner\t-\t8\t-\tself\t00:00:5e:00:53:01@seven",
		"",
	}, "\n"))
	reg := readBack(t, path)
	seven, _ := reg.Lookup("seven")
	if seven.Provider != ProviderSelf || seven.Sleeps() {
		t.Errorf("a seven-column line read back as %+v; a machine that does not say is ours and never sleeps", seven)
	}
	eight, _ := reg.Lookup("eight")
	if eight.Provider != "hetzner" || eight.Sleeps() {
		t.Errorf("an eight-column line read back as %+v", eight)
	}
	nine, _ := reg.Lookup("nine")
	if nine.Provider != ProviderSelf || nine.LAN != "seven" {
		t.Errorf("a nine-column line read back as %+v", nine)
	}
}

func TestATenColumnLineIsRefused(t *testing.T) {
	_, err := ReadRegistry(write(t, benchRow+
		"ten\tten\tlinux/x64\tbench\t-\t8\t-\tself\t-\tspare\n"))
	if err == nil {
		t.Fatal("a line with a tenth column was accepted")
	}
	if !strings.Contains(err.Error(), "provider, mac") {
		t.Errorf("refusal %q does not name the columns it takes", err)
	}
}

// TestTheMigratedExampleCarriesEveryRowOfTheOldWakeRegistry is the migration itself: the
// shipped example is the fleet's file of 2026-09-18 with the mac column filled, and
// testdata/wake-registry.csv is the file it replaces. Every row of the old file must be a
// row of the new one, with the same address and the same lan-bench -- that is what "folded
// in" has to mean, and it is the one thing a migration can silently get wrong.
func TestTheMigratedExampleCarriesEveryRowOfTheOldWakeRegistry(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "wake-registry.csv"))
	if err != nil {
		t.Fatal(err)
	}
	reg := readBack(t, filepath.Join("testdata", "machines.tsv"))
	rows := 0
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Split(line, ",")
		if len(f) != 3 {
			t.Fatalf("the old registry's line %q is not name,mac,lan-bench", line)
		}
		rows++
		m, ok := reg.Lookup(f[0])
		if !ok {
			t.Errorf("%s could be woken and is not in the machines registry at all", f[0])
			continue
		}
		if m.MAC != strings.ToLower(f[1]) || m.LAN != f[2] {
			t.Errorf("%s was %s@%s and is now %s", f[0], f[1], f[2], m.MACField())
		}
	}
	if rows == 0 {
		t.Fatal("the old wake registry fixture is empty; the migration proves nothing")
	}
	sleepers := 0
	for _, m := range reg.Machines() {
		if m.Sleeps() {
			sleepers++
		}
	}
	if sleepers != rows {
		t.Errorf("the machines registry wakes %d machines, the old file %d; the fold added or lost a row", sleepers, rows)
	}
}
