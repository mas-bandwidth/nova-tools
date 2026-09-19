package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// THE CARDS THE SHIFT ACTUALLY WROTE ARE THE FIXTURES (2026-09-19).
//
// Five managers ran a shift against this lint on 2026-09-19 -- tools10, tools11, tools12,
// tools13 and work-swarm -- and all five recorded the same sentence in their PROGRESS file:
// the drifts are false, so we launched anyway. A lint a manager has learned to launch past
// is worse than no lint, because the true finding on the next card is read the same way.
//
// So the fixtures here are not invented cards. They are three cards those managers cut,
// byte for byte, copied out of the shift's own card directories:
//
//   - tools11-c1-links-specpulse.md -- five `no-parent-path` findings, every one of them a
//     `../` the card QUOTES rather than walks: a Go test file's own relative path inside a
//     backtick span, prose naming the `../` token itself, and `ok .../internal/pulse` --
//     a `go test` ellipsis whose last two dots and slash read as a parent path.
//   - tools11-c2-links-toplevel.md -- three `no-parent-path` findings, two of them markdown
//     link TARGETS quoted from the document the card repairs, and the card is 12260 bytes,
//     over the advisory ceiling, which made the whole lint exit 2.
//   - queue-1282-bench-hygiene-home-guard.md -- the control. It was clean before this
//     change and it stays clean: the colon contract line, the clone on the STEP 1 line.
//
// EVERY ONE OF THESE MUST LINT CLEAN. Not "clean but for the known false ones" -- clean,
// exit 0. That is the whole point: the next manager reads a DRIFT line and believes it.
//
// The two tools11 cards were chosen over tools10's because tools10's cards also draw
// `deadline` and `scratch-absolute`, which are a different question and are not touched
// here; tools11's are the cards that carry the measured `../` findings.

// lintCardFile runs `lint --card` over one file in testdata/cards and returns its stdout
// and exit code, exactly as a manager on a bench runs it.
func lintCardFile(t *testing.T, name string) (string, int) {
	t.Helper()
	path := filepath.Join("testdata", "cards", name)
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the fixture card is missing: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := cmdLint([]string{"--card", path, "--max", "0"}, &stdout, &stderr)
	if stderr.Len() > 0 {
		t.Fatalf("lint --card %s wrote to stderr: %s", name, stderr.String())
	}
	return stdout.String(), code
}

// Each of the three real cards lints clean, and says its size while it does.
func TestTheShiftsOwnCardsLintClean(t *testing.T) {
	for _, name := range []string{
		"tools11-c1-links-specpulse.md",
		"tools11-c2-links-toplevel.md",
		"queue-1282-bench-hygiene-home-guard.md",
	} {
		t.Run(name, func(t *testing.T) {
			stdout, code := lintCardFile(t, name)
			if code != 0 {
				t.Fatalf("a card five managers cut and launched must lint clean; exit=%d\n%s", code, stdout)
			}
			if !strings.Contains(stdout, "LINT OK card="+name) {
				t.Fatalf("a clean card prints one LINT OK line naming itself:\n%s", stdout)
			}
			if strings.Contains(stdout, "LINT DRIFT") {
				t.Fatalf("a clean card prints no DRIFT line:\n%s", stdout)
			}
		})
	}
}

// The card that is over the ceiling still lints clean, and still says it is over: the
// ceiling is advisory (issue #1527, #1494), so it is reported and never refused.
func TestACardOverTheCeilingIsAdvisedNotRefused(t *testing.T) {
	const name = "tools11-c2-links-toplevel.md"
	stdout, code := lintCardFile(t, name)
	if code != 0 {
		t.Fatalf("the 12260-byte card is over an ADVISORY ceiling, so the lint exits 0; exit=%d\n%s", code, stdout)
	}
	if !strings.Contains(stdout, "LINT NOTE card="+name+" size: ") {
		t.Fatalf("over the ceiling is said, on a NOTE line and never a DRIFT line:\n%s", stdout)
	}
	if !strings.Contains(stdout, "advisory") {
		t.Fatalf("the word a manager needs is in the line: advisory, not a limit:\n%s", stdout)
	}
}
