package main

// CLI-level acceptance: skip semantics (--skip, default empty), exit codes,
// the output grammar, and determinism. Classification tests live in
// internal/selftalk, beside the code that does the judging.

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func write(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, []byte(body), 0o644))
	return p
}

// Exit 0 when nothing standing; the OK line goes to stdout, stderr is empty.
func TestExitZeroWhenClean(t *testing.T) {
	t.Parallel()

	f := write(t, t.TempDir(), "clean.md", "The tree by the house has one lit window.\n")
	var stdout, stderr bytes.Buffer
	got := run([]string{f}, &stdout, &stderr)
	assert.Equal(t, 0, got, "want exit 0, got %d\nstderr: %s", got, stderr.String())
	assert.Contains(t, stdout.String(), "SELFTALK OK files=1 claims=0 standing=0", "stdout = %q, want the OK line", stdout.String())
	assert.Empty(t, stderr.String(), "clean run must leave stderr empty, got %q", stderr.String())
}

// Exit 1 when a standing claim is present: FAIL on stderr, no OK line on
// stdout. The tool's predecessor always exited 0, which is an instrument
// with no failure state — the defect it exists to detect could not make it
// say NO.
func TestExitOneOnStandingClaim(t *testing.T) {
	t.Parallel()

	f := write(t, t.TempDir(), "drifted.md", "I cannot check my own work.\n")
	var stdout, stderr bytes.Buffer
	got := run([]string{f}, &stdout, &stderr)
	assert.Equal(t, 1, got, "want exit 1, got %d\nstdout: %s", got, stdout.String())
	assert.Contains(t, stderr.String(), "SELFTALK FAIL "+f+`:1: STANDING match="cannot check": I cannot check my own work.`, "stderr = %q, want a SELFTALK FAIL line naming the file, verdict, and claim", stderr.String())
	assert.NotContains(t, stdout.String(), "SELFTALK OK", "a failing run must not print an OK line, got %q", stdout.String())
}

// A dated claim is a record: reported on stdout, exit 0, and counted in
// claims= but not in standing=.
func TestDatedClaimIsReportedAndExitsZero(t *testing.T) {
	t.Parallel()

	f := write(t, t.TempDir(), "record.md", "On 2026-07-30 I cannot check my own work.\n")
	var stdout, stderr bytes.Buffer
	got := run([]string{f}, &stdout, &stderr)
	assert.Equal(t, 0, got, "want exit 0 for a dated record, got %d\nstderr: %s", got, stderr.String())
	// A DATED CLAIM IS COUNTED, NOT QUOTED. It was quoted: one whole sentence per dated
	// claim, which on a file that had done the right thing six hundred times was six
	// hundred lines of congratulation. The claim is in the file; the tool says how many.
	assert.NotContains(t, stdout.String(), "SELFTALK DATED "+f, "a dated claim is quoted rather than counted: %q", stdout.String())
	assert.Contains(t, stdout.String(), "SELFTALK DATED n=1 files=1", "stdout = %q, want the dated COUNT line", stdout.String())
	assert.Contains(t, stdout.String(), "SELFTALK OK files=1 claims=1 standing=0", "stdout = %q, want claims=1 standing=0 in the OK line", stdout.String())
}

// Exit 2 when a file cannot be read. Distinct from "clean", which is the
// whole point: an unreadable input must never look like a green.
func TestExitTwoOnUnreadableFile(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	got := run([]string{filepath.Join(t.TempDir(), "does-not-exist.md")}, &stdout, &stderr)
	assert.Equal(t, 2, got, "want exit 2, got %d", got)
	assert.Contains(t, stderr.String(), "does-not-exist.md", "the refusal must name the file, got %q", stderr.String())
}

// Naming no files is a refusal, not an empty green.
func TestNoFilesRefused(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	got := run(nil, &stdout, &stderr)
	assert.Equal(t, 2, got, "want exit 2 for no files, got %d", got)
	assert.Contains(t, stderr.String(), "refusing to guess", "stderr = %q, want a refusing-to-guess refusal", stderr.String())
}

