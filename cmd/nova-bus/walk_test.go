package main

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

// TestInboxSinceWalkReportsProgressAndHonoursMaxCommits is card 9376.
//
// Glenn's hard rule: a program that takes longer than 0.1 s says what it is doing, on
// stderr. The failure this test is written against ran four minutes with no output on a
// bus whose cursor was 285 commits stale, then printed nothing new. So the since-walk
// says where it is while it runs -- `INBOX WALK commits=<n>/<total> notes=<n> elapsed=<s>`
// -- and a stale cursor is bounded by --max-commits (default 500), with one line naming
// the way out when the bound is hit and exit 0.
func TestInboxSinceWalkReportsProgressAndHonoursMaxCommits(t *testing.T) {
	t.Parallel()
	hermetic(t)
	checkout := longBus(t, 1000)

	// A walk the bound allows finishes and reports progress at its end. The line is the
	// shape the rule asks for: commits walked over the total, notes parsed, elapsed.
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--max-commits", "2000").
		mustCode(t, 0).
		mustContain(t, "stderr", "INBOX WALK commits=1000/1000 notes=0 elapsed=")

	// The default bound stops the same walk at 500 commits: no listing, one remedy, exit 0.
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40").
		mustCode(t, 0).
		mustContain(t, "stderr", `INBOX WALK bounded commits=500 cursor=`).
		mustContain(t, "stderr", `behind=more-than-500 notes=0 remedy="raise --max-commits or close --before <instant>"`)

	// A tighter bound stops at the number the caller gave, with the same remedy.
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--max-commits", "100").
		mustCode(t, 0).
		mustContain(t, "stderr", `INBOX WALK bounded commits=100 cursor=`).
		mustContain(t, "stderr", `behind=more-than-100 notes=0 remedy="raise --max-commits or close --before <instant>"`)
}

