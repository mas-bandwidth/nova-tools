//go:build functional

package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/onboarding"
)

// THE FIRST RUN, at the binary, for the tool the README sends a stranger to first.
//
// docs/ONBOARDING.md's five points are asserted for every command by
// internal/ci/onboarding_test.go (the bare refusal, the `example:` block, the
// `### First run` section). What can only be done in this package is the half that
// needs the fixture: the example lines are EXECUTED here, and docs/TESTS.md's
// transcript is RUN here and compared with what the tool prints.
//
// nova-bus carried a named exemption from that walk for as long as neither existed
// (`notYetOnTheStandard`, pointing at a branch). The first-send work landed; the
// transcript did not, so the one tool a newcomer is told to start with was the one
// tool excused from the standard. These two tests are what replaced the exemption.
//
// NOTHING HERE REACHES A NETWORK. The bus is the example bus copied out of
// testdata and given a repository of its own, with a bare repository beside it as
// `origin`, both under t.TempDir(): every --remote origin below pushes to a
// directory on this disk.

// firstRunClock is the transcript's clock. It stands AFTER the example bus's own
// notes (2026-09-09) on purpose: --legacy-now draws its line at this instant, so the
// notes already on the bus are history and the note this sitting sends is news. The
// package's now() stands at 2026-09-09T12:34:56Z, which is the minute the example
// bus's newest note was written, and a legacy line drawn there would hide nothing.
func firstRunClock() time.Time {
	at, err := time.Parse(time.RFC3339, "2026-09-12T20:15:00Z")
	if err != nil {
		panic(err)
	}
	return at.UTC()
}

// firstTrialBus is the fixture the first run is documented against: the example bus,
// copied out, `git init -b main`, committed, and pushed to a bare repository that is
// its origin -- which is exactly what cmd/nova-bus/testdata/example-bus/README.md
// tells a reader to do, and what every git-reading verb requires (a --bus that is not
// its repository's root is refused). It returns the working directory the transcript
// runs in and the bus inside it.
func firstTrialBus(t *testing.T) (dir, busPath string) {
	t.Helper()
	hermetic(t)
	dir = t.TempDir()
	busPath = filepath.Join(dir, "bus")
	remote := filepath.Join(dir, "bus.git")
	gitIn(t, dir, "init", "--bare", "--quiet", "--initial-branch=main", remote)
	copyTree(t, filepath.Join("testdata", "example-bus"), busPath)
	gitIn(t, busPath, "init", "--quiet", "-b", "main")
	gitIn(t, busPath, "add", "-A")
	gitIn(t, busPath, "-c", "user.name=Ada", "-c", "user.email=ada@example.com", "commit", "-q", "-m", "the bus")
	gitIn(t, busPath, "remote", "add", "origin", remote)
	gitIn(t, busPath, "push", "-q", "-u", "origin", "main")
	return dir, busPath
}

// runAt is invoke with the transcript's clock instead of the package's.
func runAt(t *testing.T, stdin string, args ...string) result {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run(args, strings.NewReader(stdin), &out, &errOut, firstRunClock())
	return result{code, out.String(), errOut.String()}
}

// localize points a documented command at this test's fixture. The line is
// SUBSTITUTED, never rewritten: `./bus` is the bus a reader makes of their own, and
// `draft.md` is the file the reader's shell redirect leaves beside it.
func localize(dir, busPath string, args []string) []string {
	out := append([]string(nil), args...)
	for i, a := range out {
		switch a {
		case "./bus":
			out[i] = busPath
		case "draft.md":
			out[i] = filepath.Join(dir, "draft.md")
		case "reply.md":
			out[i] = filepath.Join(dir, "reply.md")
		case "./drafts":
			out[i] = filepath.Join(dir, "drafts")
		}
	}
	return out
}

// splitRedirect takes `... > draft.md` off a command line and returns the path.
// `draft` prints a skeleton and NOTHING else, so its standard output is a file, and
// the transcript says so with a shell redirect. The test does here what the shell
// does there; leaving the two tokens in the argument list would be a flag parse
// error, which is not what the documented line does.
func splitRedirect(fields []string) (args []string, out string) {
	for i, f := range fields {
		if f == ">" && i+1 < len(fields) {
			return fields[:i], fields[i+1]
		}
	}
	return fields, ""
}