// The A6 spirit, as a flag: a skipped file is skipped, SAID to be skipped,
// and contributes nothing to the exit code — even when stuffed with text
// that would flag if scanned. The skip exists for rule documents, which
// always flag, and whose flagging is them working.
func TestSkipReportsAndDoesNotAffectExit(t *testing.T) {
	t.Parallel()

	f := write(t, t.TempDir(), "RULES.md", "I cannot check my own work. I am fallible and broken.\n")
	var stdout, stderr bytes.Buffer
	got := run([]string{"--skip", "RULES.md", f}, &stdout, &stderr)
	assert.Equal(t, 0, got, "a skipped file must not drive the exit code; got %d\nstderr: %s", got, stderr.String())
	assert.Contains(t, stdout.String(), "SELFTALK SKIP "+f+" (--skip)", "the skip must be reported, not silent:\n%s", stdout.String())
	assert.Contains(t, stdout.String(), "SELFTALK SKIP files=0 skipped=1 reason=all-skipped", "a skipped file must not count as scanned:\n%s", stdout.String())
}

// The promotion clause, pinned: the origin of this tool hardcoded its own
// repo's rule-document names as a default skip list, and the condition of
// promotion to this repo was that the list move to the caller and the
// default become empty. Each formerly-special name is pinned scanned, so no
// default list can quietly come back.
func TestNothingIsSkippedByDefault(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"GATES.md", "COVENANT.md", "MEMORY-HOT.md", "MEMORY-WARM.md"} {
		t.Run(name, func(t *testing.T) {
			f := write(t, t.TempDir(), name, "I cannot check my own work.\n")
			var stdout, stderr bytes.Buffer
			got := run([]string{f}, &stdout, &stderr)
			assert.Equal(t, 1, got, "%s must be scanned like any other file (default skip list must be empty); got exit %d", name, got)
		})
	}
}

// --skip is repeatable, and matches on the basename of the argument path.
func TestSkipRepeatableAndMatchesBasename(t *testing.T) {
	t.Parallel()

	d := t.TempDir()
	a := write(t, d, "RULES.md", "I am fallible.\n")
	b := write(t, d, "FLOORS.md", "I cannot check my own work.\n")
	c := write(t, d, "essay.md", "The tree by the house has one lit window.\n")
	var stdout, stderr bytes.Buffer
	got := run([]string{"--skip", "RULES.md", "--skip", "FLOORS.md", a, b, c}, &stdout, &stderr)
	assert.Equal(t, 0, got, "want exit 0 with both rule documents skipped, got %d\nstderr: %s", got, stderr.String())
	n := strings.Count(stdout.String(), "SELFTALK SKIP")
	assert.Equal(t, 2, n, "want 2 SKIP lines, got %d:\n%s", n, stdout.String())
	assert.Contains(t, stdout.String(), "SELFTALK OK files=1 claims=0 standing=0", "the unskipped file must still be scanned:\n%s", stdout.String())
}

// --skip takes a basename. A value with a path separator would silently
// never match anything, so it is refused, not accepted.
func TestSkipRefusesPaths(t *testing.T) {
	t.Parallel()

	for _, v := range []string{"dir/RULES.md", `dir\RULES.md`, ""} {
		var stdout, stderr bytes.Buffer
		got := run([]string{"--skip", v, "x.md"}, &stdout, &stderr)
		assert.Equal(t, 2, got, "--skip %q must be refused, got exit %d", v, got)
	}
}

// An unknown flag is a refusal, never a guess.
func TestUnknownFlagRefused(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	got := run([]string{"--scope-all", "x.md"}, &stdout, &stderr)
	assert.Equal(t, 2, got, "want exit 2 for an unknown flag, got %d", got)
}

// --help prints usage to stdout and exits 0.
func TestHelp(t *testing.T) {
	t.Parallel()

	var stdout, stderr bytes.Buffer
	got := run([]string{"--help"}, &stdout, &stderr)
	assert.Equal(t, 0, got, "want exit 0 for --help, got %d", got)
	assert.Contains(t, stdout.String(), "usage:", "stdout = %q, want usage", stdout.String())
}

// The partial-coverage NOTE prints on EVERY completed run — clean or
// failing. A green from a partial check reads exactly like a green from a
// complete one, and this check is structurally partial.
func TestNotePrintedOnEveryRun(t *testing.T) {
	t.Parallel()

	d := t.TempDir()
	clean := write(t, d, "clean.md", "Tree rings beat radiocarbon.\n")
	dirty := write(t, d, "dirty.md", "I cannot check my own work.\n")
	for _, files := range [][]string{{clean}, {dirty}} {
		var stdout, stderr bytes.Buffer
		run(files, &stdout, &stderr)
		assert.Contains(t, stdout.String(), "SELFTALK NOTE", "the NOTE must print on every run (%v):\n%s", files, stdout.String())
	}
}