// TestAStaleCursorCostsTheBoundAndNotTheDistance is the second half of card 9376: the walk
// itself, measured.
//
// THE FAILURE, on the bus this was written for: `inbox` over a cursor 285 commits stale,
// carrying 2150 notes, ran four minutes and printed nothing new. The bound above stops the
// READ, and until this test it did not stop the ASKING: `rev-list --count <cursor>..HEAD`
// walks every commit between the two ends before it can answer, so the one run that reads
// nothing was still paying for the whole distance in order to be told not to read it.
//
// So the numbers here are COUNTS and never a clock -- bus.CommitsWalked, taken where the
// commits are enumerated, and bus.NoteParses, taken where a note is opened. A wall-clock
// assertion over a fixture this size is a flake on a shared runner and proves nothing on a
// fast enough machine; a count of work not done is the only honest proof there is.
//
// NOT PARALLEL, for the reason the two tests in cursor_test.go are not: both counters are
// one number for the whole process, and every assertion below is an exact number.
func TestAStaleCursorCostsTheBoundAndNotTheDistance(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: builds a thousand-commit, two-thousand-note fixture; runs on the self-hosted legs and nightly")
	}
	hermetic(t)
	const commits = 1000
	const notes = 2000
	checkout, base := longBusOfNotes(t, commits, notes)
	// The stale cursor: the commit the fixture stood at, a thousand commits and two
	// thousand notes ago. It is written and not committed, as longBus writes its own -- a
	// cursor commit of its own would stand one commit above the import and every count below
	// would be one out.
	writeFile(t, checkout, "from-ada/CURSOR", base+" 2026-09-09T12:00:00Z open=0\n")

	// BEFORE: what the question cost when it was asked without its bound. The distance is
	// the whole thousand, and every one of them is walked to say so -- on the live bus, 285
	// commits for a read that then did nothing.
	mark := bus.CommitsWalked()
	distance, err := bus.CommitsBetween(checkout, base, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	unbounded := bus.CommitsWalked() - mark
	if distance != commits || unbounded != int64(commits) {
		t.Fatalf("the unbounded count answered %d after walking %d commits, want %d and %d; the fixture is not what this test thinks it is", distance, unbounded, commits, commits)
	}

	// AFTER: the same stale cursor, read by the verb. The default bound is 500, so git stops
	// one commit past it -- 501 and not 1000 -- and the run opens NO note at all: the bound
	// is hit before anything is read, which is the whole of what the line says.
	parses := bus.NoteParses()
	mark = bus.CommitsWalked()
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40").
		mustCode(t, 0).
		mustContain(t, "stderr", `INBOX WALK bounded commits=500 cursor=`).
		mustContain(t, "stderr", `behind=more-than-500 notes=0 remedy="raise --max-commits or close --before <instant>"`)
	if got := bus.CommitsWalked() - mark; got != defaultMaxCommits+1 {
		t.Fatalf("a bounded read walked %d commits over a cursor %d behind, want %d: the count is not asked with its bound", got, commits, defaultMaxCommits+1)
	}
	if got := bus.NoteParses() - parses; got != 0 {
		t.Fatalf("a bounded read parsed %d notes, want 0: nothing is read past the bound", got)
	}

	// The read the caller asks for by raising the bound: the thousand commits, once, and
	// every one of the two thousand notes they carried, once each. Nothing is walked twice.
	parses = bus.NoteParses()
	mark = bus.CommitsWalked()
	invoke(t, "", advance(checkout, "Ada", "--max-commits", "2000")...).mustCode(t, 0).
		mustContain(t, "stdout", fmt.Sprintf("INBOX OPEN carrying=%d", notes))
	if got := bus.CommitsWalked() - mark; got != commits {
		t.Fatalf("the allowed walk crossed %d commits, want the %d since the cursor", got, commits)
	}
	if got := bus.NoteParses() - parses; got != notes {
		t.Fatalf("the allowed walk parsed %d notes, want the %d it was handed", got, notes)
	}

	// AND THE CARRYING SET IS NEVER RE-READ. The reader now carries two thousand notes; the
	// bus has moved by exactly one commit, the reader's OWN cursor commit that the advance
	// made. So the next read crosses that one commit and opens NO note: a cursor commit
	// carries state files and no note, and an open entry carries its own display line, so a
	// backlog costs nothing until somebody asks for it with --full.
	parses = bus.NoteParses()
	mark = bus.CommitsWalked()
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40").
		mustCode(t, 0).
		mustContain(t, "stdout", fmt.Sprintf("INBOX OPEN carrying=%d", notes))
	if got := bus.CommitsWalked() - mark; got != 1 {
		t.Fatalf("a read at the head walked %d commits, want the 1 the advance's own cursor commit is", got)
	}
	if got := bus.NoteParses() - parses; got != 0 {
		t.Fatalf("a read carrying %d notes parsed %d of them, want 0: the carrying set is re-read only on --full", notes, got)
	}
}

