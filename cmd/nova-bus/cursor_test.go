package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

// The property this file exists for, in Dana's words: "Make sure the bus tool is O(n)
// where n is the number of new messages to be read, instead of O(m) where m is all
// messages sent so far. This way it maintains performance over time."
//
// Every test here is about work NOT done, which is the hard kind to assert on: work not
// done leaves no output. So the assertion is a COUNT taken at the one place the work
// happens -- bus.NoteParses, incremented by ParseNote -- and never a wall time. A timing
// assertion on a shared CI runner is a flake, and on a fast enough machine it passes over
// a quadratic implementation.

// advance is the flags that move a reader's cursor, since every test below does it.
//
// It carries --carry-history, because the fixture bus is dated two days before the fixed
// clock and a FIRST advance over notes older than today is refused unless the reader says
// what to do with them (see TestAFirstAdvanceOverOldNotesIsRefused). These tests mean the
// answer "carry them": they are about the cursor and the open list, and every count they
// assert is a count of notes carried. The flag is dropped when the caller draws a
// switch-day line of its own, which is the other answer and cannot be given with this one.
func advance(checkout, who string, extra ...string) []string {
	args := append([]string{
		"inbox", "--bus", checkout, "--as", who, "--receipt-max-words", "40",
		"--advance", "--remote", "origin", "--branch", "main", "--attempts", "3",
	}, extra...)
	for _, a := range extra {
		if a == "--legacy-before" || a == "--carry-history" || strings.HasPrefix(a, "--legacy-before=") {
			return args
		}
	}
	return append(args, "--carry-history")
}

func read(t *testing.T, checkout, path string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(checkout, filepath.FromSlash(path)))
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(raw)
}

