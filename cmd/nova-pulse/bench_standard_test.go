package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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
	// Fake nova-sandbox answers `version` like the other bins, but for the
	// network probe it runs the command after `--` so the fake curl on PATH
	// answers with the http code the probe reads.
	writeBenchExe(t, filepath.Join(localBin, "nova-sandbox"), "#!/bin/sh\n"+
		"if [ \"${1:-}\" = version ] || [ \"${1:-}\" = --version ]; then echo '"+want+"'; exit 0; fi\n"+
		"while [ $# -gt 0 ]; do\n"+
		"  if [ \"$1\" = -- ]; then shift; exec \"$@\"; fi\n"+
		"  shift\n"+
		"done\n"+
		"exit 0\n")
	// Fake curl: the probe reads only the http code it prints.
	writeBenchExe(t, filepath.Join(bin, "curl"), "#!/bin/sh\necho 200\n")
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
	if runtime.GOOS == "windows" {
		t.Skip("bench-standard.sh is a bash script for linux benches; skipping on windows")
	}
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
		"NOVA_PROBE_URL=https://probe.invalid/api.json",
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

// THE SANDBOX NETWORK PROBE IS LINUX'S (bench-standard.sh check 3b, guarded by
// `[ "$OS" = "Linux" ]`): nova-sandbox is a linux sandbox, so on any other bench the
// probe is not attempted and there is nothing for it to drift about. The test mirrors
// that guard instead of asserting linux behaviour everywhere -- it asserted the drift
// on every platform, and the darwin runners are where CI found that out.
func TestBenchStandardDriftNamesSandboxNetwork(t *testing.T) {
	want := "v9.9.9-bench-test"
	goVer := "go1.26.5"
	home, bin := benchStandardHome(t, want, goVer)
	// The sandbox answers, but curl inside it cannot reach the network.
	writeBenchExe(t, filepath.Join(bin, "curl"), "#!/bin/sh\necho 000\n")
	out, code := runBenchStandard(t, home, bin, want, goVer)
	if runtime.GOOS != "linux" {
		if code != 0 {
			t.Fatalf("bench-standard exit = %d on %s, want 0: the probe is linux's\n%s", code, runtime.GOOS, out)
		}
		if strings.Contains(out, "sandbox-network") {
			t.Fatalf("the sandbox network probe ran on %s, where it is skipped:\n%s", runtime.GOOS, out)
		}
		return
	}
	if code == 0 {
		t.Fatalf("bench-standard drift exit = 0, want non-zero\n%s", out)
	}
	if !strings.Contains(out, "DRIFT sandbox-network") {
		t.Fatalf("bench-standard output must name the sandbox network probe:\n%s", out)
	}
	if !strings.Contains(out, "http=000") {
		t.Fatalf("bench-standard output must report http=000:\n%s", out)
	}
	if !strings.Contains(out, "STANDARD DRIFT") {
		t.Fatalf("bench-standard drift output missing STANDARD DRIFT line:\n%s", out)
	}
}
