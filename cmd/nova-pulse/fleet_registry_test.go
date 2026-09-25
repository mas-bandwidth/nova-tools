package main

// `nova-pulse fleet registry` at the command line, and the one flag the lock adds to the
// verbs that reach a machine (Glenn 2026-09-18: runner hosts are CI-only).

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// registryFile writes a small machines registry: one bench and one CI-only runner host.
func registryFile(t *testing.T, dir string) string {
	t.Helper()
	body := strings.Join([]string{
		"# name\tssh\tos/arch\troles\tseat\tcores\tnotes",
		"hulk\thulk\tlinux/x64\tbench\tswarm-hulk\t64\t-",
		"batman\tbatman\tdarwin/amd64\trunner\t-\t8\t2019 iMac Pro; CI-only",
		"",
	}, "\n")
	path := filepath.Join(dir, "machines.tsv")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestFleetRegistryListsTheFleetAndFiltersByRole(t *testing.T) {
	path := registryFile(t, t.TempDir())
	var out, errb bytes.Buffer
	if code := run([]string{"fleet", "registry", "--machines", path}, &out, &errb, time.Now().UTC()); code != 0 {
		t.Fatalf("exit = %d, stderr=%q", code, errb.String())
	}
	if !strings.Contains(out.String(), "MACHINE hulk ") || !strings.Contains(out.String(), "MACHINE batman ") {
		t.Fatalf("stdout = %q, want both machines", out.String())
	}
	out.Reset()
	if code := run([]string{"fleet", "registry", "--machines", path, "--role", "bench"}, &out, &errb, time.Now().UTC()); code != 0 {
		t.Fatalf("--role bench exit = %d, stderr=%q", code, errb.String())
	}
	if strings.Contains(out.String(), "batman") {
		t.Fatalf("--role bench listed a CI-only runner host: %q", out.String())
	}
}

func TestFleetRegistryRefusesWithoutTheFlag(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"fleet", "registry"}, &out, &errb, time.Now().UTC()); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "--machines is required") {
		t.Fatalf("stderr = %q, want the missing flag named", errb.String())
	}
}

// TestFillAtTheCommandLineRefusesARunnerHost: the flag is wired, the guard bites, and the
// FILL line never appears -- a card gets nowhere near batman.
func TestFillAtTheCommandLineRefusesARunnerHost(t *testing.T) {
	dir := t.TempDir()
	ready, launched := filepath.Join(dir, "ready"), filepath.Join(dir, "launched")
	fillReady(t, ready, 1)
	var out, errb bytes.Buffer
	code := run([]string{
		"fill", "--ready", ready, "--launched", launched,
		"--machines", registryFile(t, dir), "--bench", "batman", "--once",
	}, &out, &errb, time.Now().UTC())
	if code != 2 {
		t.Fatalf("exit = %d, want 2; stdout=%q stderr=%q", code, out.String(), errb.String())
	}
	if !strings.Contains(errb.String(), "FILL REFUSED bench=batman reason=runner-host") {
		t.Fatalf("stderr = %q, want the lock's refusal", errb.String())
	}
	if strings.Contains(out.String(), "FILL tick=") {
		t.Fatalf("a refused fill still ticked: %q", out.String())
	}
	if fillCount(t, ready) != 1 {
		t.Error("the ready card was moved by a refused fill")
	}
}

// TestTheFleetSubVerbListNamesRegistry: an unknown sub-verb lists what there is, and the
// list is how a person finds the registry at all.
func TestTheFleetSubVerbListNamesRegistry(t *testing.T) {
	var out, errb bytes.Buffer
	if code := run([]string{"fleet", "nonesuch"}, &out, &errb, time.Now().UTC()); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errb.String(), "registry") {
		t.Fatalf("stderr = %q, want the registry sub-verb listed", errb.String())
	}
}