// THE COMPLEXITY PROPERTY, proved, in the shape Dana asked for the second time. His first
// requirement gave the cursor; the read was then O(new + open), because every open note was
// re-opened to print its line. "O(new + open) is not great. Can we make it O(new)." This is
// that, asserted: a bus of ten thousand notes, five hundred of them OPEN for this reader,
// one new note -- and the run parses ONE note file. Not 501. One.
//
// It is asserted twice, with and without `--open`, because printing the open list is a
// choice and neither choice may cost a parse. And then once more from the other side: a
// reply that CLOSES an open entry is also one parse, so closing is driven by the new notes
// and not by a walk of what is being carried.
func TestInboxParsesOnlyWhatIsNewSinceTheCursor(t *testing.T) {
	hermetic(t)
	checkout, _ := busDir(t)

	// 10,000 notes, of which 500 are addressed to Ada and will therefore be OPEN for him.
	// The other 9,500 are Bo's business and the point is that Ada's read never touches
	// them either.
	const history = 10000
	const carried = 500
	var index strings.Builder
	for i := range history {
		id := fmt.Sprintf("bo-%012x", i+0x100000)
		path := fmt.Sprintf("from-bo/2026-08-%02dT%02d%02dZ-bulk-%s.md", i%28+1, i/60%24, i%60, id[len(id)-12:])
		to := "Bo"
		if i < carried {
			to = "Ada"
		}
		writeFile(t, checkout, path, fmt.Sprintf(
			"From: Bo\nTo: %s\nDate: Sat Aug %2d 00:00:00 UTC 2026\nId: %s\nSubject: bulk %d\n\nA note in the history.\n",
			to, i%28+1, id, i))
		fmt.Fprintf(&index, "%s\t%s\t2026-08-%02dT00:00:00Z\t%s\t-\n", id, path, i%28+1, to)
	}
	appendFile(t, checkout, "from-bo/INDEX", index.String())
	commitAs(t, checkout, "Bo", "ten thousand notes")

	// The first run has no cursor, so it is a full one and it says so. This is the only
	// full read a reader ever pays for, and it is what writes the open list every later run
	// prints from.
	before := bus.NoteParses()
	r := invoke(t, "", advance(checkout, "Ada")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX SCOPE mode=full cursor=-").
		mustContain(t, "stdout", "INBOX CURSOR commit=")
	if full := bus.NoteParses() - before; full < history {
		t.Fatalf("the full run parsed %d notes over a bus of %d; the fixture is not what this test thinks it is\n%s", full, history, r.stdout)
	}

	// Answer the two notes the FIXTURE leaves open -- by hand, in Ada's own lane, which is
	// what a reply is -- so that what is carried is exactly the 500.
	writeFile(t, checkout, "from-ada/2026-09-09T1200Z-answering-two-aaaaaaaaaaaa.md",
		"From: Ada\nTo: Bo\nDate: Wed Sep  9 12:00:00 UTC 2026\nId: ada-aaaaaaaaaaaa\n"+
			"Re: bo-abcdef012345\nRe: bo-111111111111\nSubject: Both of those\n\nAnswered, both.\n")
	commitAs(t, checkout, "Ada", "ada: answering the fixture's two")
	invoke(t, "", advance(checkout, "Ada")...).mustCode(t, 0).
		mustContain(t, "stdout", fmt.Sprintf("carrying=%d", carried))
	if n := openEntries(t, checkout, "from-ada"); n != carried {
		t.Fatalf("the open list holds %d entries, want %d", n, carried)
	}

	// One new note, addressed to Ada.
	writeFile(t, checkout, "from-bo/2026-09-08T0900Z-one-more-222222222222.md",
		"From: Bo\nTo: Ada\nDate: Tue Sep  8 09:00:00 UTC 2026\nId: bo-222222222222\nSubject: One more\n\nIs the gate on the merge queue?\n")
	appendFile(t, checkout, "from-bo/INDEX",
		"bo-222222222222\tfrom-bo/2026-09-08T0900Z-one-more-222222222222.md\t2026-09-08T09:00:00Z\tAda\t-\n")
	commitAs(t, checkout, "Bo", "one more")

	// THE DEFAULT READ. It writes nothing (no --advance), so the two reads below see the
	// same change set, and it prints one line for the 500 rather than 500 lines.
	quiet := []string{"inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40"}
	before = bus.NoteParses()
	r = invoke(t, "", quiet...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX SCOPE mode=since").
		mustContain(t, "stdout", fmt.Sprintf("INBOX OPEN carrying=%d heard=0", carried+1)).
		mustContain(t, "stdout", fmt.Sprintf("INBOX OK as=Ada carrying=%d open=%d", carried+1, carried+1))
	// ONE. Not one plus the open list, not one plus the history: one file opened and parsed,
	// over a bus of ten thousand and one with five hundred of them open.
	if got := bus.NoteParses() - before; got != 1 {
		t.Fatalf("inbox parsed %d notes for one new note over a bus of %d carrying %d; the read is not O(new)\n%s", got, history+1, carried, r.stdout)
	}
	if strings.Contains(r.stdout, "INBOX NOTE") {
		t.Fatalf("the default read listed the open notes:\n%s", r.stdout)
	}

	// THE SAME READ WITH --open. It prints all 501 entries, every field of them out of the
	// open list, and it still parses ONE.
	before = bus.NoteParses()
	r = invoke(t, "", append(append([]string{}, quiet...), "--open")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX NOTE id=bo-222222222222 from=Bo addr=to at=2026-09-08T09:00:00Z").
		mustContain(t, "stdout", "One more")
	if got := bus.NoteParses() - before; got != 1 {
		t.Fatalf("inbox --open parsed %d notes, want 1: printing the open list must not open a note\n%s", got, r.stdout)
	}
	if n := strings.Count(r.stdout, "INBOX NOTE"); n != carried+1 {
		t.Fatalf("--open listed %d notes, want %d", n, carried+1)
	}

	// Move the cursor over it, then close one entry with a REPLY. Closing is driven by the
	// new note -- my own file in the change set -- so it is one parse as well, and the open
	// list is one shorter.
	invoke(t, "", advance(checkout, "Ada")...).mustCode(t, 0).
		mustContain(t, "stdout", fmt.Sprintf("carrying=%d", carried+1))
	writeFile(t, checkout, "from-ada/2026-09-09T1300Z-yes-the-gate-bbbbbbbbbbbb.md",
		"From: Ada\nTo: Bo\nDate: Wed Sep  9 13:00:00 UTC 2026\nId: ada-bbbbbbbbbbbb\n"+
			"Re: bo-222222222222\nSubject: Yes, the gate\n\nYes, on the merge queue too.\n")
	commitAs(t, checkout, "Ada", "ada: yes, the gate")

	before = bus.NoteParses()
	r = invoke(t, "", advance(checkout, "Ada")...).mustCode(t, 0).
		mustContain(t, "stdout", fmt.Sprintf("INBOX OPEN carrying=%d heard=0", carried)).
		mustContain(t, "stdout", fmt.Sprintf("carrying=%d pushed=true", carried))
	if got := bus.NoteParses() - before; got != 1 {
		t.Fatalf("closing an open entry parsed %d notes, want the 1 reply that closed it\n%s", got, r.stdout)
	}
	if strings.Contains(read(t, checkout, "from-ada/OPEN"), "bo-222222222222") {
		t.Fatalf("the answered note is still on the open list")
	}
	if n := openEntries(t, checkout, "from-ada"); n != carried {
		t.Fatalf("the open list holds %d entries after one closed, want %d", n, carried)
	}
}

// commitAs commits everything in the checkout under a roster name's identity and pushes it,
// which is what a note arriving on the bus looks like from a test's side.
func commitAs(t *testing.T, checkout, who, message string) {
	t.Helper()
	gitIn(t, checkout, "add", "-A")
	gitIn(t, checkout, "-c", "user.name="+who, "-c", "user.email="+strings.ToLower(who)+"@example.com",
		"commit", "-q", "-m", message)
	gitIn(t, checkout, "push", "-q", "origin", "HEAD:refs/heads/main")
}

// openEntries is how many entries a lane's OPEN list holds, which is its lines less the
// version header.
func openEntries(t *testing.T, checkout, lane string) int {
	t.Helper()
	raw := read(t, checkout, lane+"/OPEN")
	if !strings.HasPrefix(raw, bus.OpenHeader+"\n") {
		t.Fatalf("%s/OPEN does not begin with %q", lane, bus.OpenHeader)
	}
	return len(strings.Split(strings.TrimRight(raw, "\n"), "\n")) - 1
}

// The OPEN list is the reason the cursor is allowed to move past an unanswered note. Two
// runs, with the note arriving before the first and being answered after the second: it is
// listed both times and gone the third.
func TestOpenListSurvivesTheCursorMovingPastIt(t *testing.T) {
	hermetic(t)
	checkout, _ := busDir(t)

	invoke(t, "", advance(checkout, "Ada")...).mustCode(t, 0)
	// Both of Bo's notes are now carried, and the cursor is at HEAD. The entry carries
	// the note's whole display line, which is what a later run prints it from.
	got := read(t, checkout, "from-ada/OPEN")
	if !strings.Contains(got, "bo-abcdef012345\tnote\t-\tBo\tto\t2026-09-07T00:01:00Z\tfrom-bo/2026-09-07T0001Z-a-question-abcdef012345.md\tA question about the gate") {
		t.Fatalf("the question was not carried into OPEN with its line:\n%s", got)
	}
	cursorOne := read(t, checkout, "from-ada/CURSOR")

	// A second run, with NOTHING new on the bus. The cursor has already moved past the
	// note, so without the OPEN list this listing would be empty -- which is the failure
	// the list exists to stop.
	r := invoke(t, "", advance(checkout, "Ada", "--open")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX SCOPE mode=since").
		mustContain(t, "stdout", "INBOX NOTE id=bo-abcdef012345 from=Bo addr=to at=2026-09-07T00:01:00Z").
		mustContain(t, "stdout", "A question about the gate").
		mustContain(t, "stdout", "INBOX OK as=Ada carrying=2 open=2")
	if strings.Contains(r.stdout, "changed=0") && !strings.Contains(r.stdout, "carrying=2") {
		t.Fatalf("the second run lost what the first was carrying:\n%s", r.stdout)
	}
	if read(t, checkout, "from-ada/CURSOR") == cursorOne {
		t.Fatalf("the second run did not advance the cursor past its own first commit")
	}

	// Now answer it. The reply names the note by id, so the third run drops it from OPEN
	// and from the listing.
	invoke(t, draft, "send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3").mustCode(t, 0)
	r = invoke(t, "", advance(checkout, "Ada", "--open")...).mustCode(t, 0)
	if strings.Contains(r.stdout, "bo-abcdef012345") {
		t.Fatalf("an answered note is still carried:\n%s", r.stdout)
	}
	if got := read(t, checkout, "from-ada/OPEN"); strings.Contains(got, "bo-abcdef012345") {
		t.Fatalf("an answered note is still in OPEN:\n%s", got)
	}
}

// A receipt survives the cursor too: heard is not answered, and a note I receipted three
// runs ago is still listed as HEARD rather than quietly dropped.
//
// WHERE THE FLAG LIVES is the whole of what changed with OPEN v2. It used to be recomputed
// by reading RECEIPTS whole on every run, for a fact that changes about once a day. Now the
// receipt reaches ONE run -- as my own RECEIPTS file in that run's change set -- and what it
// writes is the flag in OPEN, which every later run reads for nothing.
func TestHeardSurvivesTheCursor(t *testing.T) {
	hermetic(t)
	checkout, _ := busDir(t)
	invoke(t, "", advance(checkout, "Ada")...).mustCode(t, 0)
	invoke(t, "", "receipt", "--bus", checkout, "--as", "Ada", "--note", "bo-abcdef012345",
		"--remote", "origin", "--branch", "main", "--attempts", "3").mustCode(t, 0)
	invoke(t, "", advance(checkout, "Ada", "--open")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX HEARD id=bo-abcdef012345")
	// The flag is IN the open list now, which is why the next run needs neither the note nor
	// RECEIPTS to say HEARD.
	if got := read(t, checkout, "from-ada/OPEN"); !strings.Contains(got, "bo-abcdef012345\tnote\theard\t") {
		t.Fatalf("the heard flag was not written into the open list:\n%s", got)
	}
	// And again, with the receipt now far behind the cursor -- and with no note opened at
	// all, which is the count this asserts.
	before := bus.NoteParses()
	invoke(t, "", advance(checkout, "Ada", "--open")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX HEARD id=bo-abcdef012345").
		mustContain(t, "stdout", "heard=1")
	if got := bus.NoteParses() - before; got != 0 {
		t.Fatalf("a run over an unchanged bus parsed %d notes, want 0: heard is read from the open list", got)
	}
	// The default read says the same thing in one line, and RECEIPTS is still the durable
	// record underneath it.
	invoke(t, "", advance(checkout, "Ada")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX OPEN carrying=2 heard=1")
	if got := read(t, checkout, "from-ada/RECEIPTS"); !strings.Contains(got, "bo-abcdef012345") {
		t.Fatalf("RECEIPTS is not the durable record any more:\n%s", got)
	}
	// A --full read rebuilds the flag from RECEIPTS rather than carrying it, which is what
	// makes the file the record and the flag the cache.
	writeFile(t, checkout, "from-ada/OPEN", bus.OpenHeader+"\n")
	invoke(t, "", advance(checkout, "Ada", "--full", "--open")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX HEARD id=bo-abcdef012345").
		mustContain(t, "stdout", "heard=1")
}

// A cursor that is no longer on this history is a REFUSAL naming --full, never a best
// effort. A reader told "nothing new" by a broken cursor has been lied to in exactly the
// way this tool exists to stop.
func TestACursorThatIsNotAnAncestorIsRefused(t *testing.T) {
	hermetic(t)
	checkout, _ := busDir(t)
	writeFile(t, checkout, "from-ada/CURSOR", "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef 2026-09-09T12:00:00Z\n")
	r := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40").
		mustCode(t, 1).
		mustContain(t, "stderr", "INBOX REFUSED: ").
		mustContain(t, "stderr", "is not an ancestor of HEAD").
		mustContain(t, "stderr", "--full")
	if n := strings.Count(strings.TrimRight(r.stderr, "\n"), "\n"); n != 0 {
		t.Fatalf("the refusal is %d lines, want one:\n%q", n+1, r.stderr)
	}
	// --full is the fallback it names, and it works.
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--full").
		mustCode(t, 0).mustContain(t, "stdout", "INBOX SCOPE mode=full")
	// A cursor that is not a commit at all is refused where it is read, before it can
	// become a git argument.
	writeFile(t, checkout, "from-ada/CURSOR", "--upload-pack=id 2026-09-09T12:00:00Z\n")
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40").
		mustCode(t, 1).mustContain(t, "stderr", "is not a commit")
	// check reads the same cursor and refuses it the same way.
	invoke(t, "", "check", "--bus", checkout, "--as", "Ada").
		mustCode(t, 1).mustContain(t, "stderr", "BUS REFUSED: ")
}

// A --bus that is not the ROOT of its repository is refused by every verb that reads
// git, and this is the blocker the second read found. It used to run: `rev-parse
// --is-inside-work-tree` is true anywhere under a repository, `git diff --name-only`
// reports `bus/from-bo/x.md` from the repository root, ChangedSince keeps only paths
// beginning `from-`, and so `inbox --since` and `check --as` printed changed=0 and exited 0
// over notes nobody had read. Exit 2, because a --bus the tool will not work over is a
// bad invocation and not a bus that failed.
func TestABusBelowTheRepositoryRootIsRefused(t *testing.T) {
	hermetic(t)
	bare := filepath.Join(t.TempDir(), "bus.git")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, bare, "init", "--bare", "--quiet", "--initial-branch=main")
	checkout := filepath.Join(t.TempDir(), "checkout")
	gitIn(t, filepath.Dir(checkout), "clone", "--quiet", bare, checkout)
	gitIn(t, checkout, "checkout", "-q", "-B", "main")
	// The bus is one directory down, which is exactly how a bus kept inside a bigger
	// repository -- a docs tree, a monorepo -- would be pointed at.
	nested := filepath.Join(checkout, "bus")
	writeFile(t, nested, "participants.json", rosterJSON)
	writeFile(t, nested, "from-bo/2026-09-07T0001Z-a-question-abcdef012345.md",
		"From: Bo\nTo: Ada\nDate: Mon Sep  7 00:01:00 UTC 2026\nId: bo-abcdef012345\nSubject: A question about the gate\n\nShould the gate run on the merge queue too?\n")
	writeFile(t, nested, "from-bo/INDEX",
		"bo-abcdef012345\tfrom-bo/2026-09-07T0001Z-a-question-abcdef012345.md\t2026-09-07T00:01:00Z\tAda\t-\n")
	gitIn(t, checkout, "add", "-A")
	gitIn(t, checkout, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", "a bus one directory down")
	gitIn(t, checkout, "push", "-q", "origin", "HEAD:refs/heads/main")

	for _, args := range [][]string{
		{"inbox", "--bus", nested, "--as", "Ada", "--receipt-max-words", "40"},
		{"check", "--bus", nested, "--as", "Ada"},
		{"check", "--bus", nested, "--since", "HEAD"},
		{"receipt", "--bus", nested, "--as", "Ada", "--note", "bo-abcdef012345",
			"--remote", "origin", "--branch", "main", "--attempts", "3"},
	} {
		invoke(t, "", args...).mustCode(t, 2).
			mustContain(t, "stderr", "is not its root").
			mustContain(t, "stderr", "empty change set over unread notes")
	}
	invoke(t, draft, "send", "--bus", nested, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 2).mustContain(t, "stderr", "is not its root")
	// The verbs that need no git still work over it, so the refusal is exactly as wide as
	// the failure: a bus below a repository root is readable, it just cannot be read
	// INCREMENTALLY, and the tool says which.
	invoke(t, "", "check", "--bus", nested, "--full").mustCode(t, 0).mustContain(t, "stdout", "BUS OK")
	invoke(t, "", "inbox", "--bus", nested, "--as", "Ada", "--receipt-max-words", "40", "--full").
		mustCode(t, 0).mustContain(t, "stdout", "INBOX NOTE id=bo-abcdef012345")
}

// A cursor advances past a note because OPEN remembers it. Delete OPEN alone -- the cursor
// stays valid, and an empty OPEN list is REMOVED rather than left zero-length, so absent and
// nothing-open look the same on disk -- and the next run would have said open=0 over notes
// still owed. The cursor carries the count it was written with, so the two states are
// different and this is a refusal naming --full --advance.
func TestACursorWhoseOpenListWentMissingIsRefused(t *testing.T) {
	hermetic(t)
	checkout, _ := busDir(t)
	invoke(t, "", advance(checkout, "Ada")...).mustCode(t, 0).mustContain(t, "stdout", "carrying=2")
	if line := read(t, checkout, "from-ada/CURSOR"); !strings.Contains(line, "open=2") {
		t.Fatalf("the cursor does not record what it was carrying: %q", line)
	}
	if err := os.Remove(filepath.Join(checkout, "from-ada", "OPEN")); err != nil {
		t.Fatal(err)
	}
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40").
		mustCode(t, 1).
		mustContain(t, "stderr", "INBOX REFUSED: ").
		mustContain(t, "stderr", "was carrying 2 notes").
		mustContain(t, "stderr", "--full --advance")
	// And the way through is the one it names: a full read rebuilds the open list from the
	// whole bus and both notes come back.
	invoke(t, "", advance(checkout, "Ada", "--full")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX SCOPE mode=full").
		mustContain(t, "stdout", "INBOX OK as=Ada carrying=2 open=2").
		mustContain(t, "stdout", "carrying=2")
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40").
		mustCode(t, 0).mustContain(t, "stdout", "INBOX SCOPE mode=since")
	// A reader with genuinely nothing open has no OPEN file either, and that is NOT the
	// refused state: the count in their cursor is zero and nothing is compared.
	invoke(t, draft, "send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3").mustCode(t, 0)
	invoke(t, "From: Ada\nTo: Bo\nRe: bo-111111111111\nSubject: That one too\n\nAnswered.\n",
		"send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3").mustCode(t, 0)
	invoke(t, "", advance(checkout, "Ada")...).mustCode(t, 0).mustContain(t, "stdout", "carrying=0")
	if _, err := os.Stat(filepath.Join(checkout, "from-ada", "OPEN")); !os.IsNotExist(err) {
		t.Fatalf("an empty OPEN list was left on disk, so absent no longer means nothing open: %v", err)
	}
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40").
		mustCode(t, 0).mustContain(t, "stdout", "INBOX OK as=Ada carrying=0 open=0")
}

// The diff is `<cursor>..HEAD`, which is TWO dots and therefore a tree-to-tree comparison,
// not a walk of the commits between them. That is the whole reason a note that arrived on a
// side branch and came in through a MERGE is seen: git compares the two trees and the note
// is in one of them, whatever route it took. Three dots would have taken the merge base and
// missed everything on the branch that was merged.
func TestANoteThatArrivedThroughAMergeIsSeen(t *testing.T) {
	hermetic(t)
	checkout, _ := busDir(t)
	invoke(t, "", advance(checkout, "Ada")...).mustCode(t, 0)

	// Bo writes on a side branch while main moves on underneath her.
	gitIn(t, checkout, "checkout", "-q", "-b", "bo-side")
	writeFile(t, checkout, "from-bo/2026-09-08T1000Z-on-a-branch-666666666666.md",
		"From: Bo\nTo: Ada\nDate: Tue Sep  8 10:00:00 UTC 2026\nId: bo-666666666666\nSubject: On a branch\n\nDoes the runner matrix key need quoting?\n")
	appendFile(t, checkout, "from-bo/INDEX",
		"bo-666666666666\tfrom-bo/2026-09-08T1000Z-on-a-branch-666666666666.md\t2026-09-08T10:00:00Z\tAda\t-\n")
	gitIn(t, checkout, "add", "-A")
	gitIn(t, checkout, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", "bo: on a branch")
	gitIn(t, checkout, "checkout", "-q", "main")
	// Meanwhile on main, a note written by hand in a browser -- so it touches no INDEX, and
	// this fixture is about the merge rather than about the catalogue conflict that
	// TestTwoBenchesOfOneLaneConflictOnTheCatalogue pins.
	writeFile(t, checkout, "from-bo/2026-09-08T1100Z-meanwhile-777777777777.md",
		"From: Bo\nTo: Ada\nDate: Tue Sep  8 11:00:00 UTC 2026\nId: bo-777777777777\nSubject: Meanwhile\n\nAnd the gate on the merge queue?\n")
	gitIn(t, checkout, "add", "-A")
	gitIn(t, checkout, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", "bo: meanwhile")
	// A real merge commit, with two parents, which is the fixture this test is for.
	gitIn(t, checkout, "-c", "user.name=Bo", "-c", "user.email=bo@example.com",
		"merge", "-q", "--no-ff", "-m", "merge bo's branch", "bo-side")
	if parents := strings.Fields(strings.TrimSpace(gitIn(t, checkout, "rev-list", "--parents", "-n", "1", "HEAD"))); len(parents) != 3 {
		t.Fatalf("HEAD is not a merge commit: %v", parents)
	}
	gitIn(t, checkout, "push", "-q", "origin", "HEAD:refs/heads/main")

	invoke(t, "", advance(checkout, "Ada", "--open")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX SCOPE mode=since").
		mustContain(t, "stdout", "INBOX NOTE id=bo-666666666666").
		mustContain(t, "stdout", "INBOX NOTE id=bo-777777777777")
	// The run after it crosses the merge once: nothing new arrives a second time. Its
	// changed= is 2 and not 0, and that is the grammar rather than a leak -- changed=
	// counts the PATHS the diff named inside lanes, which includes this reader's own
	// CURSOR and OPEN from the advance just made. Neither is a note, and no note is
	// parsed for them.
	invoke(t, "", advance(checkout, "Ada")...).mustCode(t, 0).
		mustContain(t, "stdout", "changed=2 carrying=4").
		mustContain(t, "stdout", "INBOX OK as=Ada carrying=4 open=4 notes=3 receipts=1")
}

// send appends to its lane's catalogue in the SAME commit as the note, and check --full
// agrees with it in both directions.
func TestSendAppendsToTheIndexAndCheckAgrees(t *testing.T) {
	hermetic(t)
	checkout, bare := busDir(t)
	r := invoke(t, draft, "send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 0)
	id := field(t, r.stdout, "id=")
	path := field(t, r.stdout, "path=")

	line := read(t, checkout, "from-ada/INDEX")
	fields := strings.Split(strings.TrimRight(line, "\n"), "\t")
	if len(fields) != 5 {
		t.Fatalf("the index line is %d fields, want 5: %q", len(fields), line)
	}
	if fields[0] != id || fields[1] != path {
		t.Fatalf("the index line names %q at %q; the note is %q at %q", fields[0], fields[1], id, path)
	}
	if fields[3] != "Bo;Dana" {
		t.Fatalf("the index line's recipients are %q, want the resolved To then Cc", fields[3])
	}
	if fields[4] != "bo-abcdef012345" {
		t.Fatalf("the index line's Re is %q", fields[4])
	}
	// One commit, both files: a catalogue that could lag the notes by a commit is one a
	// reader between the two would resolve wrongly.
	files := gitIn(t, bare, "show", "--name-only", "--format=", "main")
	if !strings.Contains(files, "from-ada/INDEX") || !strings.Contains(files, path) {
		t.Fatalf("the note and its index line are not in one commit:\n%s", files)
	}
	invoke(t, "", "check", "--bus", checkout, "--full").mustCode(t, 0).
		mustContain(t, "stdout", "BUS OK").mustContain(t, "stdout", "warn=0")
}

// A catalogue line that DISAGREES with a note fails; a note with no catalogue line only
// warns, and --rebuild-index writes it.
func TestCheckFullAgainstTheIndex(t *testing.T) {
	hermetic(t)
	checkout, _ := busDir(t)

	// A note written by hand, in a browser, the way this bus's whole form allows. It has
	// an id and no catalogue line: a WARN, at any date, because the notes are the record
	// and the catalogue is a cache.
	writeFile(t, checkout, "from-bo/2026-09-08T0300Z-by-hand-333333333333.md",
		"From: Bo\nTo: Ada\nDate: Tue Sep  8 03:00:00 UTC 2026\nId: bo-333333333333\nSubject: By hand\n\nWritten in a browser, with no tool in sight.\n")
	// A WARN goes to STDOUT: the grammar puts only FAIL lines and refusals on stderr, and a
	// warning is a finding a passing run reported. On stderr it made every forgiving run
	// look like a failing one to anything reading the two streams apart.
	r := invoke(t, "", "check", "--bus", checkout, "--full").mustCode(t, 0).
		mustContain(t, "stdout", "BUS WARN from-bo/2026-09-08T0300Z-by-hand-333333333333.md").
		mustContain(t, "stdout", "--rebuild-index").
		mustContain(t, "stdout", "warn=1")
	if strings.Contains(r.stderr, "BUS WARN") {
		t.Fatalf("a warning reached stderr, where only failures and refusals go:\n%s", r.stderr)
	}

	// --rebuild-index writes it, and the warning goes.
	invoke(t, "", "check", "--bus", checkout, "--full", "--rebuild-index").mustCode(t, 0).
		mustContain(t, "stdout", "BUS INDEX lane=from-bo notes=3").
		mustContain(t, "stdout", "BUS INDEX lane=from-ada notes=0").
		mustContain(t, "stdout", "warn=0")

	// A catalogue line naming a note that is not there is a FAILURE, not a warning: that
	// one could resolve a thread to the wrong note.
	appendFile(t, checkout, "from-bo/INDEX",
		"bo-444444444444\tfrom-bo/never-written.md\t2026-09-08T04:00:00Z\tAda\t-\n")
	invoke(t, "", "check", "--bus", checkout, "--full").mustCode(t, 1).
		mustContain(t, "stderr", "BUS FAIL from-bo/INDEX:4").
		mustContain(t, "stderr", "which is not a note on this bus")

	// And --rebuild-index is refused without --full, because it writes every lane's
	// catalogue from every note in it.
	invoke(t, "", "check", "--bus", checkout, "--rebuild-index", "--as", "Ada").
		mustCode(t, 2).mustContain(t, "stderr", "needs --full")
}

// check --since checks the change set and nothing else, and finds in it what --full would
// find. It is the same per-note code with the lookups answered from the catalogue.
func TestCheckSinceChecksOnlyWhatChanged(t *testing.T) {
	hermetic(t)
	checkout, _ := busDir(t)
	base := strings.TrimSpace(gitIn(t, checkout, "rev-parse", "HEAD"))

	// Something already on the bus that --full would fail on, committed BEFORE the
	// baseline: --since must not see it, and --full must.
	writeFile(t, checkout, "from-bo/older-stranger.md", "From: Bo\nTo: Boe\nSubject: s\n\nbody\n")
	gitIn(t, checkout, "add", "-A")
	gitIn(t, checkout, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", "older")
	after := strings.TrimSpace(gitIn(t, checkout, "rev-parse", "HEAD"))

	invoke(t, "", "check", "--bus", checkout, "--since", after).mustCode(t, 0).
		mustContain(t, "stdout", "BUS SCOPE mode=since").
		mustContain(t, "stdout", "changed=0")
	invoke(t, "", "check", "--bus", checkout, "--since", base).mustCode(t, 1).
		mustContain(t, "stderr", "BUS FAIL from-bo/older-stranger.md").
		mustContain(t, "stderr", `"Boe"`)
	invoke(t, "", "check", "--bus", checkout, "--full").mustCode(t, 1).
		mustContain(t, "stderr", "BUS FAIL from-bo/older-stranger.md")
	// A revision this checkout does not hold is a bad invocation, not a bus that failed.
	invoke(t, "", "check", "--bus", checkout, "--since", "nosuchref").
		mustCode(t, 2).mustContain(t, "stderr", "names no commit")
	// And a --since that would be an option to git never reaches git.
	invoke(t, "", "check", "--bus", checkout, "--since", "--upload-pack=id").
		mustCode(t, 2).mustContain(t, "stderr", "nova-bus check:")
}

// check refuses to guess a baseline, exactly the way every other flag here is refused.
func TestCheckRefusesToGuessItsBaseline(t *testing.T) {
	checkout, _ := busDir(t)
	invoke(t, "", "check", "--bus", checkout).mustCode(t, 2).
		mustContain(t, "stderr", "give one of --full, --as <name> or --since <commit>").
		mustContain(t, "stderr", "refusing to guess")
	// A reader with no cursor yet has no baseline, so --as falls back to a full run and
	// SAYS so rather than reporting an empty change set as a clean bus.
	invoke(t, "", "check", "--bus", checkout, "--as", "Ada").mustCode(t, 0).
		mustContain(t, "stdout", "BUS SCOPE mode=full cursor=-")
}

// inbox without --advance writes NOTHING. A report that edits the bus without being
// asked is the surprise this repo does not do.
func TestInboxWithoutAdvanceWritesNothing(t *testing.T) {
	hermetic(t)
	checkout, _ := busDir(t)
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40").mustCode(t, 0)
	if _, err := os.Stat(filepath.Join(checkout, "from-ada")); err == nil {
		t.Fatalf("a plain inbox created the reader's lane")
	}
	if out := gitIn(t, checkout, "status", "--porcelain"); strings.TrimSpace(out) != "" {
		t.Fatalf("a plain inbox left the checkout dirty:\n%s", out)
	}
	// --advance without the flags it needs to push is refused by name.
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--advance").
		mustCode(t, 2).mustContain(t, "stderr", "needs --remote and --branch")
}

// The cursor is on the bus, not on the bench: it is committed under the reader's own
// identity from the roster and pushed like a receipt.
func TestTheCursorIsPushedLikeAReceipt(t *testing.T) {
	hermetic(t)
	checkout, bare := busDir(t)
	invoke(t, "", advance(checkout, "Ada")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX CURSOR commit=").
		mustContain(t, "stdout", "pushed=true attempts=1")
	files := gitIn(t, bare, "ls-tree", "-r", "--name-only", "main")
	for _, want := range []string{"from-ada/CURSOR", "from-ada/OPEN"} {
		if !strings.Contains(files, want) {
			t.Fatalf("%s is not on the remote:\n%s", want, files)
		}
	}
	if who := strings.TrimSpace(gitIn(t, bare, "log", "-1", "--format=%an <%ae>", "main")); who != "Ada <ada@example.com>" {
		t.Fatalf("the cursor was committed as %q, not the roster's identity for Ada", who)
	}
	// --no-push commits it and says the cursor is not on the bus.
	writeFile(t, checkout, "from-bo/2026-09-08T0500Z-another-555555555555.md",
		"From: Bo\nTo: Ada\nDate: Tue Sep  8 05:00:00 UTC 2026\nId: bo-555555555555\nSubject: Another\n\nWhat about the Windows runner?\n")
	gitIn(t, checkout, "add", "-A")
	gitIn(t, checkout, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", "another")
	gitIn(t, checkout, "push", "-q", "origin", "HEAD:refs/heads/main")
	invoke(t, "", advance(checkout, "Ada", "--no-push")...).mustCode(t, 0).
		mustContain(t, "stdout", "pushed=false")
}

// The lane's state files are not notes and not strays. check --full says so, and inbox
// does not try to read one as a note.
func TestLaneStateFilesAreNotStrays(t *testing.T) {
	hermetic(t)
	checkout, _ := busDir(t)
	invoke(t, "", advance(checkout, "Ada")...).mustCode(t, 0)
	invoke(t, "", "check", "--bus", checkout, "--full").mustCode(t, 0).mustContain(t, "stdout", "BUS OK")
	// A file that is none of them still is one.
	writeFile(t, checkout, "from-ada/notes.txt", "a scratch file\n")
	invoke(t, "", "check", "--bus", checkout, "--full").mustCode(t, 1).
		mustContain(t, "stderr", "BUS FAIL from-ada/notes.txt").
		mustContain(t, "stderr", "RECEIPTS, CURSOR, OPEN, INDEX")
	// A malformed state file is a finding, not a crash: a reader would otherwise refuse on
	// their next run with nothing on the bus saying why.
	if err := os.Remove(filepath.Join(checkout, "from-ada", "notes.txt")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, checkout, "from-ada/OPEN", bus.OpenHeader+"\nno-path-here\n")
	invoke(t, "", "check", "--bus", checkout, "--full").mustCode(t, 1).
		mustContain(t, "stderr", "BUS FAIL from-ada/OPEN").
		mustContain(t, "stderr", "an open entry is 8 tab-separated fields")
}

// A LANE'S README IS NOT A NOTE, and this is the finding a review named. It ends in
// `.md` and sits in a lane, so the lane walk parsed it, failed, told every reader on the
// bus `INBOX UNREADABLE` about it forever, and failed `check` at every date -- the one
// finding the legacy tolerance could not forgive, because a README cannot say when it was
// written and is not a note whatever it says.
func TestALanesReadmeIsNotANote(t *testing.T) {
	hermetic(t)
	checkout, _ := busDir(t)
	writeFile(t, checkout, "from-bo/README.md",
		"# Bo's lane\n\nWhat I write about here, and how to reach me faster than the bus.\n")
	commitAs(t, checkout, "Bo", "bo: a README for the lane")

	// check passes over it, in both modes: not a note, not a stray.
	invoke(t, "", "check", "--bus", checkout, "--full").mustCode(t, 0).mustContain(t, "stdout", "BUS OK")
	invoke(t, "", "check", "--bus", checkout, "--since", "HEAD~1").mustCode(t, 0).
		mustContain(t, "stdout", "BUS SCOPE mode=since")
	// And no reader is told it cannot be read, on either read.
	for _, args := range [][]string{
		{"inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--full"},
		advance(checkout, "Ada"),
	} {
		r := invoke(t, "", args...).mustCode(t, 0).mustContain(t, "stdout", "unreadable=0")
		if strings.Contains(r.stdout, "README.md") {
			t.Fatalf("a lane's README reached a reader's listing:\n%s", r.stdout)
		}
	}
	// The tolerance is ONE name and is not a licence: a second document in a lane is read as
	// the note it is not, and fails, exactly as it did before. (A `readme.md` in another case
	// is a stray too, and is not asserted here because half the machines this runs on cannot
	// hold both spellings in one directory.)
	writeFile(t, checkout, "from-bo/NOTES.md", "# not the one allowed name\n\nprose where a header goes.\n")
	invoke(t, "", "check", "--bus", checkout, "--full").mustCode(t, 1).
		mustContain(t, "stderr", "BUS FAIL from-bo/NOTES.md")
}

// AN OPEN LIST WRITTEN BEFORE v2 IS REFUSED, and the refusal names the read that repairs
// it. A v1 entry is `<id or -> <path>`; read as a v2 line it is one field, and a run would
// print a note nobody sent and carry it forever. The cursor cannot catch that -- a v1 OPEN
// beside a counted cursor looks exactly like a healthy reader -- so the file says its own
// version and this is what happens when it does not.
func TestAnOpenListFromBeforeV2IsRefusedAndFullAdvanceRepairsIt(t *testing.T) {
	hermetic(t)
	checkout, _ := busDir(t)
	invoke(t, "", advance(checkout, "Ada")...).mustCode(t, 0).mustContain(t, "stdout", "carrying=2")

	// The shape the previous version wrote, under the cursor it wrote beside.
	writeFile(t, checkout, "from-ada/OPEN",
		"bo-abcdef012345 from-bo/2026-09-07T0001Z-a-question-abcdef012345.md\n"+
			"bo-111111111111 from-bo/2026-09-07T0002Z-heard-111111111111.md\n")
	r := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40").
		mustCode(t, 1).
		mustContain(t, "stderr", "INBOX REFUSED: ").
		mustContain(t, "stderr", bus.OpenHeader).
		mustContain(t, "stderr", "--full --advance")
	if n := strings.Count(strings.TrimRight(r.stderr, "\n"), "\n"); n != 0 {
		t.Fatalf("the refusal is %d lines, want one:\n%q", n+1, r.stderr)
	}
	// check says the same thing about the same file, so a bus carrying one is not a
	// silence that only its own reader ever meets.
	invoke(t, "", "check", "--bus", checkout, "--full").mustCode(t, 1).
		mustContain(t, "stderr", "BUS FAIL from-ada/OPEN")

	// And the way through is the one it names: nothing is lost, and the list comes back in
	// the new shape with both notes on it.
	invoke(t, "", advance(checkout, "Ada", "--full")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX SCOPE mode=full").
		mustContain(t, "stdout", "INBOX OK as=Ada carrying=2 open=2")
	if got := read(t, checkout, "from-ada/OPEN"); !strings.HasPrefix(got, bus.OpenHeader+"\n") {
		t.Fatalf("--full --advance did not write a v2 open list:\n%s", got)
	}
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40").
		mustCode(t, 0).mustContain(t, "stdout", "INBOX OPEN carrying=2")
}

// A file this tool cannot read is carried on the open list until it parses or is receipted,
// so an incremental run keeps naming it. It used to be named once by a --full read and left
// off the list, which meant no later run ever mentioned it again: a note somebody wrote, on
// the bus, that its reader is told about once and then never.
func TestAnUnreadableFileIsCarriedAcrossRuns(t *testing.T) {
	hermetic(t)
	checkout, _ := busDir(t)
	writeFile(t, checkout, "from-bo/2026-09-07T0009Z-prose.md",
		"Ada, the checkpoint is pushed and the suite passed: zero divergence.\n\nMore prose.\n")
	commitAs(t, checkout, "Bo", "bo: a file that will not parse")

	invoke(t, "", advance(checkout, "Ada")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX UNREADABLE path=from-bo/2026-09-07T0009Z-prose.md: ").
		mustContain(t, "stdout", "unreadable=1")
	if got := read(t, checkout, "from-ada/OPEN"); !strings.Contains(got, "-\tunreadable\t-\t-\t-\t-\tfrom-bo/2026-09-07T0009Z-prose.md\t-") {
		t.Fatalf("the unreadable file was not carried on the open list:\n%s", got)
	}
	// The next run, with nothing new at all, still names it. That is the whole point.
	invoke(t, "", advance(checkout, "Ada")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX UNREADABLE path=from-bo/2026-09-07T0009Z-prose.md: ").
		mustContain(t, "stdout", "unreadable=1")

	// Receipting it is one way it leaves the list: a reader saying "I have seen this file"
	// about something with no id to answer.
	invoke(t, "", "receipt", "--bus", checkout, "--as", "Ada",
		"--note", "from-bo/2026-09-07T0009Z-prose.md",
		"--remote", "origin", "--branch", "main", "--attempts", "3").mustCode(t, 0)
	r := invoke(t, "", advance(checkout, "Ada")...).mustCode(t, 0).
		mustContain(t, "stdout", "unreadable=0")
	if strings.Contains(r.stdout, "2026-09-07T0009Z-prose.md") {
		t.Fatalf("a receipted unreadable file is still named:\n%s", r.stdout)
	}

	// The other way is that somebody FIXES it: it stops being unreadable and becomes an
	// ordinary open note, with the line it should have had.
	writeFile(t, checkout, "from-bo/2026-09-08T0009Z-now-a-note-333333333333.md",
		"Ada, prose where a header goes.\n\nMore prose.\n")
	commitAs(t, checkout, "Bo", "bo: another one")
	invoke(t, "", advance(checkout, "Ada")...).mustCode(t, 0).mustContain(t, "stdout", "unreadable=1")
	writeFile(t, checkout, "from-bo/2026-09-08T0009Z-now-a-note-333333333333.md",
		"From: Bo\nTo: Ada\nDate: Tue Sep  8 00:09:00 UTC 2026\nId: bo-333333333333\nSubject: Now it parses\n\nAnd here is the question.\n")
	commitAs(t, checkout, "Bo", "bo: fixed the header")
	invoke(t, "", advance(checkout, "Ada", "--open")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX NOTE id=bo-333333333333 from=Bo addr=to").
		mustContain(t, "stdout", "unreadable=0")
}

// field pulls one key=value out of an event line.
func field(t *testing.T, out, key string) string {
	t.Helper()
	i := strings.Index(out, key)
	if i < 0 {
		t.Fatalf("no %s in %q", key, out)
	}
	rest := out[i+len(key):]
	if j := strings.IndexAny(rest, " \n"); j >= 0 {
		return rest[:j]
	}
	return rest
}

func appendFile(t *testing.T, root, path, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(path))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(content); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

// The example bus in testdata is the shape a person and an AI are both pointed at from
// the README, so it is held to the tool rather than left to drift: it passes check --full
// clean, and its listings are the ones the README prints.
//
// It is copied out and given a repository OF ITS OWN, which is the honest shape and is what
// the example's own README now tells a reader to do. In the tree it ships in, it is a
// directory inside a repository about tools, and every verb that reads git refuses a
// --bus that is not its repository's root.
func TestTheExampleBusInTestdataIsWhatTheREADMESays(t *testing.T) {
	hermetic(t)
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "example-bus"), root)
	gitIn(t, root, "init", "--quiet", "-b", "main")
	gitIn(t, root, "add", "-A")
	gitIn(t, root, "-c", "user.name=Ada", "-c", "user.email=ada@example.com", "commit", "-q", "-m", "the bus")

	invoke(t, "", "check", "--bus", root, "--full").mustCode(t, 0).
		mustContain(t, "stdout", "BUS OK notes=4 lanes=2 receipts=1 participants=3 warn=0")
	invoke(t, "", "names", "--bus", root).mustCode(t, 0).
		mustContain(t, "stdout", `NAMES NAME name="Dana" lane=-`).
		mustContain(t, "stdout", "NAMES OK participants=3 groups=1 senders=2")
	// Ada's listing: the thread is answered and gone, the Windows finding was receipted
	// and is HEARD rather than closed, and the bare acknowledgement is last.
	invoke(t, "", "inbox", "--bus", root, "--as", "Ada", "--receipt-max-words", "40", "--full").
		mustCode(t, 0).
		mustContain(t, "stdout", "INBOX HEARD id=bo-222222222222").
		mustContain(t, "stdout", "INBOX OK as=Ada carrying=2 open=1 notes=0 receipts=1 heard=1 unaddressed=0 unreadable=0")
	// Bo's: the answer to her question is a note she owes nothing on until she reads
	// it, and it is the one thing in her inbox.
	invoke(t, "", "inbox", "--bus", root, "--as", "Bo", "--receipt-max-words", "40", "--full").
		mustCode(t, 0).
		mustContain(t, "stdout", "INBOX NOTE id=ada-0f1e2d3c4b5a from=Ada addr=to").
		mustContain(t, "stdout", "INBOX OK as=Bo carrying=1 open=1")
	// And a rebuild over it changes nothing, which is what "the catalogue agrees with the
	// notes" means when you can run it.
	wantAda := read(t, root, "from-ada/INDEX")
	wantBo := read(t, root, "from-bo/INDEX")
	invoke(t, "", "check", "--bus", root, "--full", "--rebuild-index").mustCode(t, 0)
	if read(t, root, "from-ada/INDEX") != wantAda || read(t, root, "from-bo/INDEX") != wantBo {
		t.Fatal("a rebuild changed the example bus's catalogue, so the committed one is stale")
	}
	// As a repository root it is a bus the git-reading verbs will work over, which is
	// what its README tells a reader to make it. `--since HEAD` is the cheapest proof:
	// the root test passes, the diff runs, and the change set over no change is empty.
	invoke(t, "", "check", "--bus", root, "--since", "HEAD").mustCode(t, 0).
		mustContain(t, "stdout", "BUS SCOPE mode=since").
		mustContain(t, "stdout", "changed=0")
	// And the CURSOR it ships records what its OPEN list holds, so a reader arriving on it
	// is not refused for an open list that went missing.
	if line := read(t, root, "from-ada/CURSOR"); !strings.Contains(line, "open=2") {
		t.Fatalf("the example cursor does not record its two carried notes: %q", line)
	}
}

func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(to, rel), 0o755)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(to, rel), raw, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A reader's very first --advance with an EMPTY inbox. There is no OPEN file to commit,
// and `git add` refuses a pathspec matching neither the disk nor the index -- so this run
// used to list the inbox correctly and then die on `pathspec 'from-ada/OPEN' did not
// match any files`. The cursor must land anyway: a reader with nothing open is the state
// every reader is trying to get to.
func TestAFirstAdvanceWithNothingOpen(t *testing.T) {
	hermetic(t)
	checkout, bare := busDir(t)
	// Ada answers the two the fixture leaves open, so nothing is carried.
	invoke(t, draft, "send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3").mustCode(t, 0)
	invoke(t, "From: Ada\nTo: Bo\nRe: bo-111111111111\nSubject: That one too\n\nAnswered.\n",
		"send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3").mustCode(t, 0)

	invoke(t, "", advance(checkout, "Ada")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX OK as=Ada carrying=0 open=0").
		mustContain(t, "stdout", "INBOX CURSOR commit=").
		mustContain(t, "stdout", "carrying=0 pushed=true")
	files := gitIn(t, bare, "ls-tree", "-r", "--name-only", "main")
	if !strings.Contains(files, "from-ada/CURSOR") {
		t.Fatalf("the cursor did not land:\n%s", files)
	}
	if strings.Contains(files, "from-ada/OPEN") {
		t.Fatalf("an empty OPEN list was committed as a file:\n%s", files)
	}
	// The next run reads from it, and is a `since` run over no change at all.
	invoke(t, "", advance(checkout, "Ada")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX SCOPE mode=since").
		mustContain(t, "stdout", "INBOX OK as=Ada carrying=0 open=0")
}

// And the other direction: a reader who HAD an open list and now has none. The OPEN file
// is tracked, so its removal must be staged or git brings it straight back.
func TestAnOpenListThatEmptiesIsRemovedFromTheBus(t *testing.T) {
	hermetic(t)
	checkout, bare := busDir(t)
	invoke(t, "", advance(checkout, "Ada")...).mustCode(t, 0)
	if files := gitIn(t, bare, "ls-tree", "-r", "--name-only", "main"); !strings.Contains(files, "from-ada/OPEN") {
		t.Fatalf("the first advance did not publish an OPEN list:\n%s", files)
	}
	invoke(t, draft, "send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3").mustCode(t, 0)
	invoke(t, "From: Ada\nTo: Bo\nRe: bo-111111111111\nSubject: That one too\n\nAnswered.\n",
		"send", "--bus", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3").mustCode(t, 0)
	invoke(t, "", advance(checkout, "Ada")...).mustCode(t, 0).mustContain(t, "stdout", "carrying=0")
	if files := gitIn(t, bare, "ls-tree", "-r", "--name-only", "main"); strings.Contains(files, "from-ada/OPEN") {
		t.Fatalf("an emptied OPEN list is still on the bus:\n%s", files)
	}
	if out := gitIn(t, checkout, "status", "--porcelain"); strings.TrimSpace(out) != "" {
		t.Fatalf("the checkout is dirty after an emptied OPEN list:\n%s", out)
	}
}

// THE SWITCH DAY, end to end. The bus this tool was written for had been running by hand
// for months when it adopted the tool, and the first `inbox --as Ada --full` reported
// 657 notes open. Because the open list is what lets the cursor move, every run after it
// reported the same 657 -- forever, until each was answered or receipted one at a time.
// Nobody was going to do that, and a listing nobody reads hides the one new note in it.
//
// So the line is drawn on a date: the notes behind it are not carried, not listed, and
// counted on one line; the notes in front of it are the inbox. The date goes into the
// cursor, so the run after it does not have to be told again.
func TestTheSwitchDayLineLeavesTheOldNotesOffTheOpenList(t *testing.T) {
	hermetic(t)
	checkout, _ := busDir(t)
	// Two notes from before the line and one after it, on top of the fixture's two, which
	// are dated 2026-09-07 and are therefore also in front of the line.
	writeFile(t, checkout, "from-bo/2026-08-01T0001Z-old-one-aaaaaaaaaaaa.md",
		"From: Bo\nTo: Ada\nDate: Sat Aug  1 00:01:00 UTC 2026\nId: bo-aaaaaaaaaaaa\nSubject: One from the months before the tool\n\nThe body.\n")
	writeFile(t, checkout, "from-bo/2026-08-02T0001Z-old-two-bbbbbbbbbbbb.md",
		"From: Bo\nTo: Ada\nDate: Sun Aug  2 00:01:00 UTC 2026\nId: bo-bbbbbbbbbbbb\nSubject: Another from the months before the tool\n\nThe body.\n")
	gitIn(t, checkout, "add", "-A")
	gitIn(t, checkout, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", "the months before")
	gitIn(t, checkout, "push", "-q", "origin", "HEAD:refs/heads/main")

	// The switch-day read: full, with the line, advancing.
	r := invoke(t, "", advance(checkout, "Ada", "--full", "--legacy-before", "2026-09-01")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX LEGACY before=2026-09-01 notes=2").
		mustContain(t, "stdout", "INBOX OK as=Ada carrying=2 open=2")
	for _, old := range []string{"bo-aaaaaaaaaaaa", "bo-bbbbbbbbbbbb"} {
		if strings.Contains(r.stdout, old) {
			t.Fatalf("a note behind the line was listed one by one:\n%s", r.stdout)
		}
	}
	// The open list is the notes in front of the line and nothing else, and the cursor
	// carries the date so the next run needs no flag.
	if open := read(t, checkout, "from-ada/OPEN"); strings.Contains(open, "aaaaaaaaaaaa") || strings.Contains(open, "bbbbbbbbbbbb") {
		t.Fatalf("the open list carries a note from behind the line:\n%s", open)
	}
	if cursor := read(t, checkout, "from-ada/CURSOR"); !strings.Contains(cursor, "legacy=2026-09-01") {
		t.Fatalf("the cursor did not record the line: %s", cursor)
	}

	// The second read, with no flag at all: quiet. It honours the line from the cursor,
	// says so, and names none of the old notes.
	r = invoke(t, "", advance(checkout, "Ada")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX SCOPE mode=since").
		mustContain(t, "stdout", "INBOX LEGACY before=2026-09-01 notes=0").
		mustContain(t, "stdout", "INBOX OK as=Ada carrying=2 open=2")
	for _, old := range []string{"bo-aaaaaaaaaaaa", "bo-bbbbbbbbbbbb"} {
		if strings.Contains(r.stdout, old) {
			t.Fatalf("the second read brought the old notes back:\n%s", r.stdout)
		}
	}

	// A line that moves EARLIER would put the notes between the two dates back on the open
	// list, which is a listing the reader has already settled arriving with nothing saying
	// why. It is refused, and the refusal names the read that can honestly do it.
	invoke(t, "", advance(checkout, "Ada", "--legacy-before", "2026-08-02")...).mustCode(t, 1).
		mustContain(t, "stderr", "moves the line earlier").
		mustContain(t, "stderr", "--full --legacy-before 2026-08-02 --advance")

	// Moving it LATER forgives more and needs no --full: the forgiven set only grows, and
	// nothing comes back.
	invoke(t, "", advance(checkout, "Ada", "--legacy-before", "2026-09-08")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX LEGACY before=2026-09-08 notes=2").
		mustContain(t, "stdout", "INBOX OK as=Ada carrying=0 open=0")

	// And --full is the way through, exactly as the refusal said: with the earlier line the
	// old notes are the reader's again, counted at zero because none is behind it.
	r = invoke(t, "", advance(checkout, "Ada", "--full", "--legacy-before", "2026-08-01")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX LEGACY before=2026-08-01 notes=0").
		mustContain(t, "stdout", "INBOX OK as=Ada carrying=4 open=4")
	if !strings.Contains(r.stdout, "bo-aaaaaaaaaaaa") {
		t.Fatalf("a --full read with an earlier line did not bring the old notes back:\n%s", r.stdout)
	}
}

// The flag itself: a date it cannot read is a bad invocation rather than a guess, on the
// same rule check's is.
func TestInboxRefusesALegacyDateItCannotRead(t *testing.T) {
	checkout, _ := busDir(t)
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
		"--full", "--legacy-before", "last Tuesday").mustCode(t, 2).
		mustContain(t, "stderr", "is not a UTC date")
}

// THE FIRST ADVANCE ON A LANE, and the 602 notes that made it a question the tool asks.
//
// A live line ran `inbox --full --advance` as its first read, on a bus of about 1,900
// notes, with no switch-day line -- because the line is a flag you have to know about
// before the run that needs it, and the run that needs it is the first one. It worked as
// written: 602 old notes went onto the open list, the cursor was written beside them, and
// every poll after it printed the same 602 carried notes. Glenn, reading the polls: "lots
// of spam there. do we need so much spam? it costs $$$".
//
// So the first advance on a lane, over notes older than today, is refused until the reader
// says which they mean. The refusal names the count and hands them the line to run.
func TestAFirstAdvanceOverOldNotesIsRefused(t *testing.T) {
	hermetic(t)
	checkout, bare := busDir(t)
	// The fixture's two notes are dated 2026-09-07 and the clock is 2026-09-09.
	r := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
		"--advance", "--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 1).
		mustContain(t, "stderr", "INBOX REFUSED: this is the first advance on from-ada/CURSOR").
		mustContain(t, "stderr", "2 of the 2 notes it would carry are dated before today").
		// The line to run, with TOMORROW's date computed for the reader, so that every note
		// on the bus today is behind it and what arrives from now on is not.
		mustContain(t, "stderr", `--full --legacy-before 2026-09-10 --advance --remote "origin" --branch "main"`).
		mustContain(t, "stderr", "--carry-history")
	// It refuses BEFORE the listing, because the listing is the cost being complained
	// about: a first full read of that bus is a line per open note.
	if r.stdout != "" {
		t.Fatalf("the refusal printed a listing anyway:\n%s", r.stdout)
	}
	// And it wrote nothing: no cursor, no open list, nothing pushed.
	for _, p := range []string{"from-ada/CURSOR", "from-ada/OPEN"} {
		if _, err := os.Stat(filepath.Join(checkout, filepath.FromSlash(p))); !os.IsNotExist(err) {
			t.Fatalf("%s exists after a refused advance: %v", p, err)
		}
	}
	if files := gitIn(t, bare, "ls-tree", "-r", "--name-only", "main"); strings.Contains(files, "from-ada/") {
		t.Fatalf("a refused advance pushed something:\n%s", files)
	}
	// The first answer, which is the one the refusal recommends: draw the line. The old
	// notes are counted on the LEGACY line and carried by nobody.
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
		"--full", "--legacy-before", "2026-09-10",
		"--advance", "--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 0).
		mustContain(t, "stdout", "INBOX LEGACY before=2026-09-10 notes=2").
		mustContain(t, "stdout", "INBOX OK as=Ada carrying=0 open=0")
	if cursor := read(t, checkout, "from-ada/CURSOR"); !strings.Contains(cursor, "legacy=2026-09-10") {
		t.Fatalf("the cursor did not record the line the refusal named: %s", cursor)
	}
}

// The other answer: the reader who means to carry the history says so, once, and the guard
// never asks again -- it is a question about a FIRST advance, and after it there is a
// cursor.
func TestAFirstAdvanceCarriesTheHistoryWhenAsked(t *testing.T) {
	hermetic(t)
	checkout, _ := busDir(t)
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
		"--carry-history",
		"--advance", "--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 0).
		mustContain(t, "stdout", "INBOX OK as=Ada carrying=2 open=2")
	if open := read(t, checkout, "from-ada/OPEN"); !strings.Contains(open, "bo-abcdef012345") {
		t.Fatalf("--carry-history did not carry the old notes:\n%s", open)
	}
	// It is not a switch-day line and does not become one: nothing is written to the cursor
	// that a later run would honour.
	if cursor := read(t, checkout, "from-ada/CURSOR"); strings.Contains(cursor, "legacy=") {
		t.Fatalf("--carry-history wrote a legacy line into the cursor: %s", cursor)
	}
	// The second advance needs neither flag: there is a cursor now.
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
		"--advance", "--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 0).
		mustContain(t, "stdout", "INBOX OK as=Ada carrying=2 open=2")
}

// The guard is about a HISTORY, so a bus that has none does not meet it. A line joining a
// bus whose notes are all from today advances with no flag at all, which is what a bus
// started with this tool looks like forever.
func TestAFirstAdvanceOnABusWithNoOldNotesNeedsNeither(t *testing.T) {
	hermetic(t)
	checkout, _ := busDir(t)
	// Take the fixture's two old notes off the bus and leave one note dated today.
	for _, p := range []string{"from-bo/2026-09-07T0001Z-a-question-abcdef012345.md", "from-bo/2026-09-07T0002Z-heard-111111111111.md"} {
		if err := os.Remove(filepath.Join(checkout, filepath.FromSlash(p))); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, checkout, "from-bo/2026-09-09T0900Z-today-cccccccccccc.md",
		"From: Bo\nTo: Ada\nDate: Wed Sep  9 09:00:00 UTC 2026\nId: bo-cccccccccccc\nSubject: Written today\n\nDoes the guard fire on a bus with no history?\n")
	writeFile(t, checkout, "from-bo/INDEX",
		"bo-cccccccccccc\tfrom-bo/2026-09-09T0900Z-today-cccccccccccc.md\t2026-09-09T09:00:00Z\tAda\t-\n")
	gitIn(t, checkout, "add", "-A")
	gitIn(t, checkout, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", "a bus with no history")
	gitIn(t, checkout, "push", "-q", "origin", "HEAD:refs/heads/main")

	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
		"--advance", "--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 0).
		mustContain(t, "stdout", "INBOX OK as=Ada carrying=1 open=1")
}

// The two answers answer the same question, so giving both says nothing about which.
func TestTheTwoAnswersToTheFirstAdvanceCannotBothBeGiven(t *testing.T) {
	checkout, _ := busDir(t)
	invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
		"--carry-history", "--legacy-before", "2026-09-10",
		"--advance", "--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 2).
		mustContain(t, "stderr", "give one or the other")
}

// The other half of the same complaint, from the other end: a reader carrying a lot is told
// the number and where the entries are, rather than being handed the entries. The count
// alone is already the default at any size; the HINT is the line that says the flag.
func TestAPlainInboxHintsWhereTheCarriedEntriesAre(t *testing.T) {
	hermetic(t)
	checkout, _ := busDir(t)
	for i := 0; i < 60; i++ {
		id := fmt.Sprintf("bo-d%011d", i)
		writeFile(t, checkout, fmt.Sprintf("from-bo/2026-09-09T10%02dZ-many-%s.md", i, id),
			fmt.Sprintf("From: Bo\nTo: Ada\nDate: Wed Sep  9 10:%02d:00 UTC 2026\nId: %s\nSubject: One of many %d\n\nA note that will sit open.\n", i, id, i))
	}
	gitIn(t, checkout, "add", "-A")
	gitIn(t, checkout, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", "many notes")
	gitIn(t, checkout, "push", "-q", "origin", "HEAD:refs/heads/main")
	invoke(t, "", advance(checkout, "Ada")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX OK as=Ada carrying=62 open=62")

	// The plain run: the count, and one line saying how to see them.
	r := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40").
		mustCode(t, 0).
		mustContain(t, "stdout", "INBOX OPEN carrying=62 heard=0").
		mustContain(t, "stdout", "INBOX HINT --open lists the 62 carried entries; they are also in from-ada/OPEN")
	if strings.Contains(r.stdout, "INBOX NOTE ") {
		t.Fatalf("a plain run listed the carried entries:\n%s", r.stdout)
	}
	if n := strings.Count(r.stdout, "\n"); n > 4 {
		t.Fatalf("a plain run carrying 62 printed %d lines:\n%s", n, r.stdout)
	}
	// --open is the answer the hint gives, and it does not repeat the hint.
	r = invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40", "--open").
		mustCode(t, 0)
	if strings.Contains(r.stdout, "INBOX HINT") {
		t.Fatalf("--open printed the hint at the reader who took it:\n%s", r.stdout)
	}
	// 61 notes and the fixture's one bare acknowledgement, which is 62 entries listed.
	if n := strings.Count(r.stdout, "INBOX NOTE ") + strings.Count(r.stdout, "INBOX RECEIPT "); n != 62 {
		t.Fatalf("--open listed %d entries, want 62:\n%s", n, r.stdout)
	}
}

// And a reader carrying a handful is not told: the flag is guessable from a short listing,
// and a hint on every run is the noise this is about.
func TestASmallOpenListGetsNoHint(t *testing.T) {
	hermetic(t)
	checkout, _ := busDir(t)
	invoke(t, "", advance(checkout, "Ada")...).mustCode(t, 0)
	r := invoke(t, "", "inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40").
		mustCode(t, 0).mustContain(t, "stdout", "INBOX OPEN carrying=2 heard=0")
	if strings.Contains(r.stdout, "INBOX HINT") {
		t.Fatalf("a reader carrying 2 was hinted at:\n%s", r.stdout)
	}
}
