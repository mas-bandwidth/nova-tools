// First-run tests for nova-play: the usage banner's `example:` block and the
// docs/TESTS.md `### First run` transcript are RUN here rather than read, so a
// line that has drifted out of what the binary prints is caught by a build.
// nova-play is the sixteen-and-first command: every other `cmd/<tool>` carried a
// firstrun_test.go and this one's transcript was prose that nothing executed.
//
// WHAT IS DIFFERENT HERE FROM MOST OF ITS SIBLINGS. The transcript is compared
// LINE BY LINE AND IN ORDER, and a command that prints one line fewer or one line
// more than the document shows is red. Most siblings collect the shapes a command
// printed into a SET and ask whether each documented line is in it (see the
// `printed map[string]bool` in cmd/nova-bus and cmd/nova-work): under that
// comparison an ABRIDGED transcript passes -- drop the `  BODY ...` line, drop the
// whole `NOTE` block, repeat a line twice, and the set still contains everything
// asked of it. A transcript is a thing a person reads at a prompt and then checks
// their own screen against, so the count and the order are as much of the promise
// as the words.
//
// THE ONE VALUE NOT COMPARED. docs/TESTS.md has no placeholder convention: every
// transcript in it is real output pasted whole. nova-play's ids are content
// addresses -- sha12(author+passage+note) and sha12(author+body) -- so they
// reproduce exactly and are compared byte for byte, as are the authors, the
// counts, the source path, the passage and body text, and the two-space indents.
// The `created=` instant is the one field a rerun cannot reproduce, so it alone is
// matched as an instant rather than as its digits, by createdInstant below. A
// `created=` the document spells any other way does not match that pattern, stays
// what the document wrote, and fails against what the tool printed.
//
// NOTHING HERE TOUCHES A PATH OUTSIDE t.TempDir(). The transcript names its source
// as the bare `story.txt`, which is what a reader types, so the test runs in a temp
// directory of its own (t.Chdir) and the documented lines run verbatim -- and the
// `source=story.txt` the tool prints back is verbatim too, which a rewritten path
// would not be.
package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// firstRunSource is the source text the transcript annotates: three lines of
// prose holding the documented passage exactly once. It is more than the passage
// on purpose -- an anchor into a one-line file proves nothing about anchoring --
// and it is small enough to read in a sitting, which is what docs/TESTS.md's
// nova-play fixture line promises a reader they can rebuild.
const firstRunSource = `The keeper climbed the last stair before dawn.
The lantern room held a brass fitting.
Below, the harbour was still asleep.
`

// firstRunDir puts the transcript's fixture in a temp directory and moves the
// test into it, so that `--source story.txt` means what it says on the page. The
// notes sidecar the verbs write lands beside it and goes with the directory.
//
// ONE directory serves a whole transcript rather than one per line: the lines are
// a sitting -- annotate, then read what was written, then reply to it by the id
// the first line printed -- and the sidecar has to survive from one to the next.
func firstRunDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeSource(t, dir, "story.txt", firstRunSource)
	t.Chdir(dir)
	return dir
}