// longBusOfNotes hands a test a checkout standing `commits` commits above the fixture's
// HEAD, carrying `notes` notes from Bo to Ada spread evenly over them, and the commit the
// fixture stood at -- which is where a stale cursor is put. It is one git process, for the
// reason fastImport is: a loop of `git commit` over a thousand commits is a thousand
// subprocesses and the package has a minute to run in.
func longBusOfNotes(t *testing.T, commits, notes int) (checkout, base string) {
	t.Helper()
	checkout, _ = busDir(t)
	base = strings.TrimSpace(gitIn(t, checkout, "rev-parse", "HEAD"))
	// The INDEX is REPLACED rather than appended to, because fast-import writes a whole
	// blob: the fixture's own two lines are carried into it or the notes they name leave the
	// catalogue. It is written once, in the last commit, because it is not a note and no
	// reader's listing depends on which commit carried it.
	index := read(t, checkout, "from-bo/INDEX")
	per := notes / commits
	var b strings.Builder
	prev := base
	for i := range commits {
		msg := fmt.Sprintf("bo: batch %d", i)
		fmt.Fprintf(&b, "commit refs/heads/main\n")
		fmt.Fprintf(&b, "mark :%d\n", i+1)
		fmt.Fprintf(&b, "author Bo <bo@example.com> %d +0000\n", 1757376000+i)
		fmt.Fprintf(&b, "committer Bo <bo@example.com> %d +0000\n", 1757376000+i)
		fmt.Fprintf(&b, "data %d\n%s\n", len(msg), msg)
		fmt.Fprintf(&b, "from %s\n", prev)
		for j := range per {
			n := i*per + j
			id := fmt.Sprintf("bo-%012x", n+0x200000)
			day := n%28 + 1
			path := fmt.Sprintf("from-bo/2026-08-%02dT%02d%02dZ-walk-%s.md", day, n/60%24, n%60, id[len(id)-12:])
			note := fmt.Sprintf(
				"From: Bo\nTo: Ada\nDate: Sat Aug %2d 00:00:00 UTC 2026\nId: %s\nSubject: walk %d\n\nA note in the history.\n",
				day, id, n)
			fmt.Fprintf(&b, "M 100644 inline %s\ndata %d\n%s", path, len(note), note)
			index += fmt.Sprintf("%s\t%s\t2026-08-%02dT00:00:00Z\tAda\t-\n", id, path, day)
		}
		if i == commits-1 {
			fmt.Fprintf(&b, "M 100644 inline from-bo/INDEX\ndata %d\n%s", len(index), index)
		}
		b.WriteString("\n")
		prev = fmt.Sprintf(":%d", i+1)
	}
	b.WriteString("done\n")
	cmd := exec.Command("git", "-C", checkout, "fast-import", "--quiet")
	cmd.Stdin = strings.NewReader(b.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git fast-import %d commits carrying %d notes: %v\n%s", commits, notes, err, out)
	}
	// fast-import moved the branch under the working tree; this is what puts the notes in
	// it, and it leaves the checkout clean, which inbox --advance requires.
	gitIn(t, checkout, "reset", "--hard", "-q", "refs/heads/main")
	gitIn(t, checkout, "push", "-q", "origin", "HEAD:refs/heads/main")
	return checkout, base
}

// longBus hands a test a checkout whose cursor stands `commits` empty commits behind
// HEAD. The fixture's HEAD is the stale cursor's commit and the commits above it carry
// no changes, so the notes and the working tree the fixture built are untouched.
func longBus(t *testing.T, commits int) string {
	t.Helper()
	checkout, _ := busDir(t)
	base := strings.TrimSpace(gitIn(t, checkout, "rev-parse", "HEAD"))
	fastImport(t, checkout, base, commits)
	writeFile(t, checkout, "from-ada/CURSOR", base+" 2026-09-09T12:00:00Z open=0\n")
	return checkout
}

// fastImport writes n empty commits onto refs/heads/main with one git process. A loop of
// `git commit` over a thousand commits is a thousand subprocesses; fast-import is one, and
// the fixture stays hermetic and inside the package's time budget.
func fastImport(t *testing.T, dir, from string, n int) {
	t.Helper()
	var b strings.Builder
	prev := from
	for i := 0; i < n; i++ {
		msg := fmt.Sprintf("empty commit %d", i)
		fmt.Fprintf(&b, "commit refs/heads/main\n")
		fmt.Fprintf(&b, "mark :%d\n", i+1)
		fmt.Fprintf(&b, "author Bus <bus@example.com> %d +0000\n", 1700000000+i)
		fmt.Fprintf(&b, "committer Bus <bus@example.com> %d +0000\n", 1700000000+i)
		fmt.Fprintf(&b, "data %d\n%s\n", len(msg), msg)
		fmt.Fprintf(&b, "from %s\n", prev)
		b.WriteString("\n")
		prev = fmt.Sprintf(":%d", i+1)
	}
	b.WriteString("done\n")
	cmd := exec.Command("git", "-C", dir, "fast-import", "--quiet")
	cmd.Stdin = strings.NewReader(b.String())
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git fast-import %d commits: %v\n%s", n, err, out)
	}
}
