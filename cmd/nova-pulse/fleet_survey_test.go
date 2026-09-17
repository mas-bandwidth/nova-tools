package main

// fleet survey (issue #880 item 13): `nova-pulse fleet survey --benches <file>` runs
// tools/bench-standard.sh on every bench over ssh and folds the answers into one FLEET
// <name> line per bench. The tests drive a fake ssh on PATH that runs `bash -s` locally
// under a fake HOME keyed by the ssh target, so no test touches the network.

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const fleetTestWant = "v9.9.9-fleet-test"

// fleetTestTools is the shared tools bin: the fake go, sbcl and nova-secrets the bench
// script probes, plus the fake ssh itself.
func fleetTestTools(t *testing.T, goVer string) string {
	t.Helper()
	bin := t.TempDir()
	writeBenchExe(t, filepath.Join(bin, "go"), "#!/bin/sh\necho 'go version "+goVer+" linux/amd64'\n")
	writeBenchExe(t, filepath.Join(bin, "sbcl"), "#!/bin/sh\nexit 0\n")
	writeBenchExe(t, filepath.Join(bin, "nova-secrets"), "#!/bin/sh\nexit 0\n")
	return bin
}

// fleetTestHome builds one fake bench home: the 16 nova bins at --want, the harness and
// exactly one seat key. driftBin, when non-empty, is the one bin that reports another
// version, so the bench drifts.
func fleetTestHome(t *testing.T, want, driftBin string) string {
	t.Helper()
	home := t.TempDir()
	localBin := filepath.Join(home, ".local", "bin")
	for _, name := range benchStandardBins {
		ver := want
		if name == driftBin {
			ver = "v0.0.0-other"
		}
		writeBenchExe(t, filepath.Join(localBin, name), "#!/bin/sh\necho '"+ver+"'\n")
	}
	writeBenchExe(t, filepath.Join(home, "nova-bench", "harness-v1", "opencode"), "#!/bin/sh\nexit 0\n")
	seatDir := filepath.Join(home, ".config", "nova-secrets")
	if err := os.MkdirAll(seatDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seatDir, "rowan.key"), []byte("AGE-SECRET-KEY-FAKE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return home
}

// writeFakeSSH puts a fake ssh on PATH. It maps a target to a home through the file
// <homes>/<target>, runs `bash -s` under that HOME, and refuses a target with no mapping
// the way ssh refuses a host it cannot reach.
func writeFakeSSH(t *testing.T, bin, homes string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fleet is a linux bench; the fake ssh runs the remote script through bash -s")
	}
	path := filepath.Join(bin, "ssh")
	body := `#!/usr/bin/env bash
target="$1"
home="$(cat "$FAKE_SSH_HOMES/$target" 2>/dev/null || true)"
if [ -z "$home" ] || [ ! -d "$home" ]; then
  echo "ssh: connect to host $target port 22: Connection refused" >&2
  exit 255
fi
HOME="$home" exec bash -s
`
	writeBenchExe(t, path, body)
	return path
}

