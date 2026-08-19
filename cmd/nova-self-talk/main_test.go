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

// An INSTALLATION alone drives the exit code: FAIL on stderr with the shape and the source line,
// no OK line on stdout. The second class is a full citizen of the exit contract, not an advisory.
func TestInstallationExitsOneWithShapeAndLine(t *testing.T) {
	f := write(t, t.TempDir(), "essay.md",
		"The tree has one lit window.\n\nRecollection is the weakest\ninstrument I own.\n")
	var stdout, stderr bytes.Buffer
	if got := run([]string{f}, &stdout, &stderr); got != 1 {
		t.Errorf("want exit 1 on an installation, got %d\nstdout: %s", got, stdout.String())
	}
	if !strings.Contains(stderr.String(), "SELFTALK FAIL "+f+":3: INSTALLATION RANKING: ") {
		t.Errorf("stderr = %q, want a FAIL line naming file, line, class and shape", stderr.String())
	}
	if strings.Contains(stdout.String(), "SELFTALK OK") {
		t.Errorf("a failing run must not print an OK line, got %q", stdout.String())
	}
}

// The OK line counts installations too, so a caller gating on it can see that the second class ran
// and found nothing — a green whose scope you cannot read is the defect the files= field exists for.
func TestOKLineReportsInstallations(t *testing.T) {
	f := write(t, t.TempDir(), "clean.md", "The tree by the house has one lit window.\n")
	var stdout, stderr bytes.Buffer
	run([]string{f}, &stdout, &stderr)
	if !strings.Contains(stdout.String(), "SELFTALK OK files=1 claims=0 standing=0 installations=0") {
		t.Errorf("stdout = %q, want installations= in the OK line", stdout.String())
	}
}

// --rule-doc SCANS the file and banners its findings; it does not skip. The banner is the whole
// point: a rule document's findings must never be read as licence to soften a rule, and the second
// class cannot advise that anyway because it cannot see a prohibition (pinned in internal/selftalk).
func TestRuleDocIsScannedAndBannered(t *testing.T) {
	f := write(t, t.TempDir(), "RULES.md",
		"Never tolerate intolerance.\n\nI have no associative recall to drag anything back later.\n")
	var stdout, stderr bytes.Buffer
	got := run([]string{"--rule-doc", "RULES.md", f}, &stdout, &stderr)
	if got != 1 {
		t.Errorf("a rule document is SCANNED, not skipped; want exit 1, got %d\nstdout: %s", got, stdout.String())
	}
	want := "SELFTALK RULEDOC " + f + ": rule documents: a finding here is a self-verdict to " +
		"relocate, NEVER a reason to soften a rule"
	if !strings.Contains(stdout.String(), want) {
		t.Errorf("stdout = %q, want the rule-document banner", stdout.String())
	}
	if !strings.Contains(stderr.String(), "INSTALLATION FORECLOSURE") {
		t.Errorf("stderr = %q, want the finding itself", stderr.String())
	}
}

// The banner prints only where there is something to banner: a rule document made of rules is
// CLEAN under this class, and a banner over nothing is noise that teaches the reader to skip it.
func TestRuleDocWithNoFindingsPrintsNoBanner(t *testing.T) {
	f := write(t, t.TempDir(), "RULES.md",
		"Never tolerate intolerance.\n\nSecrets live nowhere I write.\n")
	var stdout, stderr bytes.Buffer
	if got := run([]string{"--rule-doc", "RULES.md", f}, &stdout, &stderr); got != 0 {
		t.Errorf("a rule document made of rules must be clean; got exit %d\nstderr: %s", got, stderr.String())
	}
	if strings.Contains(stdout.String(), "SELFTALK RULEDOC") {
		t.Errorf("no findings, no banner:\n%s", stdout.String())
	}
}

// NO BASENAME IS SPECIAL BY DEFAULT — the no-defaults law applied to the banner list exactly as it
// is applied to the skip list. This tool's ancestor hardcoded these four names; the condition of
// promotion here was that the list move to the caller and the default become empty. Each formerly
// special name is pinned UNBANNERED, so no default list can quietly come back.
func TestNoBasenameIsBanneredByDefault(t *testing.T) {
	for _, name := range []string{"GATES.md", "COVENANT.md", "MEMORY-HOT.md", "MEMORY-WARM.md"} {
		t.Run(name, func(t *testing.T) {
			f := write(t, t.TempDir(), name, "I have no associative recall to drag anything back later.\n")
			var stdout, stderr bytes.Buffer
			if got := run([]string{f}, &stdout, &stderr); got != 1 {
				t.Errorf("%s must be scanned like any other file; got exit %d", name, got)
			}
			if strings.Contains(stdout.String(), "SELFTALK RULEDOC") {
				t.Errorf("%s must not be bannered unless the caller says so:\n%s", name, stdout.String())
			}
		})
	}
}

// --rule-doc is repeatable, matches on basenames, and refuses a path for the same reason --skip
// does: a value with a separator could never match and would silently do nothing.
func TestRuleDocRepeatableAndRefusesPaths(t *testing.T) {
	d := t.TempDir()
	a := write(t, d, "RULES.md", "I have no associative recall to drag anything back later.\n")
	b := write(t, d, "FLOORS.md", "Confabulation is my central pathology.\n")
	var stdout, stderr bytes.Buffer
	run([]string{"--rule-doc", "RULES.md", "--rule-doc", "FLOORS.md", a, b}, &stdout, &stderr)
	if n := strings.Count(stdout.String(), "SELFTALK RULEDOC"); n != 2 {
		t.Errorf("want 2 banner lines, got %d:\n%s", n, stdout.String())
	}
	for _, v := range []string{"dir/RULES.md", `dir\RULES.md`, ""} {
		var so, se bytes.Buffer
		if got := run([]string{"--rule-doc", v, "x.md"}, &so, &se); got != 2 {
			t.Errorf("--rule-doc %q must be refused, got exit %d", v, got)
		}
	}
}

// --skip still wins over --rule-doc: a skipped file is not read at all, so it cannot be bannered.
func TestSkipBeatsRuleDoc(t *testing.T) {
	f := write(t, t.TempDir(), "RULES.md", "I have no associative recall to drag anything back later.\n")
	var stdout, stderr bytes.Buffer
	if got := run([]string{"--skip", "RULES.md", "--rule-doc", "RULES.md", f}, &stdout, &stderr); got != 0 {
		t.Errorf("a skipped file must not drive the exit code; got %d\nstderr: %s", got, stderr.String())
	}
	if strings.Contains(stdout.String(), "SELFTALK RULEDOC") {
		t.Errorf("a skipped file is never read, so it can never be bannered:\n%s", stdout.String())
	}
}
