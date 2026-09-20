package ci

// #1282: scripts/bench-hygiene.sh builds LOG, ROOT1 and ROOT2 from $HOME at
// lines 31-32 with no check on $HOME at all. Every path the script may remove
// is the join of one of two literal roots under $HOME, so a HOME that is empty,
// is not absolute, or has fewer than two components must be refused before a
// single path is built. The coordinator's hostile-input run measured 36 of 40
// cases passing and exactly the four HOME cases failing.
//
// This is the Go test for that guard, written the way
// benchstandard_disk_test.go drives tools/bench-standard.sh: bash, a HOME of its
// own, and only the line it is about is read. The verb driven is `log`, which
// tails <home>/hygiene.log and deletes nothing from the real bench.
//
// TestBenchHygieneRefusesAHostileHome feeds the four values #1282 names -- "",
// "/", "relative/home" and "/onlyone" -- and requires exit 2 with the
// coordinator's own refusal line. The control is not optional: a good
// multi-component HOME must NOT print the refusal, or the guard would refuse
// every bench and break the reaper.

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// benchHygieneRefusal is the coordinator's line, verbatim: the wording #1282
// asks this repository's copy to carry.
const benchHygieneRefusal = "REFUSE: HOME is not an absolute path with at least two components"

// benchHygieneLog runs `bench-hygiene.sh log` with HOME set to home and returns
// its exit code and combined output. `log` only tails <home>/hygiene.log, so it
// deletes nothing; no deleting verb is driven here.
func benchHygieneLog(t *testing.T, home string) (int, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("bench-hygiene.sh is a bash script for a Linux bench")
	}
	cmd := exec.Command("bash", filepath.Join(repoRoot(t), "scripts", "bench-hygiene.sh"), "log")
	cmd.Env = append(os.Environ(), "HOME="+home)
	out, _ := cmd.CombinedOutput()
	code := 0
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	return code, string(out)
}

func TestBenchHygieneRefusesAHostileHome(t *testing.T) {
	t.Parallel()
	hostile := []struct {
		name string
		home string
	}{
		{"empty", ""},
		{"whole-disk", "/"},
		{"relative", "relative/home"},
		{"one-component", "/onlyone"},
	}
	for _, tc := range hostile {
		t.Run(tc.name, func(t *testing.T) {
			code, out := benchHygieneLog(t, tc.home)
			if code != 2 {
				t.Errorf("HOME=%q exited %d; the script must refuse with exit 2 before it builds a path; output:\n%s", tc.home, code, out)
			}
			if !strings.Contains(out, benchHygieneRefusal) {
				t.Errorf("HOME=%q exited %d without the refusal %q; output:\n%s", tc.home, code, benchHygieneRefusal, out)
			}
		})
	}

	// THE CONTROL: a good multi-component home is not refused. `log` finds no
	// log and may exit non-zero for its own reasons; the refusal must be absent.
	if code, out := benchHygieneLog(t, t.TempDir()); strings.Contains(out, benchHygieneRefusal) {
		t.Errorf("a good home was refused (exit %d); the guard must not refuse every bench:\n%s", code, out)
	}
}