// (a) The usage banner's `example:` block: lines a stranger can paste, RUN against
// the fixture. "Run" is this repo's exit law -- 0 or 1 is an answer, 2 is "could not
// run", so an example exiting 2 is a broken example.
func TestUsageBannerExamplesRunAtTheExampleBus(t *testing.T) {
	t.Parallel()
	dir, busPath := firstTrialBus(t)
	banner := runAt(t, "", "help").mustCode(t, 0).stdout
	examples, err := onboarding.ExampleLines(banner, "nova-bus")
	if err != nil {
		t.Fatalf("%v\n\nwhat the banner printed:\n%s", err, banner)
	}
	if len(examples) < 2 {
		t.Fatalf("the `example:` block holds %d lines; a first sitting is more than one command: %q", len(examples), examples)
	}
	for _, ex := range examples {
		args := localize(dir, busPath, strings.Fields(ex)[1:])
		r := runAt(t, "", args...)
		if r.code == 2 {
			t.Errorf("the usage example %q does not run: exit 2 (could not run)\nstderr: %s", ex, r.stderr)
			continue
		}
		if r.stdout == "" && r.stderr == "" {
			t.Errorf("the usage example %q printed nothing", ex)
		}
	}
}

// (c) docs/TESTS.md's `### First run`, EXECUTED in order on one fixture and compared
// with what the tool prints -- by event prefix and field names, never by value, so
// the transcript stays a document instead of becoming a fixture. Ids, commits, paths
// and instants are a run's own business.
//
// One sitting, in order, because that is what the document is: what Bo is carrying,
// the receipt, the cursor she puts down, the note she writes, and then the two reads
// that show Ada's cursor being replaced and the next read costing the CHANGE.
func TestTheFirstRunTranscriptIsWhatTheToolPrints(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "TESTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := onboarding.FirstRun(string(raw), "nova-bus")
	if err != nil {
		t.Fatal(err)
	}
	dir, busPath := firstTrialBus(t)
	// What the writer has beside the bus before the reply line runs: a body they wrote,
	// and a scratch directory to put the draft in. The tool creates neither, by the same
	// law that refuses a default bus, so the transcript's own reader makes both too.
	if err := os.WriteFile(filepath.Join(dir, "reply.md"), []byte("Green on all three platforms.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "drafts"), 0o755); err != nil {
		t.Fatal(err)
	}
	seen, commands := runFirstRunShapes(t, "docs/TESTS.md", lines, dir, busPath, func(stdout string) string {
		// What the WRITER does with the file: a body over the placeholder. The send
		// line after this one is documented as reading a draft somebody finished;
		// docs/TESTS.md says so in its prose, and this is that sentence done.
		return strings.Replace(stdout, bus.PlaceholderBody, "Ada, the gate is green on all three platforms.", 1)
	})
	if commands < 8 {
		t.Fatalf("the transcript runs %d commands; the documented sitting is the whole first one", commands)
	}
	// The lines this sitting exists to show, counted rather than merely matched --
	// every shape above would still pass if the transcript quietly lost half of them.
	for prefix, want := range map[string]int{
		"NAMES OK":      1, // the roster, which is where identity lives
		"BUS OK":        1, // the bus is sound before a read is trusted
		"RECEIPT OK":    1, // heard, which is not closed
		"INBOX LEGACY":  1, // the switch-day line the first --advance demands
		"INBOX CURSOR":  2, // Bo puts hers down, Ada's is replaced
		"SEND OK":       1, // one note, written and pushed
		"INBOX REFUSED": 1, // a cursor that is not on this history, and the way out
		"INBOX BODIES":  1, // the bounded body return, with its frame above it
		"INBOX SCOPE":   5, // four full reads and the one that is mode=since
		"DRAFT OK":      1, // one reply drafted against the bus, outside the checkout
	} {
		if seen[prefix] != want {
			t.Errorf("docs/TESTS.md's nova-bus `### First run` shows %d %s lines, want %d", seen[prefix], prefix, want)
		}
	}
}

// runFirstRunShapes EXECUTES a documented nova-bus `### First run`, in order, on the
// fixture in dir, and holds every documented output line to a shape the command above
// it printed. finish is what the writer does to a redirected draft between `draft` and
// `send`: it gets what the shell wrote to the file and returns what the file holds
// when `send` reads it. It returns the documented lines' two-token prefixes, counted,
// and the number of commands run.
func runFirstRunShapes(t *testing.T, doc string, lines []string, dir, busPath string, finish func(stdout string) string) (map[string]int, int) {
	t.Helper()
	var printed map[string]bool
	seen := map[string]int{}
	commands := 0
	for _, line := range lines {
		cmd, isCommand := strings.CutPrefix(line, "$ ")
		if !isCommand {
			shape := onboarding.Shape(line)
			if shape == "" {
				continue
			}
			if printed == nil {
				t.Fatalf("%s: a transcript line stands before any command: %q", doc, line)
			}
			if !printed[shape] {
				t.Errorf("%s line\n  %s\nhas shape %q, which this tool never printed here. Re-run the command and paste what it said.", doc, line, shape)
			}
			seen[strings.Join(strings.Fields(shape)[:2], " ")]++
			continue
		}
		commands++
		fields, redirect := splitRedirect(strings.Fields(cmd))
		r := runAt(t, "", localize(dir, busPath, fields[1:])...)
		if r.code == 2 {
			t.Fatalf("%s: the documented command %q does not run: exit 2 (could not run)\nstderr: %s", doc, line, r.stderr)
		}
		if redirect != "" {
			if !strings.Contains(r.stdout, bus.PlaceholderBody) {
				t.Fatalf("%s: the skeleton %q carries no %q placeholder for a writer to replace:\n%s", doc, line, bus.PlaceholderBody, r.stdout)
			}
			if err := os.WriteFile(filepath.Join(dir, redirect), []byte(finish(r.stdout)), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		printed = map[string]bool{}
		for _, out := range strings.Split(r.stdout+"\n"+r.stderr, "\n") {
			if shape := onboarding.Shape(out); shape != "" {
				printed[shape] = true
			}
		}
	}
	return seen, commands
}

// docs/CLI.md's `### First run` is the page a stranger copies from, and until this test
// nothing ran it: the executed copy was docs/TESTS.md's, whose harness replaced the
// draft's placeholder by itself, so a CLI.md sitting whose `send` could only answer
// `SEND FAIL ... the unedited template placeholder` was green (the 2026-09-27 audit).
//
// This runs CLI.md's lines in order on the same fixture, and the step between `draft`
// and `send` is the one the page WRITES: the `sed` line in its prose, run with sh in
// the directory the transcript runs in. A page that loses that line is red here, and
// so is a page whose line leaves the placeholder standing, because `send` then prints
// no SEND OK.
func TestTheCommandReferenceFirstRunRunsAsWritten(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "CLI.md"))
	if err != nil {
		t.Fatal(err)
	}
	lines, err := onboarding.FirstRun(string(raw), "nova-bus")
	if err != nil {
		t.Fatal(err)
	}
	edit, err := documentedDraftEdit(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	dir, busPath := firstTrialBus(t)
	seen, commands := runFirstRunShapes(t, "docs/CLI.md", lines, dir, busPath, func(stdout string) string {
		path := filepath.Join(dir, "draft.md")
		if err := os.WriteFile(path, []byte(stdout), 0o644); err != nil {
			t.Fatal(err)
		}
		sh := exec.Command("sh", "-c", edit)
		sh.Dir = dir
		if out, err := sh.CombinedOutput(); err != nil {
			t.Fatalf("docs/CLI.md's edit step %q: %v\n%s", edit, err, out)
		}
		finished, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(finished)
	})
	if commands != 5 {
		t.Errorf("docs/CLI.md's nova-bus `### First run` runs %d commands, want 5: names, draft, send, and Ada's two reads", commands)
	}
	for prefix, want := range map[string]int{
		"NAMES OK":      1,
		"DRAFT NOTE":    1,
		"SEND OK":       1, // the note went: the edit step is what makes this line possible
		"INBOX REFUSED": 1,
		"INBOX NOTE":    1, // and Ada is carrying it
		"INBOX CURSOR":  1,
	} {
		if seen[prefix] != want {
			t.Errorf("docs/CLI.md's nova-bus `### First run` shows %d %s lines, want %d", seen[prefix], prefix, want)
		}
	}
}
