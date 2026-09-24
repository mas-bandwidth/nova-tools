package secrets

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// "one seat per OS user, keys for swarms not people" -- docs/SPEC-SECRETS.md,
// Additions from dogfooding (2026-09-16/17), item 2: an AI is a unix user with
// one file and one key, and a pool of workers shares a swarm key, because a
// person's credential in a shared seat cannot be told from a worker's.
//
// The bench standard holds the rule. tools/bench-standard.sh, check #5,
// counts `$HOME/.config/nova-secrets/*.key` and demands the count be exactly
// one, because two keys on one user share the key in fact -- the rule says
// "wouldn't", not "can't" (SPEC-SECRETS.md, line 1119) -- and the survey is
// the one thing that says so.
//
// The narrow assertion this test pins: a HOME carrying exactly one seat key
// is accepted by the bench standard (no `DRIFT seat keys=...` line, and the
// `STANDARD OK` line carries `seats=1`); a HOME carrying two keys is refused
// by name on the `seat keys=2 want=1` line. Two fixtures, one rule, one
// stdout pipe.

const oneSeatBenchBins = "v9.9.9-bench-test"

// writeSeatBenchExe lays down one shell stub at path with the given body, mode 0755.
func writeSeatBenchExe(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

// oneSeatBenchHome builds the closed world the bench standard runs in:
// the 16 nova bins print the wanted version, a fake go prints the wanted
// toolchain, a fake sbcl is on PATH, the toolchain roots exist under HOME,
// a fake harness sits under $HOME/nova-bench/harness-<ver>/opencode, and a
// fake nova-secrets whose `check` returns 0. The seat directory holds the
// caller-named key files (one or two).
//
// NOVA_WANT is fixed; NOVA_GO comes from the caller's go.mod line so the
// toolchain check is green too, and the seat-keys check is the only thing
// this test reads. The probe URL is pointed at .invalid so the network
// half is skipped on benches where it runs.
func oneSeatBenchHome(t *testing.T, seatFiles ...string) (home, bin string) {
	t.Helper()
	want := oneSeatBenchBins
	goVer := "go1.26.5"
	home = t.TempDir()
	bin = t.TempDir()
	localBin := filepath.Join(home, ".local", "bin")
	for _, name := range []string{
		"nova-board", "nova-bus", "nova-check", "nova-fuse",
		"nova-memory", "nova-merge", "nova-pulse", "nova-review",
		"nova-sandbox", "nova-secrets", "nova-self-talk", "nova-swarm",
		"nova-tokens", "nova-update", "nova-version", "nova-wake",
	} {
		writeSeatBenchExe(t, filepath.Join(localBin, name), "#!/bin/sh\necho '"+want+"'\n")
	}
	// Fake nova-sandbox: the network probe runs the command after `--` so the
	// fake curl on PATH answers with http=200.
	writeSeatBenchExe(t, filepath.Join(localBin, "nova-sandbox"), "#!/bin/sh\n"+
		"if [ \"${1:-}\" = version ] || [ \"${1:-}\" = --version ]; then echo '"+want+"'; exit 0; fi\n"+
		"while [ $# -gt 0 ]; do\n"+
		"  if [ \"$1\" = -- ]; then shift; exec \"$@\"; fi\n"+
		"  shift\n"+
		"done\n"+
		"exit 0\n")
	writeSeatBenchExe(t, filepath.Join(bin, "curl"), "#!/bin/sh\necho 200\n")
	// Fake go and sbcl under $HOME/sdk/<tool>-<ver>/bin, the home root the wall grants
	// EXECUTE, with symlinked PATH entries: check (3c) drifts on a tool whose real path
	// is outside a granted root, and this test is about the seat count, not that rule.
	goReal := filepath.Join(home, "sdk", "go-"+goVer, "bin", "go")
	writeSeatBenchExe(t, goReal, "#!/bin/sh\necho 'go version "+goVer+" linux/amd64'\n")
	// sbcl answers the pin (check 3d) and sqlite3 resolves under ~/sdk (check 3f),
	// so the only thing this fixture can drift on is the seat (nova-tools#2053).
	sbclReal := filepath.Join(home, "sdk", "sbcl-2.5.8", "bin", "sbcl")
	writeSeatBenchExe(t, sbclReal, "#!/bin/sh\necho 'SBCL 2.5.8'\n")
	sqliteReal := filepath.Join(home, "sdk", "sqlite3-3.46.0", "bin", "sqlite3")
	writeSeatBenchExe(t, sqliteReal, "#!/bin/sh\necho '3.46.0'\n")
	for name, real := range map[string]string{"go": goReal, "sbcl": sbclReal, "sqlite3": sqliteReal} {
		if err := os.Symlink(real, filepath.Join(bin, name)); err != nil {
			t.Fatal(err)
		}
	}
	// Fake nova-secrets whose check passes for any key: this test is about
	// the seat-count rule, not about whether check could fail.
	writeSeatBenchExe(t, filepath.Join(bin, "nova-secrets"), "#!/bin/sh\nexit 0\n")
	// Toolchain roots come from the ONE list (internal/swarm/toolchain.go),
	// not spelled again here; see the class test in internal/ci.
	for _, name := range swarm.ToolchainRootNames("linux") {
		if err := os.MkdirAll(filepath.Join(home, filepath.FromSlash(name)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeSeatBenchExe(t, filepath.Join(home, "nova-bench", "harness-v1", "opencode"), "#!/bin/sh\nexit 0\n")
	// The pro rung (check 3e).
	if err := os.MkdirAll(filepath.Join(home, "nova-bench", "rungs", "pro"), 0o755); err != nil {
		t.Fatal(err)
	}
	// The seat keys themselves, exactly the names the caller chose.
	seatDir := filepath.Join(home, ".config", "nova-secrets")
	if err := os.MkdirAll(seatDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, name := range seatFiles {
		if err := os.WriteFile(filepath.Join(seatDir, name), []byte("AGE-SECRET-KEY-FAKE\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return home, bin
}

// runOneSeatBenchStandard runs tools/bench-standard.sh under a controlled HOME
// and PATH and returns its combined output. NOVA_GO is supplied so the
// toolchain check is green; the test only reads the seat-keys line.
func runOneSeatBenchStandard(t *testing.T, home, bin string) (string, int) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("tools/bench-standard.sh is a bash script for a Linux bench; the rule is read through it on Linux")
	}
	script := filepath.Join("..", "..", "tools", "bench-standard.sh")
	cmd := exec.Command("bash", script)
	cmd.Env = append([]string{},
		"PATH="+bin+string(os.PathListSeparator)+"/usr/bin"+string(os.PathListSeparator)+"/bin"+string(os.PathListSeparator)+"/usr/sbin"+string(os.PathListSeparator)+"/sbin",
		"HOME="+home,
		"NOVA_WANT="+oneSeatBenchBins,
		"NOVA_GO=go1.26.5",
		"NOVA_PROBE_URL=https://probe.invalid/api.json",
		// The bench's declared slot share (check 3g).
		"NOVA_SLOT_SHARE=64",
	)
	raw, err := cmd.CombinedOutput()
	code := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			code = ee.ExitCode()
		} else {
			t.Fatalf("running tools/bench-standard.sh: %v\n%s", err, raw)
		}
	}
	return string(raw), code
}

// hasDRIFTSearch returns the first line of out starting with prefix, or "".
func hasDRIFTSearch(out, prefix string) string {
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, prefix) {
			return l
		}
	}
	return ""
}

// TestOneSeatPerOSUser pins the spec sentence "an AI is a unix user with one
// file and one key" through the bench standard's check #5: exactly one
// `*.key` under $HOME/.config/nova-secrets is what makes a bench conforming
// on the seat-keys line, and two keys on one OS user is the drift the spec
// says "the survey reports" (line 1119). The two halves of one rule, in
// one test, on one stdout.
func TestOneSeatPerOSUser(t *testing.T) {
	t.Parallel()

	// One key on one OS user: no DRIFT on the seat-keys line, STANDARD OK
	// carries `seats=1`. The fixture is otherwise empty of all the things
	// bench-standard.sh checks (a stub for every one), so any green here is
	// the seat count alone.
	home, bin := oneSeatBenchHome(t, "rowan.key")
	out, code := runOneSeatBenchStandard(t, home, bin)
	if code != 0 {
		t.Fatalf("bench-standard.sh with one seat key exited %d, want 0:\n%s", code, out)
	}
	if !strings.Contains(out, "STANDARD OK") {
		t.Fatalf("bench-standard.sh output missing STANDARD OK:\n%s", out)
	}
	if !strings.Contains(out, "seats=1") {
		t.Errorf("bench-standard.sh STANDARD OK line does not carry `seats=1`; the seat-keys check is not the one assertion being made:\n%s", out)
	}
	if drift := hasDRIFTSearch(out, "DRIFT seat keys="); drift != "" {
		t.Errorf("bench-standard.sh drifted on the seat-keys line with exactly one key:\n%s", drift)
	}

	// Two keys on one OS user: the drift is named, and the named line is
	// the one the spec demanded ("exactly one seat key per owner prefix",
	// rule 6 of the dogfooding additions). This is the control that proves
	// the green above is not the script saying yes to anything; it is the
	// same script saying NO when the count is two.
	home2, bin2 := oneSeatBenchHome(t, "rowan.key", "air.key")
	out2, code2 := runOneSeatBenchStandard(t, home2, bin2)
	if code2 == 0 {
		t.Fatalf("bench-standard.sh with two seat keys exited 0, want non-zero; the seat-count rule did not refuse:\n%s", out2)
	}
	if !strings.Contains(out2, "STANDARD DRIFT") {
		t.Fatalf("bench-standard.sh drift output missing STANDARD DRIFT line:\n%s", out2)
	}
	drift := hasDRIFTSearch(out2, "DRIFT seat keys=")
	if drift == "" {
		t.Fatalf("bench-standard.sh did not drift on the seat-keys line; the rule the spec demands is absent:\n%s", out2)
	}
	for _, want := range []string{"seat keys=2", "want=1"} {
		if !strings.Contains(drift, want) {
			t.Errorf("the seat-keys DRIFT line does not carry %q; the count and the rule should both be on the line:\n%s", want, drift)
		}
	}
	if !strings.Contains(drift, ".config/nova-secrets") {
		t.Errorf("the seat-keys DRIFT line does not name the seat directory; a refusal without the path leaves the reader to find it:\n%s", drift)
	}
}
