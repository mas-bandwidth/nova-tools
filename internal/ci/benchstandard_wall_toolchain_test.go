package ci

// The bench standard used to ask only whether `go` and `sbcl` were ON PATH. A card does not
// run in the bench user's shell: it runs behind the sandbox wall, whose linux read roots are
// the system table of internal/sandbox/wrap_linux.go plus the toolchain roots of
// internal/swarm/toolchain.go ($HOME/sdk with EXECUTE, $HOME/go/pkg/mod without). An
// interpreter at $HOME/.local/bin/sbcl satisfies `command -v` and is `Permission denied`
// inside the wall, so the bench passes the standard and every card on it dies -- which is
// exactly what happened: every lisp card was forced onto the one bench whose sbcl is
// /usr/bin/sbcl, E09-G1 taking 1036 s on vision against 248-393 s for the same class on
// space, and the r1785 worker on mini fetching an SBCL 2.4.0 of its own into $TMPDIR before
// it could run a test. The wall was right and the standard was silent.
//
// This is the #1706 shape, the one benchstandard_disk_test.go already uses: run the REAL
// tools/bench-standard.sh with a FAKE PATH layout and a HOME of its own. The layout is the
// whole method -- the test cannot move a real bench's sbcl, and a test that only passes
// where the developer's own sbcl happens to live is not a test.

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// benchStandardWithTool runs the standard with `tool` installed at `at` (relative to the
// fake HOME) and first on PATH, and returns the script's combined output. Everything else
// about the bench is missing, so the script prints other DRIFT lines too and exits 1; only
// the wall-toolchain lines are read here.
func benchStandardWithTool(t *testing.T, tool, at string) (string, string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the bench standard is a bash script for a Linux bench")
	}
	home := t.TempDir()
	full := filepath.Join(home, at)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	// A stub that answers --version, because check (3) reads `go version` before (3b) runs.
	if err := os.WriteFile(full, []byte("#!/bin/sh\necho 'stub "+tool+" 0.0'\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Dir(full)
	cmd := exec.Command("bash", filepath.Join(repoRoot(t), "tools", "bench-standard.sh"))
	cmd.Env = append(os.Environ(),
		"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"HOME="+home,
	)
	out, _ := cmd.CombinedOutput() // a bench missing everything exits 1; the lines are the answer
	return string(out), full
}

// driftLinesFor returns every DRIFT line that names the tool's wall problem.
func driftLinesFor(out, tool string) []string {
	var lines []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, "DRIFT "+tool+" on PATH is ") {
			lines = append(lines, l)
		}
	}
	return lines
}

// TestBenchStandardDriftsOnAToolTheWallCannotExecute is the negative direction: a tool that
// resolves under NO read root the wall grants must be one DRIFT line, and that line must
// NAME THE REMEDY, because "your sbcl is in the wrong place" is a sentence somebody then has
// to go and work out the right place for at whatever hour it is.
func TestBenchStandardDriftsOnAToolTheWallCannotExecute(t *testing.T) {
	t.Parallel()

	// $HOME/.local/bin is exactly where the fleet's sbcl was on hulk, vision, mini and
	// captainamerica on 2026-09-19, and exactly where the wall denied it.
	out, path := benchStandardWithTool(t, "sbcl", ".local/bin/sbcl")
	lines := driftLinesFor(out, "sbcl")
	if len(lines) == 0 {
		t.Fatalf("an sbcl at %s is on PATH and `Permission denied` inside the wall, and the standard said nothing:\n%s", path, out)
	}
	if len(lines) != 1 {
		t.Errorf("the wall-toolchain finding is not one line for sbcl:\n%s", strings.Join(lines, "\n"))
	}
	line := lines[0]
	// The path as PATH gave it, the path it really resolves to, the granted home, and the
	// remedy. A DRIFT line that names the fault without naming the fix is half a finding.
	for _, want := range []string{path, "$HOME/sdk", "sdk/sbcl-", "EXECUTE"} {
		if !strings.Contains(line, want) {
			t.Errorf("the wall-toolchain DRIFT line does not carry %q:\n%s", want, line)
		}
	}

	// go is held to the same rule; the check is a loop over both and must not have been
	// written for sbcl alone.
	outGo, pathGo := benchStandardWithTool(t, "go", ".local/bin/go")
	if got := driftLinesFor(outGo, "go"); len(got) != 1 {
		t.Errorf("a go at %s drew %d wall-toolchain DRIFT lines, want 1:\n%s", pathGo, len(got), outGo)
	}
}

// TestBenchStandardAcceptsAToolUnderAGrantedRoot is the positive direction, and it is the
// half that catches a check written as `always drift`. $HOME/sdk is the toolchain root
// internal/swarm/toolchain.go grants with EXECUTE, so a tool under it is the CONFORMING
// layout and must draw no line at all.
func TestBenchStandardAcceptsAToolUnderAGrantedRoot(t *testing.T) {
	t.Parallel()

	out, path := benchStandardWithTool(t, "sbcl", "sdk/sbcl-2.5.8/bin/sbcl")
	if lines := driftLinesFor(out, "sbcl"); len(lines) != 0 {
		t.Errorf("an sbcl at %s is under the granted $HOME/sdk and still drifted:\n%s", path, strings.Join(lines, "\n"))
	}
	outGo, pathGo := benchStandardWithTool(t, "go", "sdk/go1.26.6/bin/go")
	if lines := driftLinesFor(outGo, "go"); len(lines) != 0 {
		t.Errorf("a go at %s is under the granted $HOME/sdk and still drifted:\n%s", pathGo, strings.Join(lines, "\n"))
	}
}
