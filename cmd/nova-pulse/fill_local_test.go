package main

// The red tests of rule (2) of 2026-09-18: `fill` had no local transport. Filling the bench
// it was running on asked that bench to ssh to itself and got `glenn@hulk: Permission
// denied` -- no machine in the fleet holds its own key, by design -- so every card the tick
// would have placed there was lost to a transport error that had nothing to do with
// capacity. A bench whose registry row resolves to this host is reached without ssh.
//
// Both seams are driven through the fakes on PATH: an `ssh` fake that FAILS the test if it
// is ever started, and the local shell and launcher answering in its place.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/pulse"
)

// localFakes puts the local seam's programs on PATH: `sh` answering the capacity formula,
// the two launchers recording their argv, and an `ssh` that exits non-zero with a line that
// names the very failure this rule removes.
func localFakes(t *testing.T, capacity string) (specs, arglog string) {
	t.Helper()
	specs = fakePATH(t)
	arglog = filepath.Join(t.TempDir(), "argv.log")
	fakeTool(t, specs, "sh", fakeSpec{Log: arglog, Default: fakeRule{Stdout: capacity}})
	fakeTool(t, specs, "ssh", fakeSpec{Log: arglog, Default: fakeRule{
		Exit: 255, Stderr: "glenn@hulk: Permission denied (publickey)."}})
	fakeTool(t, specs, "flash-native-bench.sh", fakeSpec{Log: arglog})
	fakeTool(t, specs, "flash-native-local.sh", fakeSpec{Log: arglog})
	return specs, arglog
}

// TestFillReadsTheLocalBenchsCapacityWithoutSSH: the capacity of the machine you are
// standing on is read here, with the same formula, and no connection is opened.
func TestFillReadsTheLocalBenchsCapacityWithoutSSH(t *testing.T) {
	_, arglog := localFakes(t, "12")
	dir := t.TempDir()
	machines := fillMachines(t, dir, "hulk", "vision")
	local := pulse.LocalBenches(machines, "hulk")
	if !local["hulk"] || local["vision"] {
		t.Fatalf("LocalBenches on hulk = %v, want hulk alone", local)
	}
	got, err := sshCapacity{local: local}.Capacity("hulk")
	if err != nil {
		t.Fatalf("the local capacity read failed: %v", err)
	}
	if got != 12 {
		t.Errorf("capacity = %d, want the 12 the formula answered", got)
	}
	log, _ := os.ReadFile(arglog)
	if strings.Contains(string(log), "ssh ") {
		t.Errorf("an ssh was opened to the machine fill is running on:\n%s", log)
	}
	if !strings.Contains(string(log), "sh -c") {
		t.Errorf("the capacity formula was not run locally:\n%s", log)
	}
}

// TestFillStillReadsARemoteBenchOverSSH: the rule narrows to this host and nothing else. A
// bench that is not this machine is still an ssh, exactly as before.
func TestFillStillReadsARemoteBenchOverSSH(t *testing.T) {
	_, arglog := localFakes(t, "12")
	dir := t.TempDir()
	machines := fillMachines(t, dir, "hulk", "vision")
	local := pulse.LocalBenches(machines, "hulk")
	if _, err := (sshCapacity{local: local}).Capacity("vision"); err == nil {
		t.Fatal("the remote capacity read did not go through the failing ssh fake")
	}
	log, _ := os.ReadFile(arglog)
	if !strings.Contains(string(log), "ssh -n") {
		t.Errorf("a bench that is not this machine was not read over ssh:\n%s", log)
	}
}

// TestFillLaunchesALocalCardWithTheLocalLauncher: the remote launcher's first act is
// `ssh -n <bench> true`, which on the bench itself is a refused launch, every card, every
// tick. A local card goes to --launcher-local, with no bench argument, because there is no
// bench to reach.
func TestFillLaunchesALocalCardWithTheLocalLauncher(t *testing.T) {
	_, arglog := localFakes(t, "12")
	dir := t.TempDir()
	machines := fillMachines(t, dir, "hulk", "vision")
	local := pulse.LocalBenches(machines, "hulk")
	l := flashLauncher{local: local}
	card := filepath.Join(dir, "card-9601.md")
	if err := os.WriteFile(card, []byte("a card\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := l.Launch("hulk", card); err != nil {
		t.Fatalf("the local launch failed: %v", err)
	}
	if err := l.Launch("vision", card); err != nil {
		t.Fatalf("the remote launch failed: %v", err)
	}
	log, _ := os.ReadFile(arglog)
	lines := strings.Split(strings.TrimSpace(string(log)), "\n")
	if len(lines) != 2 {
		t.Fatalf("the two launches ran %d programs:\n%s", len(lines), log)
	}
	if !strings.HasPrefix(lines[0], "flash-native-local.sh swarm-hulk ") {
		t.Errorf("the local card did not go to the local launcher without a bench: %q", lines[0])
	}
	if strings.Contains(lines[0], " hulk ") {
		t.Errorf("the local launcher was handed a bench to ssh to: %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], "flash-native-bench.sh vision swarm-vision ") {
		t.Errorf("the remote card did not go to the bench launcher: %q", lines[1])
	}
	if !strings.HasSuffix(lines[0], " card-9601 "+strconv.Itoa(defaultCardDeadline)) {
		t.Errorf("the local launcher did not get the label and the deadline: %q", lines[0])
	}
}

// TestLocalBenchIsResolvedFromTheRegistryNotTheName: `--bench space` is an ~/.ssh/config
// alias and the machine's hostname is something else, so the ssh TARGET is what decides.
func TestLocalBenchIsResolvedFromTheRegistryNotTheName(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "machines.tsv")
	body := "# name\tssh\tos/arch\troles\tseat\tcores\tnotes\n" +
		"space\tnova@space-01.tail1234.ts.net\tlinux/x64\tbench\tswarm-space\t16\t-\n" +
		"vision\tvision\tlinux/x64\tbench\tswarm-vision\t64\t-\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := pulse.LocalBenches(path, "space-01"); !got["space"] || got["vision"] {
		t.Errorf("LocalBenches on space-01 = %v, want space alone (the alias resolves to it)", got)
	}
	if got := pulse.LocalBenches(path, "space-02"); len(got) != 0 {
		t.Errorf("LocalBenches on a machine in no row = %v, want nothing local", got)
	}
	if got := pulse.LocalBenches(path, "vision.local"); !got["vision"] {
		t.Errorf("LocalBenches on vision.local = %v, want vision: a domain is not a different machine", got)
	}
}