// fleetFixture writes a benches file and wires the fake ssh to the homes. It returns the
// benches path and the ssh path.
func fleetFixture(t *testing.T, want, goVer string, benches []struct {
	name, target string
	driftBin     string
}) (benchesPath, sshPath string) {
	t.Helper()
	bin := fleetTestTools(t, goVer)
	homes := t.TempDir()
	if err := os.MkdirAll(homes, 0o755); err != nil {
		t.Fatal(err)
	}
	sshPath = writeFakeSSH(t, bin, homes)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_SSH_HOMES", homes)
	t.Setenv("NOVA_WANT", want)
	t.Setenv("NOVA_GO", goVer)

	var b strings.Builder
	for _, bench := range benches {
		home := fleetTestHome(t, want, bench.driftBin)
		if err := os.WriteFile(filepath.Join(homes, bench.target), []byte(home), 0o644); err != nil {
			t.Fatal(err)
		}
		b.WriteString(bench.name + "\t" + bench.target + "\t" + home + "\tmock\n")
	}
	dir := t.TempDir()
	benchesPath = filepath.Join(dir, "benches.tsv")
	if err := os.WriteFile(benchesPath, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	return benchesPath, sshPath
}

func TestFleetSurveyTwoOKBenchesExitZero(t *testing.T) {
	benches, ssh := fleetFixture(t, fleetTestWant, "go1.26.5", []struct {
		name, target string
		driftBin     string
	}{
		{"alpha", "fake-alpha", ""},
		{"beta", "fake-beta", ""},
	})
	var out, errb bytes.Buffer
	code := run([]string{"fleet", "survey", "--benches", benches, "--ssh", ssh}, &out, &errb, time.Now().UTC())
	if code != 0 {
		t.Fatalf("fleet survey exit = %d, want 0; stderr=%q out=%q", code, errb.String(), out.String())
	}
	for _, name := range []string{"alpha", "beta"} {
		if !strings.Contains(out.String(), "FLEET "+name+" STANDARD OK") {
			t.Errorf("output missing FLEET %s STANDARD OK:\n%s", name, out.String())
		}
	}
}

func TestFleetSurveyDriftBenchNamesItExitTwo(t *testing.T) {
	benches, ssh := fleetFixture(t, fleetTestWant, "go1.26.5", []struct {
		name, target string
		driftBin     string
	}{
		{"alpha", "fake-alpha", ""},
		{"beta", "fake-beta", "nova-bus"},
	})
	var out, errb bytes.Buffer
	code := run([]string{"fleet", "survey", "--benches", benches, "--ssh", ssh}, &out, &errb, time.Now().UTC())
	if code != 2 {
		t.Fatalf("fleet survey drift exit = %d, want 2; stderr=%q out=%q", code, errb.String(), out.String())
	}
	if !strings.Contains(out.String(), "FLEET beta DRIFT nova-bus") {
		t.Fatalf("output missing FLEET beta DRIFT naming nova-bus:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "FLEET beta STANDARD DRIFT") {
		t.Fatalf("output missing FLEET beta STANDARD DRIFT:\n%s", out.String())
	}
}

func TestFleetSurveyUnreachableBenchNamesItExitThree(t *testing.T) {
	benches, ssh := fleetFixture(t, fleetTestWant, "go1.26.5", []struct {
		name, target string
		driftBin     string
	}{
		{"alpha", "fake-alpha", ""},
		{"gamma", "fake-gamma", ""}, // no mapping file: the fake ssh refuses it
	})
	// Drop gamma's mapping so the fake ssh cannot reach its home.
	homes := os.Getenv("FAKE_SSH_HOMES")
	if err := os.Remove(filepath.Join(homes, "fake-gamma")); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := run([]string{"fleet", "survey", "--benches", benches, "--ssh", ssh}, &out, &errb, time.Now().UTC())
	if code != 3 {
		t.Fatalf("fleet survey unreachable exit = %d, want 3; stderr=%q out=%q", code, errb.String(), out.String())
	}
	if !strings.Contains(out.String(), "FLEET gamma UNREACHABLE") {
		t.Fatalf("output missing FLEET gamma UNREACHABLE:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Connection refused") {
		t.Fatalf("output does not carry the ssh error:\n%s", out.String())
	}
}

func TestFleetSurveyMaxCapsLines(t *testing.T) {
	benches, ssh := fleetFixture(t, fleetTestWant, "go1.26.5", []struct {
		name, target string
		driftBin     string
	}{
		{"alpha", "fake-alpha", "nova-bus"},
		{"beta", "fake-beta", "nova-bus"},
	})
	var out, errb bytes.Buffer
	run([]string{"fleet", "survey", "--benches", benches, "--ssh", ssh, "--max", "1"}, &out, &errb, time.Now().UTC())
	lines := strings.Split(strings.TrimRight(out.String(), "\n"), "\n")
	if len(lines) != 1 || !strings.HasPrefix(lines[0], "FLEET ") {
		t.Fatalf("--max 1 printed %d lines, want 1 FLEET line:\n%s", len(lines), out.String())
	}
}