// runPlay invokes the binary's own entry point with the documented arguments.
func runPlay(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// readTranscriptDoc returns docs/TESTS.md, the document these tests execute.
func readTranscriptDoc(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// createdInstant matches the value of a `created=` field: an RFC3339 instant in
// UTC, which is the format cmd/nova-play/main.go prints and the only part of this
// transcript that belongs to the run rather than to the document. Everything else
// on the line is compared as written.
var createdInstant = regexp.MustCompile(`created=[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z`)

// theInstantOfThisRun is what a matched instant becomes on both sides of the
// comparison. It is spelled as a sentence because it is read in a failure
// message, beside the line it stood in.
const theInstantOfThisRun = "created=<the instant of this run>"

// elideInstant is applied to the document's line and to the tool's line alike.
func elideInstant(line string) string {
	return createdInstant.ReplaceAllString(line, theInstantOfThisRun)
}

// shellFields splits a documented command line the way the shell a reader is
// typing into would: whitespace separates arguments, and a double-quoted run is
// ONE argument with its quotes removed. nova-play's transcript is the first in
// this repository whose arguments are sentences -- a passage and a note -- so the
// strings.Fields every sibling uses would hand the tool eleven arguments where
// the reader typed eight, and the passage would not be found in the source. An
// unterminated quote is the document's own bug and is fatal here rather than
// guessed at.
func shellFields(t *testing.T, line string) []string {
	t.Helper()
	var fields []string
	var cur strings.Builder
	quoted, started := false, false
	for _, r := range line {
		switch {
		case r == '"':
			quoted = !quoted
			started = true
		case (r == ' ' || r == '\t') && !quoted:
			if started {
				fields = append(fields, cur.String())
				cur.Reset()
				started = false
			}
		default:
			cur.WriteRune(r)
			started = true
		}
	}
	if quoted {
		t.Fatalf("the documented command line has an unterminated quote:\n  %s", line)
	}
	if started {
		fields = append(fields, cur.String())
	}
	return fields
}

// step is one `$` line of the transcript and EVERY line the document says it
// prints, in the order the document prints them.
type step struct {
	line string   // the `$ ...` line as the document writes it, for failure messages
	args []string // what the shell would hand the tool
	want []string // the output lines below it, in order
}

// firstRunSteps cuts a transcript into its commands and their outputs. A line
// opening with `$ ` starts a step; every line after it belongs to that step until
// the next `$ ` line, with the blank line the document leaves between commands
// dropped from the end. Output before the first command, and a `$` line for some
// other tool, are the document's bugs and are fatal: they would otherwise be
// skipped, and a skipped line is the abridgement this file exists to catch.
func firstRunSteps(t *testing.T, tool string, lines []string) []step {
	t.Helper()
	var steps []step
	for _, line := range lines {
		cmd, isCommand := strings.CutPrefix(line, "$ ")
		if !isCommand {
			if len(steps) == 0 {
				if strings.TrimSpace(line) == "" {
					continue
				}
				t.Fatalf("a transcript line stands before any command:\n  %s", line)
			}
			last := &steps[len(steps)-1]
			last.want = append(last.want, line)
			continue
		}
		args := shellFields(t, cmd)
		if len(args) < 2 || args[0] != tool {
			t.Fatalf("the transcript line %q is not a %s command", line, tool)
		}
		steps = append(steps, step{line: line, args: args[1:]})
	}
	for i := range steps {
		for len(steps[i].want) > 0 && strings.TrimSpace(steps[i].want[len(steps[i].want)-1]) == "" {
			steps[i].want = steps[i].want[:len(steps[i].want)-1]
		}
	}
	return steps
}

// outputLines splits what the tool wrote into the lines a reader sees, dropping
// only the final newline that ends the last of them. A blank line the tool
// printed is KEPT and counted, because the document would have to show it.
func outputLines(out string) []string {
	if out == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(out, "\n"), "\n")
}

// TestFirstRunTranscriptIsWhatTheToolPrintsLineForLine runs `### First run` in
// order in one directory and compares each command's whole output with the block
// written under it: same number of lines, same lines, same order.
func TestFirstRunTranscriptIsWhatTheToolPrintsLineForLine(t *testing.T) {
	lines, err := onboarding.FirstRun(readTranscriptDoc(t), "nova-play")
	if err != nil {
		t.Fatal(err)
	}
	steps := firstRunSteps(t, "nova-play", lines)
	if len(steps) == 0 {
		t.Fatal("the `### First run` block holds no nova-play command; this test would pass by running nothing")
	}
	// The sitting is the whole tool: a note put down, the reading that shows it,
	// and an answer to it. A transcript that has quietly lost one of the three
	// verbs is short of what a first run needs, and no per-line comparison below
	// would say so, because the lines that remain would still match.
	verbs := map[string]bool{}
	for _, s := range steps {
		verbs[s.args[0]] = true
	}
	for _, verb := range []string{"annotate", "read", "reply"} {
		if !verbs[verb] {
			t.Errorf("the `### First run` block never runs `nova-play %s`; the first sitting is all three verbs", verb)
		}
	}

	firstRunDir(t)
	for _, s := range steps {
		code, stdout, stderr := runPlay(t, s.args...)
		if code != 0 {
			t.Fatalf("the documented command\n  %s\nexits %d (want 0)\nstderr: %s", s.line, code, stderr)
		}
		if stderr != "" {
			t.Errorf("the documented command\n  %s\nwrote to stderr, which the transcript does not show:\n%s", s.line, stderr)
		}
		got := outputLines(stdout)
		if len(got) != len(s.want) {
			t.Errorf("the documented command\n  %s\nprints %d lines and docs/TESTS.md shows %d.\nwhat the document says:\n%s\nwhat the tool printed:\n%s",
				s.line, len(got), len(s.want), block(s.want), block(got))
			continue
		}
		for i := range s.want {
			if elideInstant(s.want[i]) == elideInstant(got[i]) {
				continue
			}
			t.Errorf("under\n  %s\ndocs/TESTS.md line %d of %d reads\n  %s\nand the tool printed\n  %s\nRe-run the command and paste what it said.",
				s.line, i+1, len(s.want), s.want[i], got[i])
		}
	}
}

// block indents a set of lines for a failure message, so that the document's
// block and the tool's stand under each other and can be read side by side.
func block(lines []string) string {
	if len(lines) == 0 {
		return "  (nothing)"
	}
	return "  " + strings.Join(lines, "\n  ")
}

// TestUsageBannerExamplesRun executes every line under the banner's `example:`
// heading, in order, in one directory -- the third of them replies by the id the
// first one prints, so an example that drifts out of the others is red here.
func TestUsageBannerExamplesRun(t *testing.T) {
	code, banner, stderr := runPlay(t, "help")
	if code != 0 {
		t.Fatalf("`nova-play help` exits %d, want 0; stderr: %s", code, stderr)
	}
	examples, err := onboarding.ExampleLines(banner, "nova-play")
	if err != nil {
		t.Fatalf("%v\n\nwhat the banner printed:\n%s", err, banner)
	}
	firstRunDir(t)
	for _, ex := range examples {
		args := shellFields(t, ex)
		code, stdout, stderr := runPlay(t, args[1:]...)
		if code == 2 {
			t.Errorf("the usage example %q does not run: exit 2 (could not run)\nstderr: %s", ex, stderr)
			continue
		}
		if code != 0 {
			t.Errorf("the usage example %q ran but said NO (exit %d)\nstderr: %s", ex, code, stderr)
		}
		if stdout == "" {
			t.Errorf("the usage example %q printed nothing on stdout", ex)
		}
	}
}

// TestTESTSNamesNovaPlayOnce is #1550's guard, asserted for this tool where this
// tool's transcript is executed. onboarding.Section cuts to the FIRST `## nova-play`
// and stops, so a second section is read by no test and by no reader looking for
// the one place to change -- which is exactly how `## nova-work` grew two sentences
// the binary no longer printed. internal/ci holds the same guard for the whole
// document; this one fails in the package whose transcript would be the half that
// nothing runs.
func TestTESTSNamesNovaPlayOnce(t *testing.T) {
	sections := 0
	for _, name := range onboarding.SectionNames(readTranscriptDoc(t)) {
		if name == "nova-play" {
			sections++
		}
	}
	if sections != 1 {
		t.Fatalf("docs/TESTS.md heads %d `## nova-play` sections, want exactly 1; only the first is executed by this file", sections)
	}
}
