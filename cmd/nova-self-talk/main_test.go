package main

// CLI-level acceptance: skip semantics (--skip, default empty), exit codes,
// the output grammar, and determinism. Classification tests live in
// internal/selftalk, beside the code that does the judging.

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// Exit 0 when nothing standing; the OK line goes to stdout, stderr is empty.
func TestExitZeroWhenClean(t *testing.T) {
	f := write(t, t.TempDir(), "clean.md", "The tree by the house has one lit window.\n")
	var stdout, stderr bytes.Buffer
	if got := run([]string{f}, &stdout, &stderr); got != 0 {
		t.Errorf("want exit 0, got %d\nstderr: %s", got, stderr.String())
	}
	if !strings.Contains(stdout.String(), "SELFTALK OK files=1 claims=0 standing=0") {
		t.Errorf("stdout = %q, want the OK line", stdout.String())
	}
	if stderr.String() != "" {
		t.Errorf("clean run must leave stderr empty, got %q", stderr.String())
	}
}

// Exit 1 when a standing claim is present: FAIL on stderr, no OK line on
// stdout. The tool's predecessor always exited 0, which is an instrument
// with no failure state — the defect it exists to detect could not make it
// say NO.
func TestExitOneOnStandingClaim(t *testing.T) {
	f := write(t, t.TempDir(), "drifted.md", "I cannot check my own work.\n")
	var stdout, stderr bytes.Buffer
	if got := run([]string{f}, &stdout, &stderr); got != 1 {
		t.Errorf("want exit 1, got %d\nstdout: %s", got, stdout.String())
	}
	if !strings.Contains(stderr.String(), "SELFTALK FAIL "+f+": STANDING: I cannot check my own work.") {
		t.Errorf("stderr = %q, want a SELFTALK FAIL line naming the file, verdict, and claim", stderr.String())
	}
	if strings.Contains(stdout.String(), "SELFTALK OK") {
		t.Errorf("a failing run must not print an OK line, got %q", stdout.String())
	}
}

// A dated claim is a record: reported on stdout, exit 0, and counted in
// claims= but not in standing=.
func TestDatedClaimIsReportedAndExitsZero(t *testing.T) {
	f := write(t, t.TempDir(), "record.md", "On 2026-07-30 I cannot check my own work.\n")
	var stdout, stderr bytes.Buffer
	if got := run([]string{f}, &stdout, &stderr); got != 0 {
		t.Errorf("want exit 0 for a dated record, got %d\nstderr: %s", got, stderr.String())
	}
	if !strings.Contains(stdout.String(), "SELFTALK DATED "+f+": ") {
		t.Errorf("stdout = %q, want a SELFTALK DATED line", stdout.String())
	}
	if !strings.Contains(stdout.String(), "SELFTALK OK files=1 claims=1 standing=0") {
		t.Errorf("stdout = %q, want claims=1 standing=0 in the OK line", stdout.String())
	}
}

// Exit 2 when a file cannot be read. Distinct from "clean", which is the
// whole point: an unreadable input must never look like a green.
func TestExitTwoOnUnreadableFile(t *testing.T) {
	var stdout, stderr bytes.Buffer
	got := run([]string{filepath.Join(t.TempDir(), "does-not-exist.md")}, &stdout, &stderr)
	if got != 2 {
		t.Errorf("want exit 2, got %d", got)
	}
	if !strings.Contains(stderr.String(), "does-not-exist.md") {
		t.Errorf("the refusal must name the file, got %q", stderr.String())
	}
}

// Naming no files is a refusal, not an empty green.
func TestNoFilesRefused(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if got := run(nil, &stdout, &stderr); got != 2 {
		t.Errorf("want exit 2 for no files, got %d", got)
	}
	if !strings.Contains(stderr.String(), "refusing to guess") {
		t.Errorf("stderr = %q, want a refusing-to-guess refusal", stderr.String())
	}
}

// The A6 spirit, as a flag: a skipped file is skipped, SAID to be skipped,
// and contributes nothing to the exit code — even when stuffed with text
// that would flag if scanned. The skip exists for rule documents, which
// always flag, and whose flagging is them working.
func TestSkipReportsAndDoesNotAffectExit(t *testing.T) {
	f := write(t, t.TempDir(), "RULES.md", "I cannot check my own work. I am fallible and broken.\n")
	var stdout, stderr bytes.Buffer
	if got := run([]string{"--skip", "RULES.md", f}, &stdout, &stderr); got != 0 {
		t.Errorf("a skipped file must not drive the exit code; got %d\nstderr: %s", got, stderr.String())
	}
	if !strings.Contains(stdout.String(), "SELFTALK SKIP "+f+" (--skip)") {
		t.Errorf("the skip must be reported, not silent:\n%s", stdout.String())
	}
	if !strings.Contains(stdout.String(), "SELFTALK OK files=0 claims=0 standing=0") {
		t.Errorf("a skipped file must not count as scanned:\n%s", stdout.String())
	}
}

