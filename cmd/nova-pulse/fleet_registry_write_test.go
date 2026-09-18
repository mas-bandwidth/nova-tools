package main

// `nova-pulse fleet registry add` and `set` at the command line, and the wake table folded
// into the machines registry.
//
// The hurt these tests hold: on 2026-09-18 the fleet's machines.tsv -- the control file
// `nova-pulse fill` resolves every `--bench` through -- was edited three times by hand,
// twice with `sed` and once with a python one-liner. Nothing validated any of those edits.
// A `bench,runner` row written without its dated exception, a role spelt `benhc`, a name
// written twice: each of them puts a card on a CI runner host and stops the merge queue,
// and each of them is a test below.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writableRegistry is one machines file with its header, in a directory the verb may
// rename over. It is the seven-column shape the fleet had before this card, so every test
// here also exercises the migration.
func writableRegistry(t *testing.T) string {
	t.Helper()
	body := strings.Join([]string{
		"# The fleet's machines.",
		"# name<TAB>ssh<TAB>os/arch<TAB>roles<TAB>seat<TAB>cores<TAB>notes<TAB>provider<TAB>mac",
		"",
		"hulk\thulk\tlinux/x64\tbench,runner\tswarm-hulk\t64\tallow-shared=2026-09-18 CI runners beside the cards",
		"batman\tbatman\tdarwin/amd64\trunner\t-\t8\t2019 iMac Pro; CI-only",
		"",
	}, "\n")
	path := filepath.Join(t.TempDir(), "machines.tsv")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func body(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestRegistryAddWritesTheRowAndPrintsIt(t *testing.T) {
	path := writableRegistry(t)
	code, out, errs := runPower(t, "fleet", "registry", "add",
		"--machines", path, "--name", "vision", "--ssh", "vision", "--os", "linux/x64",
		"--roles", "bench", "--seat", "swarm-vision", "--cores", "64",
		"--notes", "four CI runners moved off; a card bench")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, errs)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 {
		t.Fatalf("printed %d lines, want the REGISTRY line and the row: %q", len(lines), out)
	}
	if !strings.HasPrefix(lines[0], "REGISTRY ADDED name=vision machines=") {
		t.Errorf("first line = %q, want REGISTRY ADDED name=vision machines=…", lines[0])
	}
	if !strings.HasPrefix(lines[1], "MACHINE vision ssh=vision os=linux/x64 roles=bench ") {
		t.Errorf("second line = %q, want the machine's row", lines[1])
	}
	if !strings.Contains(lines[1], "provider=self") || !strings.Contains(lines[1], "mac=-") {
		t.Errorf("the row does not carry the two new columns: %q", lines[1])
	}
	after := body(t, path)
	if !strings.Contains(after, "# The fleet's machines.") {
		t.Errorf("the write ate the header:\n%s", after)
	}
	if !strings.Contains(after, "\nvision\tvision\tlinux/x64\tbench\tswarm-vision\t64\t") {
		t.Errorf("the row is not in the file:\n%s", after)
	}
}

func TestRegistryAddRefusesADuplicateNameAndLeavesTheFileAlone(t *testing.T) {
	path := writableRegistry(t)
	before := body(t, path)
	code, _, errs := runPower(t, "fleet", "registry", "add",
		"--machines", path, "--name", "hulk", "--ssh", "hulk2", "--os", "linux/x64",
		"--roles", "bench", "--seat", "-", "--cores", "64", "--notes", "-")
	if code != 2 {
		t.Fatalf("exit = %d, want 2; a second hulk was written", code)
	}
	if !strings.Contains(errs, "REGISTRY REFUSED") || !strings.Contains(errs, "one line per machine") {
		t.Errorf("stderr = %q, want a REGISTRY REFUSED naming the rule", errs)
	}
	if body(t, path) != before {
		t.Error("a refused add still changed the file")
	}
}

func TestRegistryAddRefusesAnUnknownRole(t *testing.T) {
	path := writableRegistry(t)
	code, _, errs := runPower(t, "fleet", "registry", "add",
		"--machines", path, "--name", "vision", "--ssh", "vision", "--os", "linux/x64",
		"--roles", "benhc", "--seat", "-", "--cores", "64", "--notes", "-")
	if code != 2 {
		t.Fatalf("exit = %d, want 2; a typo'd role was written", code)
	}
	if !strings.Contains(errs, "benhc") || !strings.Contains(errs, "coordination") {
		t.Errorf("stderr = %q, want the typo and the roles it takes", errs)
	}
}

// This is the 2026-09-18 hand edit exactly: a machine given BOTH roles, with a note that
// does not carry the dated exception. A card on that machine is a slow CI shard and a red
// gate, so the verb refuses before the file is touched.
func TestRegistryAddRefusesASharedMachineWithNoDatedException(t *testing.T) {
	path := writableRegistry(t)
	before := body(t, path)
	code, _, errs := runPower(t, "fleet", "registry", "add",
		"--machines", path, "--name", "vision", "--ssh", "vision", "--os", "linux/x64",
		"--roles", "bench,runner", "--seat", "swarm-vision", "--cores", "64",
		"--notes", "four CI runners beside the cards")
	if code != 2 {
		t.Fatalf("exit = %d, want 2; a shared machine was written with no exception", code)
	}
	if !strings.Contains(errs, "allow-shared=") {
		t.Errorf("stderr = %q, want the note it wants and the day it must carry", errs)
	}
	if body(t, path) != before {
		t.Error("a refused add still changed the file")
	}
}

func TestRegistryAddRefusesAProviderOutsideTheSet(t *testing.T) {
	path := writableRegistry(t)
	code, _, errs := runPower(t, "fleet", "registry", "add",
		"--machines", path, "--name", "vision", "--ssh", "vision", "--os", "linux/x64",
		"--roles", "bench", "--seat", "-", "--cores", "64", "--notes", "-",
		"--provider", "nebulous")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errs, "nebulous") || !strings.Contains(errs, "hetzner") {
		t.Errorf("stderr = %q, want the typo and the providers it takes", errs)
	}
}