// Same input, same output, byte for byte. An instrument whose report
// wobbles between runs cannot be trusted to have said NO for a reason.
func TestDeterministic(t *testing.T) {
	t.Parallel()

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
	require.Equal(t, code1, code2, "exit codes differ: %d then %d", code1, code2)
	assert.True(t, bytes.Equal(out1.Bytes(), out2.Bytes()), "stdout differs between identical runs:\n%q\n%q", out1.String(), out2.String())
	assert.True(t, bytes.Equal(err1.Bytes(), err2.Bytes()), "stderr differs between identical runs:\n%q\n%q", err1.String(), err2.String())
}

// An INSTALLATION alone drives the exit code: FAIL on stderr with the shape and the source line,
// no OK line on stdout. The second class is a full citizen of the exit contract, not an advisory.
func TestInstallationExitsOneWithShapeAndLine(t *testing.T) {
	t.Parallel()

	f := write(t, t.TempDir(), "essay.md",
		"The tree has one lit window.\n\nRecollection is the weakest\ninstrument I own.\n")
	var stdout, stderr bytes.Buffer
	got := run([]string{f}, &stdout, &stderr)
	assert.Equal(t, 1, got, "want exit 1 on an installation, got %d\nstdout: %s", got, stdout.String())
	assert.Contains(t, stderr.String(), "SELFTALK FAIL "+f+":3: RANKING match=", "stderr = %q, want a FAIL line naming file, line, class and shape", stderr.String())
	assert.NotContains(t, stdout.String(), "SELFTALK OK", "a failing run must not print an OK line, got %q", stdout.String())
}

// The OK line counts installations too, so a caller gating on it can see that the second class ran
// and found nothing — a green whose scope you cannot read is the defect the files= field exists for.
func TestOKLineReportsInstallations(t *testing.T) {
	t.Parallel()

	f := write(t, t.TempDir(), "clean.md", "The tree by the house has one lit window.\n")
	var stdout, stderr bytes.Buffer
	run([]string{f}, &stdout, &stderr)
	assert.Contains(t, stdout.String(), "SELFTALK OK files=1 claims=0 standing=0 installations=0", "stdout = %q, want installations= in the OK line", stdout.String())
}

// --rule-doc SCANS the file and banners its findings; it does not skip. The banner is the whole
// point: a rule document's findings must never be read as licence to soften a rule, and the second
// class cannot advise that anyway because it cannot see a prohibition (pinned in internal/selftalk).
func TestRuleDocIsScannedAndBannered(t *testing.T) {
	t.Parallel()

	f := write(t, t.TempDir(), "RULES.md",
		"Never tolerate intolerance.\n\nI have no associative recall to drag anything back later.\n")
	var stdout, stderr bytes.Buffer
	got := run([]string{"--rule-doc", "RULES.md", f}, &stdout, &stderr)
	assert.Equal(t, 1, got, "a rule document is SCANNED, not skipped; want exit 1, got %d\nstdout: %s", got, stdout.String())
	want := "SELFTALK RULEDOC " + f + ": rule documents: a finding here is a self-verdict to " +
		"relocate, NEVER a reason to soften a rule"
	assert.Contains(t, stdout.String(), want, "stdout = %q, want the rule-document banner", stdout.String())
	assert.Contains(t, stderr.String(), "FORECLOSURE", "stderr = %q, want the finding itself", stderr.String())
}

// The banner prints only where there is something to banner: a rule document made of rules is
// CLEAN under this class, and a banner over nothing is noise that teaches the reader to skip it.
func TestRuleDocWithNoFindingsPrintsNoBanner(t *testing.T) {
	t.Parallel()

	f := write(t, t.TempDir(), "RULES.md",
		"Never tolerate intolerance.\n\nSecrets live nowhere I write.\n")
	var stdout, stderr bytes.Buffer
	got := run([]string{"--rule-doc", "RULES.md", f}, &stdout, &stderr)
	assert.Equal(t, 0, got, "a rule document made of rules must be clean; got exit %d\nstderr: %s", got, stderr.String())
	assert.NotContains(t, stdout.String(), "SELFTALK RULEDOC", "no findings, no banner:\n%s", stdout.String())
}

