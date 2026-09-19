package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
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
	// Fake go printing the wanted version, placed under $HOME/sdk/<goVer>/bin: check (3b)
	// resolves `go` with readlink -f and drifts unless it lies under a root the sandbox
	// wall grants, and $HOME/sdk is that root (internal/swarm/toolchain.go:152, Exec: true).
	writeBenchExe(t, filepath.Join(home, "sdk", goVer, "bin", "go"), "#!/bin/sh\necho 'go version "+goVer+" linux/amd64'\n")
	// Fake sbcl, same granted root as go.
	writeBenchExe(t, filepath.Join(home, "sdk", "sbcl-2.5.8", "bin", "sbcl"), "#!/bin/sh\nexit 0\n")
	// Fake nova-secrets whose check passes for any key.
	writeBenchExe(t, filepath.Join(bin, "nova-secrets"), "#!/bin/sh\nexit 0\n")
	// The toolchain roots the sandbox wall grants a card, taken from the ONE list rather
	// than spelled again here (internal/swarm/toolchain.go): the standard checks a bench
	// has them, because a bench missing one is a bench whose Go cards die inside the wall.
	for _, name := range swarm.ToolchainRootNames("linux") {
		if err := os.MkdirAll(filepath.Join(home, filepath.FromSlash(name)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
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
	goBin := filepath.Join(home, "sdk", goVer, "bin")
	sbclBin := filepath.Join(home, "sdk", "sbcl-2.5.8", "bin")
	cmd.Env = append([]string{},
		"PATH="+goBin+string(os.PathListSeparator)+sbclBin+string(os.PathListSeparator)+bin+string(os.PathListSeparator)+"/usr/bin"+string(os.PathListSeparator)+"/bin"+string(os.PathListSeparator)+"/usr/sbin"+string(os.PathListSeparator)+"/sbin",
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

// TestBenchStandardDriftNamesAMissingToolchainRoot: the wall grants these roots and a card's
// `go` lives under one of them, so a bench that is missing one has to DRIFT here rather than
// let a Go card discover it inside the wall as `Permission denied` and then
// `go.mod requires go >= 1.26 (running go 1.22.2)` (the schema dogfood loop, 2026-09-18).
func TestBenchStandardDriftNamesAMissingToolchainRoot(t *testing.T) {
	want := "v9.9.9-bench-test"
	goVer := "go1.26.5"
	for _, name := range swarm.ToolchainRootNames("linux") {
		t.Run(name, func(t *testing.T) {
			home, bin := benchStandardHome(t, want, goVer)
			missing := filepath.Join(home, filepath.FromSlash(name))
			if err := os.RemoveAll(missing); err != nil {
				t.Fatal(err)
			}
			out, code := runBenchStandard(t, home, bin, want, goVer)
			if code == 0 {
				t.Fatalf("bench-standard with %s missing exit = 0, want non-zero\n%s", missing, out)
			}
			if !strings.Contains(out, "DRIFT toolchain root "+missing) {
				t.Fatalf("bench-standard must name the missing toolchain root %s:\n%s", missing, out)
			}
		})
	}
}