func TestRegistryAddRefusesAMissingColumnRatherThanGuessing(t *testing.T) {
	path := writableRegistry(t)
	code, _, errs := runPower(t, "fleet", "registry", "add", "--machines", path, "--name", "vision")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	for _, want := range []string{"--ssh is required", "--os is required", "--roles is required", "--cores"} {
		if !strings.Contains(errs, want) {
			t.Errorf("stderr = %q, does not say %q", errs, want)
		}
	}
}

func TestRegistrySetChangesOneColumnAndPrintsTheRow(t *testing.T) {
	path := writableRegistry(t)
	code, out, errs := runPower(t, "fleet", "registry", "set",
		"--machines", path, "--name", "batman", "--notes", "2019 iMac Pro, six darwin-x64 runners; CI-only")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, errs)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "REGISTRY SET name=batman fields=notes machines=") {
		t.Fatalf("out = %q, want REGISTRY SET name=batman fields=notes … and the row", out)
	}
	if !strings.HasPrefix(lines[1], "MACHINE batman ") {
		t.Errorf("second line = %q, want batman's row", lines[1])
	}
	after := body(t, path)
	if !strings.Contains(after, "six darwin-x64 runners") {
		t.Errorf("the note is not in the file:\n%s", after)
	}
	if !strings.Contains(after, "hulk\thulk\tlinux/x64\tbench,runner\tswarm-hulk\t64\tallow-shared=2026-09-18") {
		t.Errorf("set rewrote a row it was not given:\n%s", after)
	}
}

func TestRegistrySetRefusesANameTheFileDoesNotCarry(t *testing.T) {
	path := writableRegistry(t)
	code, _, errs := runPower(t, "fleet", "registry", "set", "--machines", path, "--name", "nowhere", "--notes", "-")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errs, "nowhere") || !strings.Contains(errs, "hulk") {
		t.Errorf("stderr = %q, want the unknown name and the machines it has", errs)
	}
}

func TestRegistrySetRefusesWhenItNamesNoColumn(t *testing.T) {
	path := writableRegistry(t)
	code, _, errs := runPower(t, "fleet", "registry", "set", "--machines", path, "--name", "hulk")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errs, "--provider") {
		t.Errorf("stderr = %q, want the columns it takes", errs)
	}
}

// The mac column and `set --mac` are how a bench that sleeps gets its wake address without
// a second file. The lan-bench must be a machine of this same registry.
func TestRegistrySetMacFoldsTheWakeRegistryIn(t *testing.T) {
	path := writableRegistry(t)
	code, out, errs := runPower(t, "fleet", "registry", "set",
		"--machines", path, "--name", "batman", "--mac", "00:00:5E:00:53:01@hulk")
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, errs)
	}
	if !strings.Contains(out, "mac=00:00:5e:00:53:01@hulk") {
		t.Errorf("out = %q, want the normalised address and its lan-bench", out)
	}
	code, _, errs = runPower(t, "fleet", "registry", "set",
		"--machines", path, "--name", "batman", "--mac", "00:00:5e:00:53:01@nowhere")
	if code != 2 {
		t.Fatalf("exit = %d, want 2; a lan-bench no machine names was written", code)
	}
	if !strings.Contains(errs, "nowhere") {
		t.Errorf("stderr = %q, want the lan-bench it could not find", errs)
	}
}