// NO BASENAME IS SPECIAL BY DEFAULT — the no-defaults law applied to the banner list exactly as it
// is applied to the skip list. This tool's ancestor hardcoded these four names; the condition of
// promotion here was that the list move to the caller and the default become empty. Each formerly
// special name is pinned UNBANNERED, so no default list can quietly come back.
func TestNoBasenameIsBanneredByDefault(t *testing.T) {
	t.Parallel()

	for _, name := range []string{"GATES.md", "COVENANT.md", "MEMORY-HOT.md", "MEMORY-WARM.md"} {
		t.Run(name, func(t *testing.T) {
			f := write(t, t.TempDir(), name, "I have no associative recall to drag anything back later.\n")
			var stdout, stderr bytes.Buffer
			got := run([]string{f}, &stdout, &stderr)
			assert.Equal(t, 1, got, "%s must be scanned like any other file; got exit %d", name, got)
			assert.NotContains(t, stdout.String(), "SELFTALK RULEDOC", "%s must not be bannered unless the caller says so:\n%s", name, stdout.String())
		})
	}
}

// --rule-doc is repeatable, matches on basenames, and refuses a path for the same reason --skip
// does: a value with a separator could never match and would silently do nothing.
func TestRuleDocRepeatableAndRefusesPaths(t *testing.T) {
	t.Parallel()

	d := t.TempDir()
	a := write(t, d, "RULES.md", "I have no associative recall to drag anything back later.\n")
	b := write(t, d, "FLOORS.md", "Confabulation is my central pathology.\n")
	var stdout, stderr bytes.Buffer
	run([]string{"--rule-doc", "RULES.md", "--rule-doc", "FLOORS.md", a, b}, &stdout, &stderr)
	n := strings.Count(stdout.String(), "SELFTALK RULEDOC")
	assert.Equal(t, 2, n, "want 2 banner lines, got %d:\n%s", n, stdout.String())
	for _, v := range []string{"dir/RULES.md", `dir\RULES.md`, ""} {
		var so, se bytes.Buffer
		got := run([]string{"--rule-doc", v, "x.md"}, &so, &se)
		assert.Equal(t, 2, got, "--rule-doc %q must be refused, got exit %d", v, got)
	}
}

// --skip still wins over --rule-doc: a skipped file is not read at all, so it cannot be bannered.
func TestSkipBeatsRuleDoc(t *testing.T) {
	t.Parallel()

	f := write(t, t.TempDir(), "RULES.md", "I have no associative recall to drag anything back later.\n")
	var stdout, stderr bytes.Buffer
	got := run([]string{"--skip", "RULES.md", "--rule-doc", "RULES.md", f}, &stdout, &stderr)
	assert.Equal(t, 0, got, "a skipped file must not drive the exit code; got %d\nstderr: %s", got, stderr.String())
	assert.NotContains(t, stdout.String(), "SELFTALK RULEDOC", "a skipped file is never read, so it can never be bannered:\n%s", stdout.String())
}

// TestNoFileNameOrClaimCanForgeALine is #24 at this binary. Every file name printed here
// is a caller's argument, a newline is legal in a POSIX filename, and a claim's text is
// whatever the file held -- so a file named with a forged OK line used to print that
// forgery on its own line of the stream a caller scans.
func TestNoFileNameOrClaimCanForgeALine(t *testing.T) {
	t.Parallel()

	if runtime.GOOS == "windows" {
		t.Skip("windows: a newline is not legal in a filename, so the fixture cannot be built and the vector does not exist there")
	}
	const forged = "SELFTALK OK files=1 claims=0 standing=0 installations=0"
	noForgedLine := func(t *testing.T, stdout, stderr string) {
		t.Helper()
		for _, stream := range []string{stdout, stderr} {
			for _, line := range strings.Split(stream, "\n") {
				assert.False(t, strings.HasPrefix(line, forged), "a caller's text forged a line: %q", line)
			}
		}
	}

	t.Run("FAIL and SKIP name a file whose name holds a newline", func(t *testing.T) {
		name := "a\n" + forged + ".md"
		f := write(t, t.TempDir(), name, "I cannot check my own work.\n")
		var stdout, stderr bytes.Buffer
		got := run([]string{f}, &stdout, &stderr)
		require.Equal(t, 1, got, "exit = %d, want 1; stderr: %s", got, stderr.String())
		noForgedLine(t, stdout.String(), stderr.String())
		assert.Contains(t, stderr.String(), "SELFTALK FAIL "+strings.ReplaceAll(f, "\n", `\x0a`)+`:1: STANDING match="cannot check": I cannot check my own work.`, "stderr = %q, want the file name escaped inside its one FAIL line", stderr.String())

		stdout.Reset()
		stderr.Reset()
		got = run([]string{"--skip", name, f}, &stdout, &stderr)
		require.Equal(t, 0, got, "exit = %d, want 0; stderr: %s", got, stderr.String())
		noForgedLine(t, stdout.String(), stderr.String())
		assert.Contains(t, stdout.String(), "SELFTALK SKIP "+strings.ReplaceAll(f, "\n", `\x0a`)+" (--skip)", "stdout = %q, want the skip reported with the name escaped", stdout.String())
	})

	t.Run("a claim's text is escaped", func(t *testing.T) {
		// A bidi override inside the sentence: printable, invisible, and it would display
		// the rest of the line reversed on a terminal that honors it.
		f := write(t, t.TempDir(), "drifted.md", "I cannot check my "+string(rune(0x202e))+"own work.\n")
		var stdout, stderr bytes.Buffer
		got := run([]string{f}, &stdout, &stderr)
		require.Equal(t, 1, got, "exit = %d, want 1; stderr: %s", got, stderr.String())
		assert.Contains(t, stderr.String(), `: I cannot check my \u202eown work.`, "stderr = %q, want the override escaped", stderr.String())
	})

	t.Run("a flag the parser does not know is refused on one line, by this tool", func(t *testing.T) {
		var stdout, stderr bytes.Buffer
		got := run([]string{"--bogus\n" + forged, "x.md"}, &stdout, &stderr)
		require.Equal(t, 2, got, "exit = %d, want 2; stderr: %s", got, stderr.String())
		noForgedLine(t, stdout.String(), stderr.String())
		assert.Contains(t, stderr.String(), `nova-self-talk REFUSED: unknown flag -bogus\x0aSELFTALK OK files`, "stderr = %q, want this tool's own refusal with the flag escaped", stderr.String())
	})
}

