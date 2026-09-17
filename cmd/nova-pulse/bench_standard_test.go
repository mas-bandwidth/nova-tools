package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// bench-standard (issue #880 item 15): tools/bench-standard.sh is the one admin
// entry for a Linux bench. The test runs it with HOME pointed at a temp dir
// holding a fake ~/.local/bin/nova-* set of 16 stubs, a fake go, a fake
// harness and a fake seat key, and asserts STANDARD OK; a second run with one
// stub printing another version asserts a DRIFT line naming that binary.

var benchStandardBins = []string{
	"nova-board", "nova-bus", "nova-check", "nova-fuse",
	"nova-memory", "nova-merge", "nova-pulse", "nova-review",
	"nova-sandbox", "nova-secrets", "nova-self-talk", "nova-swarm",
	"nova-tokens", "nova-update", "nova-version", "nova-wake",
}

func writeBenchExe(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func benchStandardHome(t *testing.T, want, goVer string) (home, bin string) {
	t.Helper()
	home = t.TempDir()
	bin = t.TempDir()
	localBin := filepath.Join(home, ".local", "bin")
	for _, name := range benchStandardBins {
		writeBenchExe(t, filepath.Join(localBin, name), "#!/bin/sh\necho '"+want+"'\n")
	}
	// Fake go printing the wanted version.
	writeBenchExe(t, filepath.Join(bin, "go"), "#!/bin/sh\necho 'go version "+goVer+" linux/amd64'\n")
	// Fake sbcl: presence on PATH is the check.
	writeBenchExe(t, filepath.Join(bin, "sbcl"), "#!/bin/sh\nexit 0\n")
	// Fake nova-secrets whose check passes for any key.
	writeBenchExe(t, filepath.Join(bin, "nova-secrets"), "#!/bin/sh\nexit 0\n")
	// Fake harness at $HOME/nova-bench/harness-<ver>/opencode.
	writeBenchExe(t, filepath.Join(home, "nova-bench", "harness-v1", "opencode"), "#!/bin/sh\nexit 0\n")
	// Fake seat: exactly one *.key.
	seatDir := filepath.Join(home, ".config", "nova-secrets")
	if err := os.MkdirAll(seatDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seatDir, "rowan.key"), []byte("AGE-SECRET-KEY-FAKE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return home, bin
}

func runBenchStandard(t *testing.T, home, bin, want, goVer string) (string, int) {
	t.Helper()
	script := filepath.Join("..", "..", "tools", "bench-standard.sh")
	cmd := exec.Command("bash", script)
	// Minimal PATH: the fakes first, then the system dirs. The test never
	// touches the real go, sbcl or nova-secrets, and a long inherited PATH
	// (IDE shims, SDKs) only slows every command -v lookup in the script.
	cmd.Env = append([]string{},
		"PATH="+bin+string(os.PathListSeparator)+"/usr/bin"+string(os.PathListSeparator)+"/bin"+string(os.PathListSeparator)+"/usr/sbin"+string(os.PathListSeparator)+"/sbin",
		"HOME="+home,
		"NOVA_WANT="+want,
		"NOVA_GO="+goVer,
	)
	raw, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("running bench-standard.sh: %v\n%s", err, raw)
		}
	}
	return string(raw), code
}

func TestBenchStandardOK(t *testing.T) {
	want := "v9.9.9-bench-test"
	goVer := "go1.26.5"
	home, bin := benchStandardHome(t, want, goVer)
	out, code := runBenchStandard(t, home, bin, want, goVer)
	if code != 0 {
		t.Fatalf("bench-standard exit = %d, want 0\n%s", code, out)
	}
	if !strings.Contains(out, "STANDARD OK") {
		t.Fatalf("bench-standard output missing STANDARD OK:\n%s", out)
	}
	if strings.Contains(out, "DRIFT") {
		t.Fatalf("bench-standard OK run printed DRIFT:\n%s", out)
	}
}

func TestBenchStandardDriftNamesBinary(t *testing.T) {
	want := "v9.9.9-bench-test"
	goVer := "go1.26.5"
	home, bin := benchStandardHome(t, want, goVer)
	// One stub drifts to another version.
	writeBenchExe(t, filepath.Join(home, ".local", "bin", "nova-bus"), "#!/bin/sh\necho 'v0.0.0-other'\n")
	out, code := runBenchStandard(t, home, bin, want, goVer)
	if code == 0 {
		t.Fatalf("bench-standard drift exit = 0, want non-zero\n%s", out)
	}
	if !strings.Contains(out, "DRIFT") || !strings.Contains(out, "nova-bus") {
		t.Fatalf("bench-standard drift output must have a DRIFT line naming nova-bus:\n%s", out)
	}
	if !strings.Contains(out, "STANDARD DRIFT") {
		t.Fatalf("bench-standard drift output missing STANDARD DRIFT line:\n%s", out)
	}
}