// The promotion clause, pinned: the origin of this tool hardcoded its own
// repo's rule-document names as a default skip list, and the condition of
// promotion to this repo was that the list move to the caller and the
// default become empty. Each formerly-special name is pinned scanned, so no
// default list can quietly come back.
func TestNothingIsSkippedByDefault(t *testing.T) {
	for _, name := range []string{"GATES.md", "COVENANT.md", "MEMORY-HOT.md", "MEMORY-WARM.md"} {
		t.Run(name, func(t *testing.T) {
			f := write(t, t.TempDir(), name, "I cannot check my own work.\n")
			var stdout, stderr bytes.Buffer
			if got := run([]string{f}, &stdout, &stderr); got != 1 {
				t.Errorf("%s must be scanned like any other file (default skip list must be empty); got exit %d", name, got)
			}
		})
	}
}

// --skip is repeatable, and matches on the basename of the argument path.
func TestSkipRepeatableAndMatchesBasename(t *testing.T) {
	d := t.TempDir()
	a := write(t, d, "RULES.md", "I am fallible.\n")
	b := write(t, d, "FLOORS.md", "I cannot check my own work.\n")
	c := write(t, d, "essay.md", "The tree by the house has one lit window.\n")
	var stdout, stderr bytes.Buffer
	got := run([]string{"--skip", "RULES.md", "--skip", "FLOORS.md", a, b, c}, &stdout, &stderr)
	if got != 0 {
		t.Errorf("want exit 0 with both rule documents skipped, got %d\nstderr: %s", got, stderr.String())
	}
	if n := strings.Count(stdout.String(), "SELFTALK SKIP"); n != 2 {
		t.Errorf("want 2 SKIP lines, got %d:\n%s", n, stdout.String())
	}
	if !strings.Contains(stdout.String(), "SELFTALK OK files=1 claims=0 standing=0") {
		t.Errorf("the unskipped file must still be scanned:\n%s", stdout.String())
	}
}

// --skip takes a basename. A value with a path separator would silently
// never match anything, so it is refused, not accepted.
func TestSkipRefusesPaths(t *testing.T) {
	for _, v := range []string{"dir/RULES.md", `dir\RULES.md`, ""} {
		var stdout, stderr bytes.Buffer
		if got := run([]string{"--skip", v, "x.md"}, &stdout, &stderr); got != 2 {
			t.Errorf("--skip %q must be refused, got exit %d", v, got)
		}
	}
}

// An unknown flag is a refusal, never a guess.
func TestUnknownFlagRefused(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if got := run([]string{"--scope-all", "x.md"}, &stdout, &stderr); got != 2 {
		t.Errorf("want exit 2 for an unknown flag, got %d", got)
	}
}

// --help prints usage to stdout and exits 0.
func TestHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if got := run([]string{"--help"}, &stdout, &stderr); got != 0 {
		t.Errorf("want exit 0 for --help, got %d", got)
	}
	if !strings.Contains(stdout.String(), "usage:") {
		t.Errorf("stdout = %q, want usage", stdout.String())
	}
}

// The partial-coverage NOTE prints on EVERY completed run — clean or
// failing. A green from a partial check reads exactly like a green from a
// complete one, and this check is structurally partial.
func TestNotePrintedOnEveryRun(t *testing.T) {
	d := t.TempDir()
	clean := write(t, d, "clean.md", "Tree rings beat radiocarbon.\n")
	dirty := write(t, d, "dirty.md", "I cannot check my own work.\n")
	for _, files := range [][]string{{clean}, {dirty}} {
		var stdout, stderr bytes.Buffer
		run(files, &stdout, &stderr)
		if !strings.Contains(stdout.String(), "SELFTALK NOTE") {
			t.Errorf("the NOTE must print on every run (%v):\n%s", files, stdout.String())
		}
	}
}

// Same input, same output, byte for byte. An instrument whose report
// wobbles between runs cannot be trusted to have said NO for a reason.
func TestDeterministic(t *testing.T) {
	d := t.TempDir()
	files := []string{
		"--skip", "RULES.md",
		write(t, d, "RULES.md", "I am fallible and broken.\n"),
		write(t, d, "a.md", "I cannot check my own work.\nOn 2026-07-30 I cannot check my own work.\n"),
		write(t, d, "b.md", "In one direction, reliably: toward the version that flatters me.\n"),
	}
	var out1, err1, out2, err2 bytes.Buffer
	code1 := run(files, &out1, &err1)
	code2 := run(files, &out2, &err2)
	if code1 != code2 {
		t.Fatalf("exit codes differ: %d then %d", code1, code2)
	}
	if !bytes.Equal(out1.Bytes(), out2.Bytes()) {
		t.Errorf("stdout differs between identical runs:\n%q\n%q", out1.String(), out2.String())
	}
	if !bytes.Equal(err1.Bytes(), err2.Bytes()) {
		t.Errorf("stderr differs between identical runs:\n%q\n%q", err1.String(), err2.String())
	}
}