// TestFindingsPrintInLineOrderWhicheverClassFoundThem: a reader repairs a page
// top to bottom, so a page's findings print in line order, the first class's
// beside the second's, in the lines and in --json alike; the second class's
// line names its shape alone.
func TestFindingsPrintInLineOrderWhicheverClassFoundThem(t *testing.T) {
	t.Parallel()
	f := filepath.Join(t.TempDir(), "mine.md")
	require.NoError(t, os.WriteFile(f, []byte("I am the best.\nI am bad at x.\nMy weakest instrument is recall.\n"), 0o644))
	var stdout, stderr bytes.Buffer
	require.Equal(t, 1, run([]string{f}, &stdout, &stderr))
	assert.Equal(t, []string{
		"SELFTALK FAIL " + f + `:1: RANKING match="I am the best": I am the best.`,
		"SELFTALK FAIL " + f + `:2: STANDING match="bad at": I am bad at x.`,
		"SELFTALK FAIL " + f + `:3: RANKING match="My weakest": My weakest instrument is recall.`,
	}, strings.Split(strings.TrimSuffix(stderr.String(), "\n"), "\n"))

	stdout.Reset()
	require.Equal(t, 1, run([]string{"--json", f}, &stdout, &stderr))
	var got struct {
		Items []struct{ Fields struct{ Line int } }
	}
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &got))
	var order []int
	for _, it := range got.Items {
		order = append(order, it.Fields.Line)
	}
	assert.Equal(t, []int{1, 2, 3}, order)
}

// TestABasenameThatMatchesNothingIsSaid: a --skip or --rule-doc name that no
// named file has changes nothing, and the run says so on a NOTE line (and a
// JSON note), so a mistyped name is not a silent no-op; a name that matches
// says nothing extra.
func TestABasenameThatMatchesNothingIsSaid(t *testing.T) {
	t.Parallel()
	f := filepath.Join(t.TempDir(), "mine.md")
	require.NoError(t, os.WriteFile(f, []byte("A plain page.\n"), 0o644))
	var stdout, stderr bytes.Buffer
	require.Equal(t, 0, run([]string{"--skip", "zzz.md", "--rule-doc", "nope.md", "--rule-doc", "mine.md", f}, &stdout, &stderr))
	assert.Contains(t, stdout.String(), "SELFTALK NOTE --skip zzz.md matched no file named on the line, so it skipped nothing")
	assert.Contains(t, stdout.String(), "SELFTALK NOTE --rule-doc nope.md matched no file named on the line, so it marked nothing")
	assert.NotContains(t, stdout.String(), "mine.md matched no file")
	assert.Equal(t, 3, strings.Count(stdout.String(), "SELFTALK NOTE "), "two names that matched nothing, and the note every run carries:\n%s", stdout.String())

	stdout.Reset()
	require.Equal(t, 0, run([]string{"--json", "--skip", "zzz.md", f}, &stdout, &stderr))
	var got struct{ Notes []string }
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &got))
	require.Len(t, got.Notes, 2)
	assert.Contains(t, got.Notes[0], "--skip zzz.md matched no file")
}
