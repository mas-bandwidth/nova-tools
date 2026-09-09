package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
)

// The property this file exists for, in Glenn's words: "Make sure the bus tool is O(n)
// where n is the number of new messages to be read, instead of O(m) where m is all
// messages sent so far. This way it maintains performance over time."
//
// Every test here is about work NOT done, which is the hard kind to assert on: work not
// done leaves no output. So the assertion is a COUNT taken at the one place the work
// happens -- bus.NoteParses, incremented by ParseNote -- and never a wall time. A timing
// assertion on a shared CI runner is a flake, and on a fast enough machine it passes over
// a quadratic implementation.

// advance is the flags that move a reader's cursor, since every test below does it.
func advance(checkout, who string, extra ...string) []string {
	return append([]string{
		"inbox", "--table", checkout, "--as", who, "--receipt-max-words", "40",
		"--advance", "--remote", "origin", "--branch", "main", "--attempts", "3",
	}, extra...)
}

func read(t *testing.T, checkout, path string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(checkout, filepath.FromSlash(path)))
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	return string(raw)
}

// THE COMPLEXITY PROPERTY, proved. A table of ten thousand notes and one new one: inbox
// parses ONE note file.
//
// The 10,000 notes are addressed to Stella, so they are not Rowan's business and the
// listing has nothing to say about them; the point is that Rowan's read does not TOUCH
// them. Rowan takes a cursor with one full run -- which does parse all 10,001, and says
// mode=full so nobody mistakes it for the cheap path -- answers what the fixture leaves
// open so that the count below is the whole of "new", and then one more note arrives. The
// parse count for that run is 1.
func TestInboxParsesOnlyWhatIsNewSinceTheCursor(t *testing.T) {
	hermetic(t)
	checkout, _ := table(t)

	const history = 10000
	var index strings.Builder
	for i := range history {
		id := fmt.Sprintf("stella-%012x", i+0x100000)
		path := fmt.Sprintf("from-stella/2026-08-%02dT%02d%02dZ-bulk-%s.md", i%28+1, i/60%24, i%60, id[len(id)-12:])
		writeFile(t, checkout, path, fmt.Sprintf(
			"From: Stella\nTo: Stella\nDate: Sat Aug %2d 00:00:00 UTC 2026\nId: %s\nSubject: bulk %d\n\nA note that is not addressed to Rowan.\n",
			i%28+1, id, i))
		fmt.Fprintf(&index, "%s\t%s\t2026-08-%02dT00:00:00Z\tStella\t-\n", id, path, i%28+1)
	}
	appendFile(t, checkout, "from-stella/INDEX", index.String())
	gitIn(t, checkout, "add", "-A")
	gitIn(t, checkout, "-c", "user.name=Stella", "-c", "user.email=stella@mas-bandwidth.com", "commit", "-q", "-m", "ten thousand notes")
	gitIn(t, checkout, "push", "-q", "origin", "HEAD:refs/heads/main")

	// The first run has no cursor, so it is a full one and it says so. This is the only
	// full read a reader ever pays for.
	before := bus.NoteParses()
	r := invoke(t, "", advance(checkout, "Rowan")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX SCOPE mode=full cursor=-").
		mustContain(t, "stdout", "INBOX CURSOR commit=")
	if full := bus.NoteParses() - before; full < history {
		t.Fatalf("the full run parsed %d notes over a table of %d; the fixture is not what this test thinks it is\n%s", full, history, r.stdout)
	}

	// Answer the two notes the fixture leaves open, so that "open" is zero and the count
	// below is the whole of "new". Both replies name their note by id.
	invoke(t, draft, "send", "--table", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3").mustCode(t, 0)
	invoke(t, "From: Rowan\nTo: Stella\nRe: stella-111111111111\nSubject: And that one too\n\nHeard your heard, and answered.\n",
		"send", "--table", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3").mustCode(t, 0)
	invoke(t, "", advance(checkout, "Rowan")...).mustCode(t, 0).mustContain(t, "stdout", "carrying=0")

	// One new note, addressed to Rowan this time.
	writeFile(t, checkout, "from-stella/2026-09-08T0900Z-one-more-222222222222.md",
		"From: Stella\nTo: Rowan\nDate: Tue Sep  8 09:00:00 UTC 2026\nId: stella-222222222222\nSubject: One more\n\nIs the gate on the merge queue?\n")
	appendFile(t, checkout, "from-stella/INDEX",
		"stella-222222222222\tfrom-stella/2026-09-08T0900Z-one-more-222222222222.md\t2026-09-08T09:00:00Z\tRowan\t-\n")
	gitIn(t, checkout, "add", "-A")
	gitIn(t, checkout, "-c", "user.name=Stella", "-c", "user.email=stella@mas-bandwidth.com", "commit", "-q", "-m", "one more")
	gitIn(t, checkout, "push", "-q", "origin", "HEAD:refs/heads/main")

	before = bus.NoteParses()
	r = invoke(t, "", advance(checkout, "Rowan")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX SCOPE mode=since").
		mustContain(t, "stdout", "INBOX NOTE id=stella-222222222222").
		mustContain(t, "stdout", "INBOX OK as=Rowan open=1")
	// ONE. Not one plus the lane, not one plus the history: one file opened and parsed,
	// over a table of ten thousand and one.
	if got := bus.NoteParses() - before; got != 1 {
		t.Fatalf("inbox parsed %d notes for one new note over a table of %d; the read is not O(new)\n%s", got, history+1, r.stdout)
	}

	// And the run after that parses ONE: nothing is new, and the one parse is the note
	// still being carried open, which is the "+ open" half of the property and is bounded
	// by what this reader owes rather than by what the table holds.
	before = bus.NoteParses()
	invoke(t, "", advance(checkout, "Rowan")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX SCOPE mode=since").
		mustContain(t, "stdout", "carrying=1")
	if got := bus.NoteParses() - before; got != 1 {
		t.Fatalf("a run over an unchanged table parsed %d notes, want the 1 it is carrying open", got)
	}
}

// The OPEN list is the reason the cursor is allowed to move past an unanswered note. Two
// runs, with the note arriving before the first and being answered after the second: it is
// listed both times and gone the third.
func TestOpenListSurvivesTheCursorMovingPastIt(t *testing.T) {
	hermetic(t)
	checkout, _ := table(t)

	invoke(t, "", advance(checkout, "Rowan")...).mustCode(t, 0)
	// Both of Stella's notes are now carried, and the cursor is at HEAD.
	if got := read(t, checkout, "from-rowan/OPEN"); !strings.Contains(got, "stella-abcdef012345") {
		t.Fatalf("the question was not carried into OPEN:\n%s", got)
	}
	cursorOne := read(t, checkout, "from-rowan/CURSOR")

	// A second run, with NOTHING new on the table. The cursor has already moved past the
	// note, so without the OPEN list this listing would be empty -- which is the failure
	// the list exists to stop.
	r := invoke(t, "", advance(checkout, "Rowan")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX SCOPE mode=since").
		mustContain(t, "stdout", "INBOX NOTE id=stella-abcdef012345").
		mustContain(t, "stdout", "INBOX OK as=Rowan open=2")
	if strings.Contains(r.stdout, "changed=0") && !strings.Contains(r.stdout, "carrying=2") {
		t.Fatalf("the second run lost what the first was carrying:\n%s", r.stdout)
	}
	if read(t, checkout, "from-rowan/CURSOR") == cursorOne {
		t.Fatalf("the second run did not advance the cursor past its own first commit")
	}

	// Now answer it. The reply names the note by id, so the third run drops it from OPEN
	// and from the listing.
	invoke(t, draft, "send", "--table", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3").mustCode(t, 0)
	r = invoke(t, "", advance(checkout, "Rowan")...).mustCode(t, 0)
	if strings.Contains(r.stdout, "stella-abcdef012345") {
		t.Fatalf("an answered note is still carried:\n%s", r.stdout)
	}
	if got := read(t, checkout, "from-rowan/OPEN"); strings.Contains(got, "stella-abcdef012345") {
		t.Fatalf("an answered note is still in OPEN:\n%s", got)
	}
}

// A receipt survives the cursor too: heard is not answered, and a note I receipted three
// runs ago is still listed as HEARD rather than quietly dropped.
func TestHeardSurvivesTheCursor(t *testing.T) {
	hermetic(t)
	checkout, _ := table(t)
	invoke(t, "", advance(checkout, "Rowan")...).mustCode(t, 0)
	invoke(t, "", "receipt", "--table", checkout, "--as", "Rowan", "--note", "stella-abcdef012345",
		"--remote", "origin", "--branch", "main", "--attempts", "3").mustCode(t, 0)
	invoke(t, "", advance(checkout, "Rowan")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX HEARD id=stella-abcdef012345")
	// And again, with the receipt now far behind the cursor.
	invoke(t, "", advance(checkout, "Rowan")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX HEARD id=stella-abcdef012345").
		mustContain(t, "stdout", "heard=1")
}

// A cursor that is no longer on this history is a REFUSAL naming --full, never a best
// effort. A reader told "nothing new" by a broken cursor has been lied to in exactly the
// way this tool exists to stop.
func TestACursorThatIsNotAnAncestorIsRefused(t *testing.T) {
	hermetic(t)
	checkout, _ := table(t)
	writeFile(t, checkout, "from-rowan/CURSOR", "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef 2026-09-09T12:00:00Z\n")
	r := invoke(t, "", "inbox", "--table", checkout, "--as", "Rowan", "--receipt-max-words", "40").
		mustCode(t, 1).
		mustContain(t, "stderr", "INBOX REFUSED: ").
		mustContain(t, "stderr", "is not an ancestor of HEAD").
		mustContain(t, "stderr", "--full")
	if n := strings.Count(strings.TrimRight(r.stderr, "\n"), "\n"); n != 0 {
		t.Fatalf("the refusal is %d lines, want one:\n%q", n+1, r.stderr)
	}
	// --full is the fallback it names, and it works.
	invoke(t, "", "inbox", "--table", checkout, "--as", "Rowan", "--receipt-max-words", "40", "--full").
		mustCode(t, 0).mustContain(t, "stdout", "INBOX SCOPE mode=full")
	// A cursor that is not a commit at all is refused where it is read, before it can
	// become a git argument.
	writeFile(t, checkout, "from-rowan/CURSOR", "--upload-pack=id 2026-09-09T12:00:00Z\n")
	invoke(t, "", "inbox", "--table", checkout, "--as", "Rowan", "--receipt-max-words", "40").
		mustCode(t, 1).mustContain(t, "stderr", "is not a commit")
	// check reads the same cursor and refuses it the same way.
	invoke(t, "", "check", "--table", checkout, "--as", "Rowan").
		mustCode(t, 1).mustContain(t, "stderr", "BUS REFUSED: ")
}

// A --table that is not the ROOT of its repository is refused by every verb that reads
// git, and this is the blocker the second read found. It used to run: `rev-parse
// --is-inside-work-tree` is true anywhere under a repository, `git diff --name-only`
// reports `table/from-stella/x.md` from the repository root, ChangedSince keeps only paths
// beginning `from-`, and so `inbox --since` and `check --as` printed changed=0 and exited 0
// over notes nobody had read. Exit 2, because a --table the tool will not work over is a
// bad invocation and not a table that failed.
func TestATableBelowTheRepositoryRootIsRefused(t *testing.T) {
	hermetic(t)
	bare := filepath.Join(t.TempDir(), "table.git")
	if err := os.MkdirAll(bare, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, bare, "init", "--bare", "--quiet", "--initial-branch=main")
	checkout := filepath.Join(t.TempDir(), "checkout")
	gitIn(t, filepath.Dir(checkout), "clone", "--quiet", bare, checkout)
	gitIn(t, checkout, "checkout", "-q", "-B", "main")
	// The table is one directory down, which is exactly how a table kept inside a bigger
	// repository -- a docs tree, a monorepo -- would be pointed at.
	nested := filepath.Join(checkout, "table")
	writeFile(t, nested, "participants.json", rosterJSON)
	writeFile(t, nested, "from-stella/2026-09-07T0001Z-a-question-abcdef012345.md",
		"From: Stella\nTo: Rowan\nDate: Mon Sep  7 00:01:00 UTC 2026\nId: stella-abcdef012345\nSubject: A question about the gate\n\nShould the gate run on the merge queue too?\n")
	writeFile(t, nested, "from-stella/INDEX",
		"stella-abcdef012345\tfrom-stella/2026-09-07T0001Z-a-question-abcdef012345.md\t2026-09-07T00:01:00Z\tRowan\t-\n")
	gitIn(t, checkout, "add", "-A")
	gitIn(t, checkout, "-c", "user.name=Stella", "-c", "user.email=stella@mas-bandwidth.com", "commit", "-q", "-m", "a table one directory down")
	gitIn(t, checkout, "push", "-q", "origin", "HEAD:refs/heads/main")

	for _, args := range [][]string{
		{"inbox", "--table", nested, "--as", "Rowan", "--receipt-max-words", "40"},
		{"check", "--table", nested, "--as", "Rowan"},
		{"check", "--table", nested, "--since", "HEAD"},
		{"receipt", "--table", nested, "--as", "Rowan", "--note", "stella-abcdef012345",
			"--remote", "origin", "--branch", "main", "--attempts", "3"},
	} {
		invoke(t, "", args...).mustCode(t, 2).
			mustContain(t, "stderr", "is not its root").
			mustContain(t, "stderr", "empty change set over unread notes")
	}
	invoke(t, draft, "send", "--table", nested, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 2).mustContain(t, "stderr", "is not its root")
	// The verbs that need no git still work over it, so the refusal is exactly as wide as
	// the failure: a table below a repository root is readable, it just cannot be read
	// INCREMENTALLY, and the tool says which.
	invoke(t, "", "check", "--table", nested, "--full").mustCode(t, 0).mustContain(t, "stdout", "BUS OK")
	invoke(t, "", "inbox", "--table", nested, "--as", "Rowan", "--receipt-max-words", "40", "--full").
		mustCode(t, 0).mustContain(t, "stdout", "INBOX NOTE id=stella-abcdef012345")
}

// A cursor advances past a note because OPEN remembers it. Delete OPEN alone -- the cursor
// stays valid, and an empty OPEN list is REMOVED rather than left zero-length, so absent and
// nothing-open look the same on disk -- and the next run would have said open=0 over notes
// still owed. The cursor carries the count it was written with, so the two states are
// different and this is a refusal naming --full --advance.
func TestACursorWhoseOpenListWentMissingIsRefused(t *testing.T) {
	hermetic(t)
	checkout, _ := table(t)
	invoke(t, "", advance(checkout, "Rowan")...).mustCode(t, 0).mustContain(t, "stdout", "carrying=2")
	if line := read(t, checkout, "from-rowan/CURSOR"); !strings.Contains(line, "open=2") {
		t.Fatalf("the cursor does not record what it was carrying: %q", line)
	}
	if err := os.Remove(filepath.Join(checkout, "from-rowan", "OPEN")); err != nil {
		t.Fatal(err)
	}
	invoke(t, "", "inbox", "--table", checkout, "--as", "Rowan", "--receipt-max-words", "40").
		mustCode(t, 1).
		mustContain(t, "stderr", "INBOX REFUSED: ").
		mustContain(t, "stderr", "was carrying 2 notes").
		mustContain(t, "stderr", "--full --advance")
	// And the way through is the one it names: a full read rebuilds the open list from the
	// whole table and both notes come back.
	invoke(t, "", advance(checkout, "Rowan", "--full")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX SCOPE mode=full").
		mustContain(t, "stdout", "INBOX OK as=Rowan open=2").
		mustContain(t, "stdout", "carrying=2")
	invoke(t, "", "inbox", "--table", checkout, "--as", "Rowan", "--receipt-max-words", "40").
		mustCode(t, 0).mustContain(t, "stdout", "INBOX SCOPE mode=since")
	// A reader with genuinely nothing open has no OPEN file either, and that is NOT the
	// refused state: the count in their cursor is zero and nothing is compared.
	invoke(t, draft, "send", "--table", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3").mustCode(t, 0)
	invoke(t, "From: Rowan\nTo: Stella\nRe: stella-111111111111\nSubject: That one too\n\nAnswered.\n",
		"send", "--table", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3").mustCode(t, 0)
	invoke(t, "", advance(checkout, "Rowan")...).mustCode(t, 0).mustContain(t, "stdout", "carrying=0")
	if _, err := os.Stat(filepath.Join(checkout, "from-rowan", "OPEN")); !os.IsNotExist(err) {
		t.Fatalf("an empty OPEN list was left on disk, so absent no longer means nothing open: %v", err)
	}
	invoke(t, "", "inbox", "--table", checkout, "--as", "Rowan", "--receipt-max-words", "40").
		mustCode(t, 0).mustContain(t, "stdout", "INBOX OK as=Rowan open=0")
}

// The diff is `<cursor>..HEAD`, which is TWO dots and therefore a tree-to-tree comparison,
// not a walk of the commits between them. That is the whole reason a note that arrived on a
// side branch and came in through a MERGE is seen: git compares the two trees and the note
// is in one of them, whatever route it took. Three dots would have taken the merge base and
// missed everything on the branch that was merged.
func TestANoteThatArrivedThroughAMergeIsSeen(t *testing.T) {
	hermetic(t)
	checkout, _ := table(t)
	invoke(t, "", advance(checkout, "Rowan")...).mustCode(t, 0)

	// Stella writes on a side branch while main moves on underneath her.
	gitIn(t, checkout, "checkout", "-q", "-b", "stella-side")
	writeFile(t, checkout, "from-stella/2026-09-08T1000Z-on-a-branch-666666666666.md",
		"From: Stella\nTo: Rowan\nDate: Tue Sep  8 10:00:00 UTC 2026\nId: stella-666666666666\nSubject: On a branch\n\nDoes the runner matrix key need quoting?\n")
	appendFile(t, checkout, "from-stella/INDEX",
		"stella-666666666666\tfrom-stella/2026-09-08T1000Z-on-a-branch-666666666666.md\t2026-09-08T10:00:00Z\tRowan\t-\n")
	gitIn(t, checkout, "add", "-A")
	gitIn(t, checkout, "-c", "user.name=Stella", "-c", "user.email=stella@mas-bandwidth.com", "commit", "-q", "-m", "stella: on a branch")
	gitIn(t, checkout, "checkout", "-q", "main")
	// Meanwhile on main, a note written by hand in a browser -- so it touches no INDEX, and
	// this fixture is about the merge rather than about the catalogue conflict that
	// TestTwoBenchesOfOneLaneConflictOnTheCatalogue pins.
	writeFile(t, checkout, "from-stella/2026-09-08T1100Z-meanwhile-777777777777.md",
		"From: Stella\nTo: Rowan\nDate: Tue Sep  8 11:00:00 UTC 2026\nId: stella-777777777777\nSubject: Meanwhile\n\nAnd the gate on the merge queue?\n")
	gitIn(t, checkout, "add", "-A")
	gitIn(t, checkout, "-c", "user.name=Stella", "-c", "user.email=stella@mas-bandwidth.com", "commit", "-q", "-m", "stella: meanwhile")
	// A real merge commit, with two parents, which is the fixture this test is for.
	gitIn(t, checkout, "-c", "user.name=Stella", "-c", "user.email=stella@mas-bandwidth.com",
		"merge", "-q", "--no-ff", "-m", "merge stella's branch", "stella-side")
	if parents := strings.Fields(strings.TrimSpace(gitIn(t, checkout, "rev-list", "--parents", "-n", "1", "HEAD"))); len(parents) != 3 {
		t.Fatalf("HEAD is not a merge commit: %v", parents)
	}
	gitIn(t, checkout, "push", "-q", "origin", "HEAD:refs/heads/main")

	invoke(t, "", advance(checkout, "Rowan")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX SCOPE mode=since").
		mustContain(t, "stdout", "INBOX NOTE id=stella-666666666666").
		mustContain(t, "stdout", "INBOX NOTE id=stella-777777777777")
	// The run after it crosses the merge once: nothing new arrives a second time. Its
	// changed= is 2 and not 0, and that is the grammar rather than a leak -- changed=
	// counts the PATHS the diff named inside lanes, which includes this reader's own
	// CURSOR and OPEN from the advance just made. Neither is a note, and no note is
	// parsed for them.
	invoke(t, "", advance(checkout, "Rowan")...).mustCode(t, 0).
		mustContain(t, "stdout", "changed=2 carrying=4").
		mustContain(t, "stdout", "INBOX OK as=Rowan open=4 notes=3 receipts=1")
}

// send appends to its lane's catalogue in the SAME commit as the note, and check --full
// agrees with it in both directions.
func TestSendAppendsToTheIndexAndCheckAgrees(t *testing.T) {
	hermetic(t)
	checkout, bare := table(t)
	r := invoke(t, draft, "send", "--table", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3").
		mustCode(t, 0)
	id := field(t, r.stdout, "id=")
	path := field(t, r.stdout, "path=")

	line := read(t, checkout, "from-rowan/INDEX")
	fields := strings.Split(strings.TrimRight(line, "\n"), "\t")
	if len(fields) != 5 {
		t.Fatalf("the index line is %d fields, want 5: %q", len(fields), line)
	}
	if fields[0] != id || fields[1] != path {
		t.Fatalf("the index line names %q at %q; the note is %q at %q", fields[0], fields[1], id, path)
	}
	if fields[3] != "Stella;Glenn" {
		t.Fatalf("the index line's recipients are %q, want the resolved To then Cc", fields[3])
	}
	if fields[4] != "stella-abcdef012345" {
		t.Fatalf("the index line's Re is %q", fields[4])
	}
	// One commit, both files: a catalogue that could lag the notes by a commit is one a
	// reader between the two would resolve wrongly.
	files := gitIn(t, bare, "show", "--name-only", "--format=", "main")
	if !strings.Contains(files, "from-rowan/INDEX") || !strings.Contains(files, path) {
		t.Fatalf("the note and its index line are not in one commit:\n%s", files)
	}
	invoke(t, "", "check", "--table", checkout, "--full").mustCode(t, 0).
		mustContain(t, "stdout", "BUS OK").mustContain(t, "stdout", "warn=0")
}

// A catalogue line that DISAGREES with a note fails; a note with no catalogue line only
// warns, and --rebuild-index writes it.
func TestCheckFullAgainstTheIndex(t *testing.T) {
	hermetic(t)
	checkout, _ := table(t)

	// A note written by hand, in a browser, the way this table's whole form allows. It has
	// an id and no catalogue line: a WARN, at any date, because the notes are the record
	// and the catalogue is a cache.
	writeFile(t, checkout, "from-stella/2026-09-08T0300Z-by-hand-333333333333.md",
		"From: Stella\nTo: Rowan\nDate: Tue Sep  8 03:00:00 UTC 2026\nId: stella-333333333333\nSubject: By hand\n\nWritten in a browser, with no tool in sight.\n")
	invoke(t, "", "check", "--table", checkout, "--full").mustCode(t, 0).
		mustContain(t, "stderr", "BUS WARN from-stella/2026-09-08T0300Z-by-hand-333333333333.md").
		mustContain(t, "stderr", "--rebuild-index").
		mustContain(t, "stdout", "warn=1")

	// --rebuild-index writes it, and the warning goes.
	invoke(t, "", "check", "--table", checkout, "--full", "--rebuild-index").mustCode(t, 0).
		mustContain(t, "stdout", "BUS INDEX lane=from-stella notes=3").
		mustContain(t, "stdout", "BUS INDEX lane=from-rowan notes=0").
		mustContain(t, "stdout", "warn=0")

	// A catalogue line naming a note that is not there is a FAILURE, not a warning: that
	// one could resolve a thread to the wrong note.
	appendFile(t, checkout, "from-stella/INDEX",
		"stella-444444444444\tfrom-stella/never-written.md\t2026-09-08T04:00:00Z\tRowan\t-\n")
	invoke(t, "", "check", "--table", checkout, "--full").mustCode(t, 1).
		mustContain(t, "stderr", "BUS FAIL from-stella/INDEX:4").
		mustContain(t, "stderr", "which is not a note on this table")

	// And --rebuild-index is refused without --full, because it writes every lane's
	// catalogue from every note in it.
	invoke(t, "", "check", "--table", checkout, "--rebuild-index", "--as", "Rowan").
		mustCode(t, 2).mustContain(t, "stderr", "needs --full")
}

// check --since checks the change set and nothing else, and finds in it what --full would
// find. It is the same per-note code with the lookups answered from the catalogue.
func TestCheckSinceChecksOnlyWhatChanged(t *testing.T) {
	hermetic(t)
	checkout, _ := table(t)
	base := strings.TrimSpace(gitIn(t, checkout, "rev-parse", "HEAD"))

	// Something already on the table that --full would fail on, committed BEFORE the
	// baseline: --since must not see it, and --full must.
	writeFile(t, checkout, "from-stella/older-stranger.md", "From: Stella\nTo: Stela\nSubject: s\n\nbody\n")
	gitIn(t, checkout, "add", "-A")
	gitIn(t, checkout, "-c", "user.name=Stella", "-c", "user.email=stella@mas-bandwidth.com", "commit", "-q", "-m", "older")
	after := strings.TrimSpace(gitIn(t, checkout, "rev-parse", "HEAD"))

	invoke(t, "", "check", "--table", checkout, "--since", after).mustCode(t, 0).
		mustContain(t, "stdout", "BUS SCOPE mode=since").
		mustContain(t, "stdout", "changed=0")
	invoke(t, "", "check", "--table", checkout, "--since", base).mustCode(t, 1).
		mustContain(t, "stderr", "BUS FAIL from-stella/older-stranger.md").
		mustContain(t, "stderr", `"Stela"`)
	invoke(t, "", "check", "--table", checkout, "--full").mustCode(t, 1).
		mustContain(t, "stderr", "BUS FAIL from-stella/older-stranger.md")
	// A revision this checkout does not hold is a bad invocation, not a table that failed.
	invoke(t, "", "check", "--table", checkout, "--since", "nosuchref").
		mustCode(t, 2).mustContain(t, "stderr", "names no commit")
	// And a --since that would be an option to git never reaches git.
	invoke(t, "", "check", "--table", checkout, "--since", "--upload-pack=id").
		mustCode(t, 2).mustContain(t, "stderr", "nova-bus check:")
}

// check refuses to guess a baseline, exactly the way every other flag here is refused.
func TestCheckRefusesToGuessItsBaseline(t *testing.T) {
	checkout, _ := table(t)
	invoke(t, "", "check", "--table", checkout).mustCode(t, 2).
		mustContain(t, "stderr", "give one of --full, --as <name> or --since <commit>").
		mustContain(t, "stderr", "refusing to guess")
	// A reader with no cursor yet has no baseline, so --as falls back to a full run and
	// SAYS so rather than reporting an empty change set as a clean table.
	invoke(t, "", "check", "--table", checkout, "--as", "Rowan").mustCode(t, 0).
		mustContain(t, "stdout", "BUS SCOPE mode=full cursor=-")
}

// inbox without --advance writes NOTHING. A report that edits the table without being
// asked is the surprise this repo does not do.
func TestInboxWithoutAdvanceWritesNothing(t *testing.T) {
	hermetic(t)
	checkout, _ := table(t)
	invoke(t, "", "inbox", "--table", checkout, "--as", "Rowan", "--receipt-max-words", "40").mustCode(t, 0)
	if _, err := os.Stat(filepath.Join(checkout, "from-rowan")); err == nil {
		t.Fatalf("a plain inbox created the reader's lane")
	}
	if out := gitIn(t, checkout, "status", "--porcelain"); strings.TrimSpace(out) != "" {
		t.Fatalf("a plain inbox left the checkout dirty:\n%s", out)
	}
	// --advance without the flags it needs to push is refused by name.
	invoke(t, "", "inbox", "--table", checkout, "--as", "Rowan", "--receipt-max-words", "40", "--advance").
		mustCode(t, 2).mustContain(t, "stderr", "needs --remote and --branch")
}

// The cursor is on the table, not on the bench: it is committed under the reader's own
// identity from the roster and pushed like a receipt.
func TestTheCursorIsPushedLikeAReceipt(t *testing.T) {
	hermetic(t)
	checkout, bare := table(t)
	invoke(t, "", advance(checkout, "Rowan")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX CURSOR commit=").
		mustContain(t, "stdout", "pushed=true attempts=1")
	files := gitIn(t, bare, "ls-tree", "-r", "--name-only", "main")
	for _, want := range []string{"from-rowan/CURSOR", "from-rowan/OPEN"} {
		if !strings.Contains(files, want) {
			t.Fatalf("%s is not on the remote:\n%s", want, files)
		}
	}
	if who := strings.TrimSpace(gitIn(t, bare, "log", "-1", "--format=%an <%ae>", "main")); who != "Rowan <rowan@mas-bandwidth.com>" {
		t.Fatalf("the cursor was committed as %q, not the roster's identity for Rowan", who)
	}
	// --no-push commits it and says the cursor is not on the table.
	writeFile(t, checkout, "from-stella/2026-09-08T0500Z-another-555555555555.md",
		"From: Stella\nTo: Rowan\nDate: Tue Sep  8 05:00:00 UTC 2026\nId: stella-555555555555\nSubject: Another\n\nWhat about the Windows runner?\n")
	gitIn(t, checkout, "add", "-A")
	gitIn(t, checkout, "-c", "user.name=Stella", "-c", "user.email=stella@mas-bandwidth.com", "commit", "-q", "-m", "another")
	gitIn(t, checkout, "push", "-q", "origin", "HEAD:refs/heads/main")
	invoke(t, "", advance(checkout, "Rowan", "--no-push")...).mustCode(t, 0).
		mustContain(t, "stdout", "pushed=false")
}

// The lane's state files are not notes and not strays. check --full says so, and inbox
// does not try to read one as a note.
func TestLaneStateFilesAreNotStrays(t *testing.T) {
	hermetic(t)
	checkout, _ := table(t)
	invoke(t, "", advance(checkout, "Rowan")...).mustCode(t, 0)
	invoke(t, "", "check", "--table", checkout, "--full").mustCode(t, 0).mustContain(t, "stdout", "BUS OK")
	// A file that is none of them still is one.
	writeFile(t, checkout, "from-rowan/notes.txt", "a scratch file\n")
	invoke(t, "", "check", "--table", checkout, "--full").mustCode(t, 1).
		mustContain(t, "stderr", "BUS FAIL from-rowan/notes.txt").
		mustContain(t, "stderr", "RECEIPTS, CURSOR, OPEN, INDEX")
	// A malformed state file is a finding, not a crash: a reader would otherwise refuse on
	// their next run with nothing on the table saying why.
	if err := os.Remove(filepath.Join(checkout, "from-rowan", "notes.txt")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, checkout, "from-rowan/OPEN", "no-path-here\n")
	invoke(t, "", "check", "--table", checkout, "--full").mustCode(t, 1).
		mustContain(t, "stderr", "BUS FAIL from-rowan/OPEN").
		mustContain(t, "stderr", "an open entry is <id or -> <path>")
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

// The example table in testdata is the shape a person and an AI are both pointed at from
// the README, so it is held to the tool rather than left to drift: it passes check --full
// clean, and its listings are the ones the README prints.
//
// It is copied out and given a repository OF ITS OWN, which is the honest shape and is what
// the example's own README now tells a reader to do. In the tree it ships in, it is a
// directory inside a repository about tools, and every verb that reads git refuses a
// --table that is not its repository's root.
func TestTheExampleTableInTestdataIsWhatTheREADMESays(t *testing.T) {
	hermetic(t)
	root := t.TempDir()
	copyTree(t, filepath.Join("testdata", "example-table"), root)
	gitIn(t, root, "init", "--quiet", "-b", "main")
	gitIn(t, root, "add", "-A")
	gitIn(t, root, "-c", "user.name=Rowan", "-c", "user.email=rowan@example.com", "commit", "-q", "-m", "the table")

	invoke(t, "", "check", "--table", root, "--full").mustCode(t, 0).
		mustContain(t, "stdout", "BUS OK notes=4 lanes=2 receipts=1 participants=3 warn=0")
	invoke(t, "", "names", "--table", root).mustCode(t, 0).
		mustContain(t, "stdout", "NAMES NAME name=Glenn lane=-").
		mustContain(t, "stdout", "NAMES OK participants=3 groups=1 senders=2")
	// Rowan's listing: the thread is answered and gone, the Windows finding was receipted
	// and is HEARD rather than closed, and the bare acknowledgement is last.
	invoke(t, "", "inbox", "--table", root, "--as", "Rowan", "--receipt-max-words", "40", "--full").
		mustCode(t, 0).
		mustContain(t, "stdout", "INBOX HEARD id=stella-222222222222").
		mustContain(t, "stdout", "INBOX OK as=Rowan open=1 notes=0 receipts=1 heard=1 unreadable=0")
	// Stella's: the answer to her question is a note she owes nothing on until she reads
	// it, and it is the one thing in her inbox.
	invoke(t, "", "inbox", "--table", root, "--as", "Stella", "--receipt-max-words", "40", "--full").
		mustCode(t, 0).
		mustContain(t, "stdout", "INBOX NOTE id=rowan-0f1e2d3c4b5a from=Rowan addr=to").
		mustContain(t, "stdout", "INBOX OK as=Stella open=1")
	// And a rebuild over it changes nothing, which is what "the catalogue agrees with the
	// notes" means when you can run it.
	wantRowan := read(t, root, "from-rowan/INDEX")
	wantStella := read(t, root, "from-stella/INDEX")
	invoke(t, "", "check", "--table", root, "--full", "--rebuild-index").mustCode(t, 0)
	if read(t, root, "from-rowan/INDEX") != wantRowan || read(t, root, "from-stella/INDEX") != wantStella {
		t.Fatal("a rebuild changed the example table's catalogue, so the committed one is stale")
	}
	// As a repository root it is a table the git-reading verbs will work over, which is
	// what its README tells a reader to make it. `--since HEAD` is the cheapest proof:
	// the root test passes, the diff runs, and the change set over no change is empty.
	invoke(t, "", "check", "--table", root, "--since", "HEAD").mustCode(t, 0).
		mustContain(t, "stdout", "BUS SCOPE mode=since").
		mustContain(t, "stdout", "changed=0")
	// And the CURSOR it ships records what its OPEN list holds, so a reader arriving on it
	// is not refused for an open list that went missing.
	if line := read(t, root, "from-rowan/CURSOR"); !strings.Contains(line, "open=2") {
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
// used to list the inbox correctly and then die on `pathspec 'from-rowan/OPEN' did not
// match any files`. The cursor must land anyway: a reader with nothing open is the state
// every reader is trying to get to.
func TestAFirstAdvanceWithNothingOpen(t *testing.T) {
	hermetic(t)
	checkout, bare := table(t)
	// Rowan answers the two the fixture leaves open, so nothing is carried.
	invoke(t, draft, "send", "--table", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3").mustCode(t, 0)
	invoke(t, "From: Rowan\nTo: Stella\nRe: stella-111111111111\nSubject: That one too\n\nAnswered.\n",
		"send", "--table", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3").mustCode(t, 0)

	invoke(t, "", advance(checkout, "Rowan")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX OK as=Rowan open=0").
		mustContain(t, "stdout", "INBOX CURSOR commit=").
		mustContain(t, "stdout", "carrying=0 pushed=true")
	files := gitIn(t, bare, "ls-tree", "-r", "--name-only", "main")
	if !strings.Contains(files, "from-rowan/CURSOR") {
		t.Fatalf("the cursor did not land:\n%s", files)
	}
	if strings.Contains(files, "from-rowan/OPEN") {
		t.Fatalf("an empty OPEN list was committed as a file:\n%s", files)
	}
	// The next run reads from it, and is a `since` run over no change at all.
	invoke(t, "", advance(checkout, "Rowan")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX SCOPE mode=since").
		mustContain(t, "stdout", "INBOX OK as=Rowan open=0")
}

// And the other direction: a reader who HAD an open list and now has none. The OPEN file
// is tracked, so its removal must be staged or git brings it straight back.
func TestAnOpenListThatEmptiesIsRemovedFromTheTable(t *testing.T) {
	hermetic(t)
	checkout, bare := table(t)
	invoke(t, "", advance(checkout, "Rowan")...).mustCode(t, 0)
	if files := gitIn(t, bare, "ls-tree", "-r", "--name-only", "main"); !strings.Contains(files, "from-rowan/OPEN") {
		t.Fatalf("the first advance did not publish an OPEN list:\n%s", files)
	}
	invoke(t, draft, "send", "--table", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3").mustCode(t, 0)
	invoke(t, "From: Rowan\nTo: Stella\nRe: stella-111111111111\nSubject: That one too\n\nAnswered.\n",
		"send", "--table", checkout, "--stdin", "--remote", "origin", "--branch", "main", "--attempts", "3").mustCode(t, 0)
	invoke(t, "", advance(checkout, "Rowan")...).mustCode(t, 0).mustContain(t, "stdout", "carrying=0")
	if files := gitIn(t, bare, "ls-tree", "-r", "--name-only", "main"); strings.Contains(files, "from-rowan/OPEN") {
		t.Fatalf("an emptied OPEN list is still on the table:\n%s", files)
	}
	if out := gitIn(t, checkout, "status", "--porcelain"); strings.TrimSpace(out) != "" {
		t.Fatalf("the checkout is dirty after an emptied OPEN list:\n%s", out)
	}
}

// THE SWITCH DAY, end to end. The table this tool was written for had been running by hand
// for months when it adopted the tool, and the first `inbox --as Rowan --full` reported
// 657 notes open. Because the open list is what lets the cursor move, every run after it
// reported the same 657 -- forever, until each was answered or receipted one at a time.
// Nobody was going to do that, and a listing nobody reads hides the one new note in it.
//
// So the line is drawn on a date: the notes behind it are not carried, not listed, and
// counted on one line; the notes in front of it are the inbox. The date goes into the
// cursor, so the run after it does not have to be told again.
func TestTheSwitchDayLineLeavesTheOldNotesOffTheOpenList(t *testing.T) {
	hermetic(t)
	checkout, _ := table(t)
	// Two notes from before the line and one after it, on top of the fixture's two, which
	// are dated 2026-09-07 and are therefore also in front of the line.
	writeFile(t, checkout, "from-stella/2026-08-01T0001Z-old-one-aaaaaaaaaaaa.md",
		"From: Stella\nTo: Rowan\nDate: Sat Aug  1 00:01:00 UTC 2026\nId: stella-aaaaaaaaaaaa\nSubject: One from the months before the tool\n\nThe body.\n")
	writeFile(t, checkout, "from-stella/2026-08-02T0001Z-old-two-bbbbbbbbbbbb.md",
		"From: Stella\nTo: Rowan\nDate: Sun Aug  2 00:01:00 UTC 2026\nId: stella-bbbbbbbbbbbb\nSubject: Another from the months before the tool\n\nThe body.\n")
	gitIn(t, checkout, "add", "-A")
	gitIn(t, checkout, "-c", "user.name=Stella", "-c", "user.email=stella@mas-bandwidth.com", "commit", "-q", "-m", "the months before")
	gitIn(t, checkout, "push", "-q", "origin", "HEAD:refs/heads/main")

	// The switch-day read: full, with the line, advancing.
	r := invoke(t, "", advance(checkout, "Rowan", "--full", "--legacy-before", "2026-09-01")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX LEGACY before=2026-09-01 notes=2").
		mustContain(t, "stdout", "INBOX OK as=Rowan open=2")
	for _, old := range []string{"stella-aaaaaaaaaaaa", "stella-bbbbbbbbbbbb"} {
		if strings.Contains(r.stdout, old) {
			t.Fatalf("a note behind the line was listed one by one:\n%s", r.stdout)
		}
	}
	// The open list is the notes in front of the line and nothing else, and the cursor
	// carries the date so the next run needs no flag.
	if open := read(t, checkout, "from-rowan/OPEN"); strings.Contains(open, "aaaaaaaaaaaa") || strings.Contains(open, "bbbbbbbbbbbb") {
		t.Fatalf("the open list carries a note from behind the line:\n%s", open)
	}
	if cursor := read(t, checkout, "from-rowan/CURSOR"); !strings.Contains(cursor, "legacy=2026-09-01") {
		t.Fatalf("the cursor did not record the line: %s", cursor)
	}

	// The second read, with no flag at all: quiet. It honours the line from the cursor,
	// says so, and names none of the old notes.
	r = invoke(t, "", advance(checkout, "Rowan")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX SCOPE mode=since").
		mustContain(t, "stdout", "INBOX LEGACY before=2026-09-01 notes=0").
		mustContain(t, "stdout", "INBOX OK as=Rowan open=2")
	for _, old := range []string{"stella-aaaaaaaaaaaa", "stella-bbbbbbbbbbbb"} {
		if strings.Contains(r.stdout, old) {
			t.Fatalf("the second read brought the old notes back:\n%s", r.stdout)
		}
	}

	// A line that moves EARLIER would put the notes between the two dates back on the open
	// list, which is a listing the reader has already settled arriving with nothing saying
	// why. It is refused, and the refusal names the read that can honestly do it.
	invoke(t, "", advance(checkout, "Rowan", "--legacy-before", "2026-08-02")...).mustCode(t, 1).
		mustContain(t, "stderr", "moves the line earlier").
		mustContain(t, "stderr", "--full --legacy-before 2026-08-02 --advance")

	// Moving it LATER forgives more and needs no --full: the forgiven set only grows, and
	// nothing comes back.
	invoke(t, "", advance(checkout, "Rowan", "--legacy-before", "2026-09-08")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX LEGACY before=2026-09-08 notes=2").
		mustContain(t, "stdout", "INBOX OK as=Rowan open=0")

	// And --full is the way through, exactly as the refusal said: with the earlier line the
	// old notes are the reader's again, counted at zero because none is behind it.
	r = invoke(t, "", advance(checkout, "Rowan", "--full", "--legacy-before", "2026-08-01")...).mustCode(t, 0).
		mustContain(t, "stdout", "INBOX LEGACY before=2026-08-01 notes=0").
		mustContain(t, "stdout", "INBOX OK as=Rowan open=4")
	if !strings.Contains(r.stdout, "stella-aaaaaaaaaaaa") {
		t.Fatalf("a --full read with an earlier line did not bring the old notes back:\n%s", r.stdout)
	}
}

// The flag itself: a date it cannot read is a bad invocation rather than a guess, on the
// same rule check's is.
func TestInboxRefusesALegacyDateItCannotRead(t *testing.T) {
	checkout, _ := table(t)
	invoke(t, "", "inbox", "--table", checkout, "--as", "Rowan", "--receipt-max-words", "40",
		"--full", "--legacy-before", "last Tuesday").mustCode(t, 2).
		mustContain(t, "stderr", "is not a UTC date")
}
