package pulse

// `fleet registry` is the reading verb over the machines file: one MACHINE line per
// machine, no ssh, no machine touched.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// exampleMachines writes a registry the size of the real fleet's interesting part: a
// shared bench, a plain bench, and a CI-only runner host.
func exampleMachines(t *testing.T) string {
	t.Helper()
	body := strings.Join([]string{
		"# name\tssh\tos/arch\troles\tseat\tcores\tnotes",
		"hulk\thulk\tlinux/x64\tbench,runner\tswarm-hulk\t64\tallow-shared=2026-09-18 CI runners beside the cards",
		"space\tspace\tlinux/x64\tbench,services\tswarm-space\t32\tthe stack lives here",
		"batman\tbatman\tdarwin/amd64\trunner\t-\t8\t2019 iMac Pro; CI-only",
		"",
	}, "\n")
	path := filepath.Join(t.TempDir(), "machines.tsv")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFleetRegistryPrintsOneLinePerMachineInFileOrder(t *testing.T) {
	var out, errb bytes.Buffer
	code := FleetRegistry(FleetRegistryInput{Machines: exampleMachines(t), Stdout: &out, Stderr: &errb})
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, errb.String())
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 3 {
		t.Fatalf("printed %d lines, want 3: %q", len(lines), out.String())
	}
	// The provider column stands on the line whether the file wrote it or not: this registry
	// is written in the seven-column form, and `tailnet` is what a seven-column line means
	// (SPEC-FLEET-NET.md R1).
	want := "MACHINE hulk ssh=hulk os=linux/x64 roles=bench,runner seat=swarm-hulk cores=64 provider=tailnet notes="
	if !strings.HasPrefix(lines[0], want) {
		t.Errorf("first line = %q, want it to start %q", lines[0], want)
	}
	if !strings.Contains(lines[0], "allow-shared=2026-09-18") {
		t.Errorf("the shared machine's line does not carry its dated exception: %q", lines[0])
	}
	if !strings.HasPrefix(lines[2], "MACHINE batman ") {
		t.Errorf("third line = %q, want batman", lines[2])
	}
}

func TestFleetRegistryFiltersByRole(t *testing.T) {
	var out, errb bytes.Buffer
	code := FleetRegistry(FleetRegistryInput{Machines: exampleMachines(t), Role: "bench", Stdout: &out, Stderr: &errb})
	if code != 0 {
		t.Fatalf("exit = %d, want 0; stderr=%q", code, errb.String())
	}
	if strings.Contains(out.String(), "batman") {
		t.Errorf("--role bench listed a CI-only runner host: %q", out.String())
	}
	for _, name := range []string{"hulk", "space"} {
		if !strings.Contains(out.String(), "MACHINE "+name+" ") {
			t.Errorf("--role bench did not list %s: %q", name, out.String())
		}
	}
}

func TestFleetRegistryRefusesARoleNobodyCarries(t *testing.T) {
	var out, errb bytes.Buffer
	code := FleetRegistry(FleetRegistryInput{Machines: exampleMachines(t), Role: "runnner", Stdout: &out, Stderr: &errb})
	if code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "runnner") || !strings.Contains(errb.String(), "the roles are") {
		t.Fatalf("stderr = %q, want the typo named and the roles listed", errb.String())
	}
}

func TestFleetRegistryRefusesAMissingFlagAndAnUnreadableFile(t *testing.T) {
	var out, errb bytes.Buffer
	if code := FleetRegistry(FleetRegistryInput{Stdout: &out, Stderr: &errb}); code != 2 {
		t.Fatalf("exit with no --machines = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "missing --machines") {
		t.Fatalf("stderr = %q", errb.String())
	}
	errb.Reset()
	code := FleetRegistry(FleetRegistryInput{
		Machines: filepath.Join(t.TempDir(), "nowhere.tsv"), Stdout: &out, Stderr: &errb})
	if code != 2 {
		t.Fatalf("exit on a missing file = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "nowhere.tsv") {
		t.Fatalf("stderr = %q, want the file named", errb.String())
	}
}