// wake reads the machines registry.

func wakeableRegistry(t *testing.T) string {
	t.Helper()
	body := strings.Join([]string{
		"hulk\thulk\tlinux/x64\tbench\tswarm-hulk\t64\t-\tself\t-",
		"batman\tbatman\tdarwin/amd64\trunner\t-\t8\tCI-only\tself\t00:00:5e:00:53:01@hulk",
		"",
	}, "\n")
	path := filepath.Join(t.TempDir(), "machines.tsv")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestWakeReadsTheMachinesRegistry(t *testing.T) {
	runner := &fakePowerRunner{}
	lister := &fakePowerRunners{list: []powerRunnerInfo{{Name: "batman-1", Status: "offline"}}, onlineAfter: 2}
	withPowerFakes(t, runner, lister, newPowerTestClock())
	code, out, errs := runPower(t, "wake", "--bench", "batman", "--machines", wakeableRegistry(t))
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s out=%s", code, errs, out)
	}
	if !strings.HasPrefix(out, "WAKE batman up after ") {
		t.Errorf("out = %q, want the WAKE line", out)
	}
	// The packet leaves from the lan-bench the mac column named, and nowhere else.
	if len(runner.to("hulk")) != 1 {
		t.Errorf("the magic packet was broadcast %d times from hulk, want once", len(runner.to("hulk")))
	}
	if strings.Contains(errs, "NOTE") {
		t.Errorf("stderr = %q, want no retirement NOTE when --machines was given", errs)
	}
}

func TestWakeRefusesABenchTheRegistryDoesNotWake(t *testing.T) {
	runner := &fakePowerRunner{}
	withPowerFakes(t, runner, &fakePowerRunners{}, newPowerTestClock())
	code, _, errs := runPower(t, "wake", "--bench", "hulk", "--machines", wakeableRegistry(t))
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if runner.count() != 0 {
		t.Errorf("%d remote steps ran before the table was validated", runner.count())
	}
	if !strings.Contains(errs, "hulk") || !strings.Contains(errs, "nova-pulse fleet registry") {
		t.Errorf("stderr = %q, want the bench and how to list the machines", errs)
	}
}

// --registry reads for one release and says so every time, so the old CSV cannot quietly
// stay the fleet's second source of truth.
func TestWakeRegistryFlagStillReadsAndPrintsTheRetirementNote(t *testing.T) {
	reg := writePowerRegistry(t, "batman,00:00:5e:00:53:01,hulk\n")
	runner := &fakePowerRunner{}
	lister := &fakePowerRunners{list: []powerRunnerInfo{{Name: "batman-1", Status: "offline"}}, onlineAfter: 2}
	withPowerFakes(t, runner, lister, newPowerTestClock())
	code, out, errs := runPower(t, "wake", "--bench", "batman", "--registry", reg)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%s", code, errs)
	}
	if !strings.HasPrefix(out, "WAKE batman up after ") {
		t.Errorf("out = %q, want the WAKE line", out)
	}
	if !strings.Contains(errs, "NOTE") || !strings.Contains(errs, "--machines") {
		t.Errorf("stderr = %q, want the NOTE naming --machines", errs)
	}
}

func TestWakeRefusesBothTablesAtOnce(t *testing.T) {
	runner := &fakePowerRunner{}
	withPowerFakes(t, runner, &fakePowerRunners{}, newPowerTestClock())
	code, _, errs := runPower(t, "wake", "--bench", "batman",
		"--machines", wakeableRegistry(t), "--registry", writePowerRegistry(t, "batman,00:00:5e:00:53:01,hulk\n"))
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errs, "the same table") {
		t.Errorf("stderr = %q, want the reason the two flags cannot both be given", errs)
	}
	if runner.count() != 0 {
		t.Errorf("%d remote steps ran on a refused invocation", runner.count())
	}
}

func TestWakeWithNeitherTableRefusesRatherThanGuessing(t *testing.T) {
	runner := &fakePowerRunner{}
	withPowerFakes(t, runner, &fakePowerRunners{}, newPowerTestClock())
	code, _, errs := runPower(t, "wake", "--bench", "batman")
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errs, "--machines is required") {
		t.Errorf("stderr = %q, want --machines named", errs)
	}
}

// The help line is where a person finds the two writing verbs at all.
func TestHelpCarriesTheRegistryWritingVerbs(t *testing.T) {
	_, out, _ := runPower(t, "help")
	for _, want := range []string{
		"nova-pulse fleet registry add --machines",
		"nova-pulse fleet registry set --machines",
		"nova-pulse wake    --bench <name>... --machines <file>",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("help does not carry %q", want)
		}
	}
}
