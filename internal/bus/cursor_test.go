package bus

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCursorRoundTripsAndRefusesWhatIsNotACommit(t *testing.T) {
	root := writeBus(t, nil)
	// A lane with no CURSOR is a reader who has not read yet, and that is not an error.
	got, err := ReadCursor(root, "from-ada")
	if err != nil || got.Commit != "" {
		t.Fatalf("a lane with no cursor: %+v, %v", got, err)
	}
	sha := "0123456789abcdef0123456789abcdef01234567"
	if err := WriteCursor(root, "from-ada", sha, 2, "", at("2026-09-09T12:34:56Z")); err != nil {
		t.Fatal(err)
	}
	got, err = ReadCursor(root, "from-ada")
	if err != nil {
		t.Fatal(err)
	}
	if got.Commit != sha || got.Stamp != "2026-09-09T12:34:56Z" {
		t.Fatalf("round trip gave %+v", got)
	}
	// The count of what the run was carrying comes back with it, and says so: a cursor
	// written before that field existed is Counted false and is trusted rather than
	// compared against an OPEN file it never claimed anything about.
	if !got.Counted || got.Open != 2 {
		t.Fatalf("the carried count did not round trip: %+v", got)
	}
	// The commit is written FIRST, the stamp second, the count third: a cursor's subject
	// is the commit.
	if line := readFile(t, root, CursorPath("from-ada")); line != sha+" 2026-09-09T12:34:56Z open=2\n" {
		t.Fatalf("the cursor line is %q", line)
	}
	// A cursor file anyone with push access could have edited into an option to git is
	// refused where it is READ, before it can become a git argument.
	for _, bad := range []string{
		"--upload-pack=id 2026-09-09T12:34:56Z\n",
		"HEAD 2026-09-09T12:34:56Z\n",
		"0123456789ABCDEF0123456789abcdef01234567 x\n",
		"abc x\n",
		sha + " 2026-09-09T12:34:56Z carrying=2\n",
		sha + " 2026-09-09T12:34:56Z open=-1\n",
		sha + " 2026-09-09T12:34:56Z open=2 and more\n",
	} {
		write(t, root, CursorPath("from-ada"), bad)
		if _, err := ReadCursor(root, "from-ada"); err == nil {
			t.Fatalf("a cursor of %q was accepted", bad)
		}
	}
	// A cursor with two tokens is one written before the count existed. It reads, and says
	// so: nobody claimed anything about an OPEN file, so nothing is compared against one.
	write(t, root, CursorPath("from-ada"), sha+" 2026-09-09T12:34:56Z\n")
	old, err := ReadCursor(root, "from-ada")
	if err != nil || old.Commit != sha || old.Counted || old.Open != 0 {
		t.Fatalf("a cursor written before the count: %+v, %v", old, err)
	}
	// Two lines is a cursor that has been merged badly, and is a refusal rather than a
	// guess about which of the two reads is the real one.
	write(t, root, CursorPath("from-ada"), sha+" 2026-09-09T12:34:56Z\n"+sha+" 2026-09-09T12:35:56Z\n")
	if _, err := ReadCursor(root, "from-ada"); err == nil || !strings.Contains(err.Error(), "one line") {
		t.Fatalf("two cursor lines: %v", err)
	}
}

// OPEN v2: an entry carries the whole line the note prints as, so a later run can list it
// without opening the note. The round trip is the proof that nothing in that line is lost.
func TestOpenListRoundTripsItsDisplayLineAndKeepsItsOrder(t *testing.T) {
	root := writeBus(t, nil)
	if got, err := ReadOpen(root, "from-ada"); err != nil || got != nil {
		t.Fatalf("a lane with no OPEN: %+v, %v", got, err)
	}
	want := []OpenEntry{
		{ID: "bo-abcdef012345", Kind: OpenNote, From: "Bo", Addr: "to",
			Date: "2026-09-07T00:01:00Z", Path: "from-bo/b.md", Subject: "A question about the gate"},
		{ID: "", Kind: OpenNote, From: "Bo", Addr: "cc",
			Path: "from-bo/a legacy note.md", Subject: "Written before there were ids"},
		{ID: "bo-111111111111", Kind: OpenReceipt, Heard: true, From: "Bo", Addr: "to",
			Date: "2026-09-09T13:00:00Z", Path: "from-bo/c.md", Subject: "Heard"},
		{Kind: OpenUnreadable, Path: "from-bo/prose.md"},
	}
	if err := WriteOpen(root, "from-ada", want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadOpen(root, "from-ada")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("read back %d entries, wrote %d", len(got), len(want))
	}
	for i := range want {
		// ORDER, not a set. The order is the order a reader's open notes arrived in, and
		// keeping it is what makes the file's diff between two runs the notes that
		// actually opened and closed.
		if got[i] != want[i] {
			t.Fatalf("entry %d round-tripped as %+v, wrote %+v", i, got[i], want[i])
		}
	}
	raw := readFile(t, root, OpenPath("from-ada"))
	// The version header is the first line, and it is what stops a v1 list being read as a
	// v2 one -- see TestAnOpenListWrittenBeforeV2IsRefusedAtTheRead.
	if !strings.HasPrefix(raw, OpenHeader+"\n") {
		t.Fatalf("the open list does not begin with its version header: %q", raw)
	}
	// A legacy note's id is "-", a path holding a space rides in its own tab-separated
	// field, and every line is the same eight fields.
	for _, line := range strings.Split(strings.TrimRight(raw, "\n"), "\n")[1:] {
		if n := strings.Count(line, "\t"); n != openFields-1 {
			t.Fatalf("the line holds %d tabs, want %d: %q", n, openFields-1, line)
		}
	}
	if !strings.Contains(raw, "-\tnote\t-\tBo\tcc\t-\tfrom-bo/a legacy note.md\t") {
		t.Fatalf("the legacy entry is not <-> <kind> <heard> ... : %q", raw)
	}
	// Nothing open REMOVES the file, rather than leaving the header alone in it: absent and
	// nothing-open are one state on disk, which is what the cursor's count is checked
	// against.
	if err := WriteOpen(root, "from-ada", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(OpenPath("from-ada")))); !os.IsNotExist(err) {
		t.Fatalf("an empty OPEN list left a file behind: %v", err)
	}
	// And every shape that is not an entry is refused where it is read, rather than being
	// guessed at into a listing.
	for _, bad := range []string{
		OpenHeader + "\nno-path\n",
		OpenHeader + "\n-\tnote\t-\tBo\tto\t-\t\t-\n",
		OpenHeader + "\n-\twibble\t-\tBo\tto\t-\tfrom-bo/a.md\t-\n",
		OpenHeader + "\n-\tnote\tyes\tBo\tto\t-\tfrom-bo/a.md\t-\n",
		OpenHeader + "\nbo-nothex\tnote\t-\tBo\tto\t-\tfrom-bo/a.md\t-\n",
	} {
		write(t, root, OpenPath("from-ada"), bad)
		if _, err := ReadOpen(root, "from-ada"); err == nil {
			t.Fatalf("an open list of %q was accepted", bad)
		}
	}
}

// THE VERSION HANDSHAKE, in both directions. A v1 open list -- `<id or -> <path>` -- read as
// v2 would be one field: a path with no kind, no date and no subject, printed as a note
// nobody sent and carried forever. The cursor cannot catch it, because a v1 OPEN beside a
// counted cursor is exactly the state a healthy v2 reader is in. So the file says its own
// version, and a file that does not is refused at the read, naming the repair.
func TestAnOpenListWrittenBeforeV2IsRefusedAtTheRead(t *testing.T) {
	root := writeBus(t, nil)
	write(t, root, OpenPath("from-ada"), "bo-abcdef012345 from-bo/old.md\n")
	_, err := ReadOpen(root, "from-ada")
	if err == nil {
		t.Fatal("a v1 open list was read as a v2 one")
	}
	for _, want := range []string{OpenHeader, "--full --advance"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not name %q: %v", want, err)
		}
	}
	// And a list this version wrote reads back, which is the other half of the handshake.
	if err := WriteOpen(root, "from-ada", []OpenEntry{
		{ID: "bo-abcdef012345", Kind: OpenNote, From: "Bo", Addr: "to", Path: "from-bo/old.md"},
	}); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadOpen(root, "from-ada"); err != nil || len(got) != 1 {
		t.Fatalf("a v2 open list did not read back: %+v, %v", got, err)
	}
}

func TestIndexLineIsFiveFieldsAndCannotBeForged(t *testing.T) {
	// A roster name holding a tab would otherwise make one record look like two, which is
	// the same hole the printed lines close and is closed here the same way.
	e := IndexEntry{
		ID:   "ada-0123456789ab",
		Path: "from-ada/note.md",
		Date: "2026-09-09T12:34:56Z",
		To:   []string{"Bo\tQuill", "Dana"},
		Lane: "from-ada",
	}
	line := IndexLine(e)
	if n := strings.Count(line, "\t"); n != indexFields-1 {
		t.Fatalf("the line holds %d tabs, want %d: %q", n, indexFields-1, line)
	}
	if strings.Contains(line, "Bo\tQuill") {
		t.Fatalf("a tab in a name was not escaped: %q", line)
	}
	// An absent list is "-" and never empty, so no line ever ends in an invisible tab.
	if !strings.HasSuffix(line, "\t-") {
		t.Fatalf("an absent Re was not written as a dash: %q", line)
	}

	root := writeBus(t, nil)
	if err := AppendIndexLine(root, e); err != nil {
		t.Fatal(err)
	}
	entries, err := ReadLaneIndex(root, "from-ada")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].ID != e.ID || entries[0].Path != e.Path || entries[0].Date != e.Date {
		t.Fatalf("round trip gave %+v", entries)
	}
	if len(entries[0].Re) != 0 {
		t.Fatalf("an absent Re read back as %v", entries[0].Re)
	}
	write(t, root, IndexPath("from-ada"), "one\ttwo\n")
	if _, err := ReadLaneIndex(root, "from-ada"); err == nil {
		t.Fatal("a two-field index line was accepted")
	}
}

// The catalogue answers a thread by id without opening a note, and falls back to the
// filesystem for the one thing it cannot hold: a note written before ids existed.
func TestIndexResolvesByIdAndLegacyPath(t *testing.T) {
	root := writeBus(t, map[string]string{
		"from-bo/INDEX":        "bo-abcdef012345\tfrom-bo/new.md\t2026-09-07T00:01:00Z\tAda\t-\n",
		"from-bo/new.md":       "From: Bo\nTo: Ada\nId: bo-abcdef012345\nSubject: s\n\nbody\n",
		"from-bo/old-one.md":   "From: Bo\nTo: Ada\nSubject: s\n\nbody\n",
		"from-ada/RECEIPTS":    "2026-09-09T12:34:56Z bo-abcdef012345\n",
		"from-bo/notanote.txt": "not a note\n",
	})
	c, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := ReadIndex(root, c)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"bo-abcdef012345", "from-bo/new.md", "from-bo/old-one.md"} {
		if !idx.resolves(target) {
			t.Errorf("%q does not resolve", target)
		}
	}
	for _, target := range []string{"bo-000000000000", "from-bo/never.md", "from-bo/notanote.txt", "participants.json", "new"} {
		if idx.resolves(target) {
			t.Errorf("%q resolves and should not", target)
		}
	}
}

// A rebuild writes the catalogue from the notes, which are the record either way. A legacy
// note contributes no line: it is addressed by path, and giving it a catalogue entry keyed
// on an id would be inventing the id.
func TestRebuildLaneIndexFromTheNotes(t *testing.T) {
	root := writeBus(t, map[string]string{
		"from-bo/b.md":   "From: Bo\nTo: Ada\nDate: Mon Sep  7 00:02:00 UTC 2026\nId: bo-111111111111\nRe: new\nSubject: b\n\nbody\n",
		"from-bo/a.md":   "From: Bo\nTo: Ada\nDate: Mon Sep  7 00:01:00 UTC 2026\nId: bo-abcdef012345\nSubject: a\n\nbody\n",
		"from-bo/old.md": "From: Bo\nTo: Ada\nSubject: old\n\nbody\n",
	})
	c, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	tab := loadBus(t, root)
	n, err := RebuildLaneIndex(root, c, tab, "from-bo")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("rebuilt %d entries, want the 2 notes that have ids", n)
	}
	lines := strings.Split(strings.TrimRight(readFile(t, root, IndexPath("from-bo")), "\n"), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "bo-abcdef012345\tfrom-bo/a.md\t") {
		t.Fatalf("the rebuilt catalogue is:\n%s", strings.Join(lines, "\n"))
	}
	// And a rebuilt catalogue agrees with the bus it was built from.
	idx, err := ReadIndex(root, c)
	if err != nil {
		t.Fatal(err)
	}
	if ps := CheckIndex(c, tab, idx); len(ps) != 0 {
		t.Fatalf("a freshly rebuilt catalogue disagrees with the notes: %+v", ps)
	}
}

// InboxSince over a change set, with the OPEN list doing the remembering AND the printing.
// The count is the claim: the notes it parses are the notes that CHANGED, and the one it is
// carrying costs nothing at all.
func TestInboxSinceParsesTheChangeSetAndPrintsTheRestFromOpen(t *testing.T) {
	root := writeBus(t, map[string]string{
		"from-bo/old.md":        "From: Bo\nTo: Ada\nDate: Mon Sep  7 00:01:00 UTC 2026\nId: bo-abcdef012345\nSubject: old\n\nA question?\n",
		"from-bo/new.md":        "From: Bo\nTo: Ada\nDate: Tue Sep  8 00:01:00 UTC 2026\nId: bo-111111111111\nSubject: new\n\nAnother question?\n",
		"from-bo/not-for-me.md": "From: Bo\nTo: Dana\nDate: Tue Sep  8 00:02:00 UTC 2026\nId: bo-222222222222\nSubject: nope\n\nFor Dana.\n",
		"from-ada/RECEIPTS":     "2026-09-09T12:34:56Z bo-abcdef012345\n",
	})
	c, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	me := mustParticipant(t, c, "Ada")
	carried := OpenEntry{ID: "bo-abcdef012345", Kind: OpenNote, Heard: true, From: "Bo", Addr: "to",
		Date: "2026-09-07T00:01:00Z", Path: "from-bo/old.md", Subject: "old"}

	before := NoteParses()
	res, err := InboxSince(root, c, me,
		[]string{"from-bo/new.md", "from-bo/not-for-me.md"},
		[]OpenEntry{carried}, 40, LegacyLine{})
	if err != nil {
		t.Fatal(err)
	}
	// TWO files opened: the two that changed. The one being carried is printed from its own
	// entry, and RECEIPTS is not in the change set so it is not read either.
	if got := NoteParses() - before; got != 2 {
		t.Fatalf("parsed %d notes for 2 changed and 1 open, want 2: the read is not O(new)", got)
	}
	if len(res.Open) != 2 {
		t.Fatalf("open = %+v, want the new one and the carried one", res.Open)
	}
	// Newest first when it is PRINTED, arrival order in the file.
	rows := SortForListing(res.Open)
	if rows[0].Path != "from-bo/new.md" || rows[0].Subject != "new" || rows[0].From != "Bo" {
		t.Fatalf("the listing is not newest first, or a new entry lost its display line: %+v", rows[0])
	}
	if !rows[1].Heard {
		t.Fatalf("the carried entry lost its heard flag: %+v", rows[1])
	}
	if notes, receipts, heard := res.Counts(); notes != 1 || receipts != 0 || heard != 1 {
		t.Fatalf("counts are notes=%d receipts=%d heard=%d, want 1, 0, 1", notes, receipts, heard)
	}
	if res.New != 1 {
		t.Fatalf("new=%d, want 1", res.New)
	}

	// Now a reply of mine, in the change set, closes the carried one -- by id.
	write(t, root, "from-ada/answer.md", "From: Ada\nTo: Bo\nDate: Wed Sep  9 00:01:00 UTC 2026\nId: ada-333333333333\nRe: bo-abcdef012345\nSubject: yes\n\nYes.\n")
	res, err = InboxSince(root, c, me,
		[]string{"from-ada/answer.md"},
		[]OpenEntry{carried, {ID: "bo-111111111111", Kind: OpenNote, From: "Bo", Addr: "to",
			Date: "2026-09-08T00:01:00Z", Path: "from-bo/new.md", Subject: "new"}}, 40, LegacyLine{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Open) != 1 || res.Open[0].ID != "bo-111111111111" {
		t.Fatalf("the answered note is still open: %+v", res.Open)
	}

	// And a LEGACY note is closed the same way, by its path: a reply written before there
	// were ids names the file, and that still has to close the entry that names the file.
	write(t, root, "from-bo/before-ids.md", "From: Bo\nTo: Ada\nSubject: before ids\n\nA question?\n")
	write(t, root, "from-ada/answer.md", "From: Ada\nTo: Bo\nDate: Wed Sep  9 00:02:00 UTC 2026\nId: ada-444444444444\nRe: from-bo/before-ids.md\nSubject: yes\n\nYes.\n")
	res, err = InboxSince(root, c, me, []string{"from-ada/answer.md"},
		[]OpenEntry{{Kind: OpenNote, From: "Bo", Addr: "to", Path: "from-bo/before-ids.md", Subject: "before ids"}},
		40, LegacyLine{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Open) != 0 {
		t.Fatalf("a path-addressed Re did not close the path entry it names: %+v", res.Open)
	}
}

// A receipt reaches an incremental run as MY OWN RECEIPTS FILE in the change set, and what
// it sets is the flag in OPEN -- which is what lets the next run say HEARD without reading
// RECEIPTS at all. Heard is still not answered: the entry stays.
func TestAReceiptInTheChangeSetSetsTheFlagInOpen(t *testing.T) {
	root := writeBus(t, map[string]string{
		"from-bo/old.md":    "From: Bo\nTo: Ada\nDate: Mon Sep  7 00:01:00 UTC 2026\nId: bo-abcdef012345\nSubject: old\n\nA question?\n",
		"from-ada/RECEIPTS": "2026-09-09T12:34:56Z bo-abcdef012345\n",
	})
	c, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	me := mustParticipant(t, c, "Ada")
	carried := OpenEntry{ID: "bo-abcdef012345", Kind: OpenNote, From: "Bo", Addr: "to",
		Date: "2026-09-07T00:01:00Z", Path: "from-bo/old.md", Subject: "old"}

	// RECEIPTS is NOT in the change set: the flag is whatever the entry says, and nothing
	// reads the file. That is the whole saving, and it is also the limit -- a receipt this
	// run cannot see is a receipt the next run applies.
	res, err := InboxSince(root, c, me, nil, []OpenEntry{carried}, 40, LegacyLine{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Open) != 1 || res.Open[0].Heard {
		t.Fatalf("a receipt outside the change set was read anyway: %+v", res.Open)
	}
	// In the change set, it sets the flag, and the entry stays open.
	before := NoteParses()
	res, err = InboxSince(root, c, me, []string{"from-ada/RECEIPTS"}, []OpenEntry{carried}, 40, LegacyLine{})
	if err != nil {
		t.Fatal(err)
	}
	if got := NoteParses() - before; got != 0 {
		t.Fatalf("reading RECEIPTS parsed %d notes; it is a line scan", got)
	}
	if len(res.Open) != 1 || !res.Open[0].Heard {
		t.Fatalf("a receipt in the change set did not set the heard flag: %+v", res.Open)
	}
	if _, _, heard := res.Counts(); heard != 1 {
		t.Fatalf("a heard entry is not counted as heard: %+v", res.Open)
	}
}

// A file this tool cannot read is CARRIED as an unreadable entry until it parses or is
// receipted, and is named on every run in between. Dropping it after one mention is how the
// first version lost it: a `--full` read said so once, and no incremental run ever did again.
func TestAnUnreadableFileIsCarriedAndReChecked(t *testing.T) {
	root := writeBus(t, map[string]string{
		"from-bo/prose.md": "Ada, this is prose and no header at all.\n\nMore prose.\n",
	})
	c, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	me := mustParticipant(t, c, "Ada")

	res, err := InboxSince(root, c, me, []string{"from-bo/prose.md"}, nil, 40, LegacyLine{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Unreadable) != 1 || len(res.Open) != 1 || res.Open[0].Kind != OpenUnreadable {
		t.Fatalf("an unreadable file was not carried: unreadable=%d open=%+v", len(res.Unreadable), res.Open)
	}
	// The run after it, with NOTHING in the change set, still names it -- and that costs one
	// parse, for this entry and nobody else's note.
	before := NoteParses()
	res, err = InboxSince(root, c, me, nil, res.Open, 40, LegacyLine{})
	if err != nil {
		t.Fatal(err)
	}
	if got := NoteParses() - before; got != 1 {
		t.Fatalf("re-checking one unreadable entry parsed %d notes, want 1", got)
	}
	if len(res.Unreadable) != 1 || len(res.Open) != 1 {
		t.Fatalf("the unreadable entry was dropped in silence: %+v", res)
	}
	// Somebody fixes the file. It becomes an ordinary entry, with its display line.
	write(t, root, "from-bo/prose.md", "From: Bo\nTo: Ada\nDate: Mon Sep  7 00:01:00 UTC 2026\nId: bo-abcdef012345\nSubject: Now it parses\n\nA question?\n")
	res, err = InboxSince(root, c, me, nil, res.Open, 40, LegacyLine{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Unreadable) != 0 || len(res.Open) != 1 || res.Open[0].Kind != OpenNote || res.Open[0].Subject != "Now it parses" {
		t.Fatalf("a file that now parses did not become an ordinary entry: %+v", res)
	}
	// And a file that never parses leaves the list when it is RECEIPTED, which is how a
	// reader says "I have seen this" about a file with no id to answer.
	write(t, root, "from-bo/prose.md", "Ada, prose again.\n\nMore prose.\n")
	write(t, root, "from-ada/RECEIPTS", "2026-09-09T12:34:56Z from-bo/prose.md\n")
	res, err = InboxSince(root, c, me, []string{"from-bo/prose.md"}, nil, 40, LegacyLine{})
	if err != nil {
		t.Fatal(err)
	}
	res, err = InboxSince(root, c, me, []string{"from-ada/RECEIPTS"}, res.Open, 40, LegacyLine{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Open) != 0 || len(res.Unreadable) != 0 {
		t.Fatalf("a receipted unreadable file is still carried: %+v", res)
	}
}

// An open note whose file has gone is CARRIED, printed from the snapshot in OPEN, until a
// --full read rebuilds the list without it. That is a deliberate limit and not an oversight:
// a deletion is not in the change set at all, and finding one would cost a stat per open
// note, which is the O(open) this design exists to remove. The direction is a stale line, not
// a lost note.
func TestAnOpenNoteWhoseFileVanishedIsCarriedUntilAFullRead(t *testing.T) {
	root := writeBus(t, nil)
	c, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	me := mustParticipant(t, c, "Ada")
	gone := OpenEntry{ID: "bo-abcdef012345", Kind: OpenNote, From: "Bo", Addr: "to",
		Date: "2026-09-07T00:01:00Z", Path: "from-bo/gone.md", Subject: "gone"}
	before := NoteParses()
	res, err := InboxSince(root, c, me, nil, []OpenEntry{gone}, 40, LegacyLine{})
	if err != nil {
		t.Fatal(err)
	}
	if got := NoteParses() - before; got != 0 {
		t.Fatalf("a carried entry cost %d parses, want 0", got)
	}
	if len(res.Open) != 1 {
		t.Fatalf("the entry was dropped without anything looking on the bus: %+v", res.Open)
	}
	// The full walk is what settles it: the note is not on the bus, so it is not on the
	// list the full read writes.
	tab := loadBus(t, root)
	if entries := OpenFromFull(tab.Inbox(me, 40), tab.Unreadable(me.Lane)); len(entries) != 0 {
		t.Fatalf("a full read carried a note that is not on the bus: %+v", entries)
	}
}

func readFile(t *testing.T, root, path string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// THE LIMIT OF CLOSING FROM THE CHANGE SET ALONE, stated as a test so nobody has to
// discover it. A reply of mine closes its thread while it is in the change set. Once it is
// behind my cursor this run cannot see it -- `answered` is built from what CHANGED and from
// nothing else, which is what makes the read O(new) -- so an EDIT to the note it answered
// puts that note back in my open list and I am shown it again.
//
// The direction is the whole point: re-show, never loss. A reader is asked twice, which is
// tiresome; a reader is never told a note is answered when it is not, and never loses one.
// The settlement is a `--full` read, which derives the list from the whole bus, where the
// reply is a note like any other.
//
// This is a WIDENING of a limit that already existed: before OPEN v2 the catalogue was read
// whole on every run, so a reply `send` wrote kept closing its thread and only a HAND-written
// one had this shape. The trade is named in SPEC.md's cost paragraph: one line scan of my
// whole history, on every run, forever, against being asked twice about a note somebody
// edited after I answered it.
func TestAReplyBehindTheCursorReShowsTheNoteAndAFullReadSettlesIt(t *testing.T) {
	files := map[string]string{
		"from-bo/old.md": "From: Bo\nTo: Ada\nDate: Mon Sep  7 00:01:00 UTC 2026\nId: bo-abcdef012345\nSubject: old\n\nA question?\n",
		"from-ada/answer.md": "From: Ada\nTo: Bo\nDate: Mon Sep  7 01:00:00 UTC 2026\nId: ada-333333333333\n" +
			"Re: bo-abcdef012345\nSubject: yes\n\nYes.\n",
		"from-ada/INDEX": "ada-333333333333\tfrom-ada/answer.md\t2026-09-07T01:00:00Z\tBo\tbo-abcdef012345\n",
	}
	root := writeBus(t, files)
	c, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	me := mustParticipant(t, c, "Ada")

	// While the reply is in the change set it closes the thread.
	res, err := InboxSince(root, c, me, []string{"from-bo/old.md", "from-ada/answer.md"}, nil, 40, LegacyLine{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Open) != 0 {
		t.Fatalf("a reply in the change set did not close its thread: %+v", res.Open)
	}

	// Once it is behind the cursor, the edited note is shown again. Shown -- not lost.
	res, err = InboxSince(root, c, me, []string{"from-bo/old.md"}, nil, 40, LegacyLine{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Open) != 1 || res.Open[0].ID != "bo-abcdef012345" {
		t.Fatalf("the re-show is not what the docs say it is: %+v", res.Open)
	}

	// And a full read settles it: the reply is on the bus, so the note is answered and the
	// list a `--full --advance` writes does not hold it.
	tab := loadBus(t, root)
	if entries := OpenFromFull(tab.Inbox(me, 40), tab.Unreadable(me.Lane)); len(entries) != 0 {
		t.Fatalf("a full read did not settle an answered note: %+v", entries)
	}

	// The catalogue warning still says what a missing INDEX line costs, because the
	// catalogue is still what `check --since` resolves a thread through.
	if err := os.Remove(filepath.Join(root, "from-ada", "INDEX")); err != nil {
		t.Fatal(err)
	}
	idx, err := ReadIndex(root, c)
	if err != nil {
		t.Fatal(err)
	}
	var found string
	for _, p := range CheckIndex(c, tab, idx) {
		if p.Where == "from-ada/answer.md" {
			found = p.Reason
		}
	}
	if !strings.Contains(found, "--rebuild-index") {
		t.Fatalf("the warning does not name the repair: %q", found)
	}
}

// A LANE STATE FILE IS REPLACED, NEVER REWRITTEN IN PLACE. Every file this writes is a
// file another run refuses on -- a CURSOR whose commit will not read stops a reader, an
// OPEN cut in half stops them, an INDEX cut in half resolves a thread to nothing -- and an
// in-place write makes all three reachable by killing the tool between the truncate and
// the write.
//
// The assertion is the one that can be made without killing anything: a descriptor opened
// BEFORE the write still reads the old bytes afterwards. That is only true of a rename --
// an in-place write would show the new content, or a truncated half of it, through the
// same descriptor -- so it is a direct test of the mechanism and not of a symptom.
func TestALaneStateFileIsReplacedByRenameAndLeavesNoPartialFile(t *testing.T) {
	root := writeBus(t, nil)
	const lane = "from-ada"
	first := "1111111111111111111111111111111111111111"
	if err := WriteCursor(root, lane, first, 1, "", at("2026-09-09T12:00:00Z")); err != nil {
		t.Fatal(err)
	}
	full := filepath.Join(root, filepath.FromSlash(CursorPath(lane)))
	// What the path names before the write, taken from an open handle so it is the file's
	// own identity and not a second look at the path.
	before := identityOf(t, full)
	// And a descriptor held across the write, where a rename can replace a file somebody
	// has open. See openHeld: on Windows it cannot, and the test's own handle would be what
	// made the write fail.
	held, holdable := openHeld(t, full)
	if holdable {
		defer held.Close()
	}

	second := "2222222222222222222222222222222222222222"
	if err := WriteCursor(root, lane, second, 7, "", at("2026-09-09T13:00:00Z")); err != nil {
		t.Fatal(err)
	}
	// The path names a DIFFERENT FILE than it did, which an in-place write cannot do and a
	// rename cannot avoid. This is the half of the assertion every platform can make.
	if after := identityOf(t, full); os.SameFile(before, after) {
		t.Fatal("the path names the same file it named before the write; the cursor was rewritten in place, so a kill mid-write would leave neither the old file nor the new one")
	}
	if holdable {
		old, err := io.ReadAll(held)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(old), first) {
			t.Fatalf("the descriptor opened before the write sees %q; the file was rewritten in place, so a kill mid-write would leave neither the old file nor the new one", string(old))
		}
	}
	// And the path itself holds the new cursor, whole: one line, four tokens, readable.
	got, err := ReadCursor(root, lane)
	if err != nil {
		t.Fatal(err)
	}
	if got.Commit != second || got.Open != 7 {
		t.Fatalf("the new cursor reads as %+v", got)
	}
	// The temporary is gone. It is a step in a write, never a file on the bus.
	entries, err := os.ReadDir(filepath.Join(root, lane))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), TempSuffix) {
			t.Fatalf("%s is still in the lane after the write", e.Name())
		}
	}
}

// And if a run IS killed between the write and the rename, what it leaves behind is a file
// the lane walk steps over rather than a stray that fails the whole check. The old file is
// still the file, and the next write replaces it.
func TestAStrandedTemporaryIsIgnoredByTheLaneWalk(t *testing.T) {
	files := fixture()
	files["from-bo/CURSOR"] = "1111111111111111111111111111111111111111 2026-09-09T12:00:00Z open=0\n"
	files["from-bo/CURSOR"+TempSuffix] = "22222222222222222222222222222222222222"
	files["from-bo/OPEN"+TempSuffix] = "bo-abcdef012345 from-bo/half-a-l"
	tab := loadBus(t, writeBus(t, files))
	if n := len(tab.Notes); n != len(fixture()) {
		t.Fatalf("the lane walk read %d notes, want %d: a temporary was read as a note", n, len(fixture()))
	}
	for _, p := range tab.Check() {
		if strings.Contains(p.Where, TempSuffix) {
			t.Fatalf("a stranded temporary is a check finding: %s: %s", p.Where, p.Reason)
		}
	}
	// A file that merely ends in .tmp is NOT covered: only the four state files' own
	// temporaries are, and a lane is still a lane that holds notes and nothing else.
	files["from-bo/notes.tmp"] = "not a state file\n"
	stray := loadBus(t, writeBus(t, files))
	found := false
	for _, p := range stray.Check() {
		if p.Where == "from-bo/notes.tmp" {
			found = true
		}
	}
	if !found {
		t.Fatal("a stray notes.tmp was stepped over; the tolerance is for the four state files and nothing else")
	}
}

// The cursor's fourth token: the switch-day line the reader read under. It is in the
// cursor rather than in a flag because a line that has to be retyped on every run is a
// line that will be forgotten on one, and the run that forgets it opens every note behind
// it at once.
func TestTheCursorCarriesTheLegacyLineAndStaysReadableWithoutOne(t *testing.T) {
	root := writeBus(t, nil)
	sha := "0123456789abcdef0123456789abcdef01234567"
	if err := WriteCursor(root, "from-ada", sha, 2, "2026-09-01", at("2026-09-09T12:34:56Z")); err != nil {
		t.Fatal(err)
	}
	if line := readFile(t, root, CursorPath("from-ada")); line != sha+" 2026-09-09T12:34:56Z open=2 legacy=2026-09-01\n" {
		t.Fatalf("the cursor line is %q", line)
	}
	got, err := ReadCursor(root, "from-ada")
	if err != nil {
		t.Fatal(err)
	}
	if got.Legacy != "2026-09-01" || !got.LegacyBefore().Equal(at("2026-09-01T00:00:00Z")) {
		t.Fatalf("the legacy line did not round trip: %+v", got)
	}
	// No line is no token, which is exactly the shape of every cursor written before the
	// line existed: three tokens, read the same way, LegacyBefore zero.
	if err := WriteCursor(root, "from-ada", sha, 2, "", at("2026-09-09T12:34:56Z")); err != nil {
		t.Fatal(err)
	}
	if line := readFile(t, root, CursorPath("from-ada")); strings.Contains(line, "legacy=") {
		t.Fatalf("a cursor with no line still wrote one: %q", line)
	}
	got, err = ReadCursor(root, "from-ada")
	if err != nil || got.Legacy != "" || !got.LegacyBefore().IsZero() {
		t.Fatalf("a cursor with no legacy line: %+v, %v", got, err)
	}
	// The tokens are read by their PREFIX and not by their position, so a cursor may carry
	// the line without the count and the two may arrive in either order.
	for _, line := range []string{
		sha + " 2026-09-09T12:34:56Z legacy=2026-09-01\n",
		sha + " 2026-09-09T12:34:56Z legacy=2026-09-01 open=2\n",
	} {
		write(t, root, CursorPath("from-ada"), line)
		got, err := ReadCursor(root, "from-ada")
		if err != nil || got.Legacy != "2026-09-01" {
			t.Fatalf("cursor %q read as %+v, %v", line, got, err)
		}
	}
	// An INSTANT token reads, and reads as itself and not as its day: this is the shape a
	// bus switching TODAY writes, and a cursor holding one has to survive every later run.
	write(t, root, CursorPath("from-ada"), sha+" 2026-09-09T12:34:56Z open=2 legacy=2026-09-09T18:07:00Z\n")
	got, err = ReadCursor(root, "from-ada")
	if err != nil || got.Legacy != "2026-09-09T18:07:00Z" || !got.LegacyBefore().Equal(at("2026-09-09T18:07:00Z")) {
		t.Fatalf("a cursor with an instant line read as %+v, %v", got, err)
	}
	if line := got.LegacyLine(); line.Text != "2026-09-09T18:07:00Z" || !line.Before.Equal(at("2026-09-09T18:07:00Z")) {
		t.Fatalf("the cursor's line was not carried as given: %+v", line)
	}
	// And a line that is neither shape is a refusal at the READ, like every other value on
	// this line that becomes a decision later. An offset is not a UTC instant.
	for _, bad := range []string{
		sha + " 2026-09-09T12:34:56Z legacy=last-tuesday\n",
		sha + " 2026-09-09T12:34:56Z legacy=2026-09-01T00:00:00+10:00\n",
		sha + " 2026-09-09T12:34:56Z legacy=2026-09-01T18:07Z\n",
		sha + " 2026-09-09T12:34:56Z legacy=\n",
	} {
		write(t, root, CursorPath("from-ada"), bad)
		if _, err := ReadCursor(root, "from-ada"); err == nil {
			t.Fatalf("a cursor of %q was accepted", bad)
		}
	}
	// A line WriteCursor cannot write is refused before it reaches the file, so a cursor on
	// the bus is never a date nobody can read -- and an instant it CAN write goes down
	// exactly as given rather than rounded to its day.
	if err := WriteCursor(root, "from-ada", sha, 0, "last Tuesday", at("2026-09-09T12:34:56Z")); err == nil {
		t.Fatal("WriteCursor wrote a legacy line that is not a date")
	}
	if err := WriteCursor(root, "from-ada", sha, 0, "2026-09-09T18:07:00Z", at("2026-09-09T12:34:56Z")); err != nil {
		t.Fatal(err)
	}
	if line := readFile(t, root, CursorPath("from-ada")); !strings.Contains(line, "legacy=2026-09-09T18:07:00Z") {
		t.Fatalf("an instant line was not written as given: %q", line)
	}
}

// The bug a family of five found in their first hour: a line drawn at TOMORROW's date made
// every note they wrote that afternoon legacy, because a date is midnight at its START and
// midnight tomorrow is after everything written today. The line is a MOMENT, so an instant
// draws it where they actually switched -- and a date still means exactly what it always
// meant.
func TestTheLegacyLineIsAMomentSoTheSwitchDayIsNotAllLegacy(t *testing.T) {
	files := fixture()
	files["from-bo/2026-09-09T1806Z-a-minute-before-the-switch.md"] = `From: Bo
To: Ada
Date: Wed Sep  9 18:06:00 UTC 2026
Id: bo-bbbbbbbbbbbb
Subject: Sent a minute before the switch

The body.
`
	files["from-bo/2026-09-09T1808Z-a-minute-after-the-switch.md"] = `From: Bo
To: Ada
Date: Wed Sep  9 18:08:00 UTC 2026
Id: bo-cccccccccccc
Subject: Sent a minute after the switch

The body.
`
	root := writeBus(t, files)
	c, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	me := mustParticipant(t, c, "Ada")
	changed := []string{
		"from-bo/2026-09-09T1806Z-a-minute-before-the-switch.md",
		"from-bo/2026-09-09T1808Z-a-minute-after-the-switch.md",
	}

	// The switch happened at 18:07. The note a minute before it is behind the line and the
	// note a minute after it is on the open list, on the same afternoon and the same date.
	instant, err := NewLegacyLine("2026-09-09T18:07:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if instant.Text != "2026-09-09T18:07:00Z" || !instant.Before.Equal(at("2026-09-09T18:07:00Z")) {
		t.Fatalf("an instant line parsed as %+v", instant)
	}
	res, err := InboxSince(root, c, me, changed, nil, 40, instant)
	if err != nil {
		t.Fatal(err)
	}
	if res.Legacy != 1 || len(res.Open) != 1 || !strings.Contains(res.Open[0].Path, "1808Z") {
		t.Fatalf("the line at 18:07 gave legacy=%d open=%+v, want the 18:06 note behind it and the 18:08 note listed", res.Legacy, res.Open)
	}
	// The same rule through the full walk, which is the read that WRITES the open list every
	// later run inherits -- and is the read that reported zero on the real bus.
	tab := loadBus(t, root)
	all := OpenFromFull(tab.Inbox(me, 40), nil)
	keep, covered := SplitLegacy(all, instant)
	// Everything on this bus predates the switch except the 18:08 note, so exactly one
	// entry survives and it is that one.
	if covered != len(all)-1 {
		t.Fatalf("the full walk left %d of %d notes off, want all but the 18:08 note", covered, len(all))
	}
	for _, e := range keep {
		if strings.Contains(e.Path, "1806Z") {
			t.Fatal("the full walk carried the note from before the switch")
		}
	}
	var sawAfter bool
	for _, e := range keep {
		sawAfter = sawAfter || strings.Contains(e.Path, "1808Z")
	}
	if !sawAfter {
		t.Fatal("the full walk left off a note sent AFTER the line, which is the bug this closes")
	}

	// A DATE is midnight at its start, unchanged: tomorrow's date takes both of today's
	// notes, which is exactly what the family saw and is the honest reading of a date.
	tomorrow, err := NewLegacyLine("2026-09-10")
	if err != nil {
		t.Fatal(err)
	}
	if !tomorrow.Before.Equal(at("2026-09-10T00:00:00Z")) {
		t.Fatalf("a date line is not midnight at its start: %+v", tomorrow)
	}
	if _, covered := SplitLegacy(all, tomorrow); covered != len(all) {
		t.Fatalf("tomorrow's date left %d of %d notes off, want all of them", covered, len(all))
	}
	// And TODAY's date is midnight this morning, so both of today's notes are carried.
	today, err := NewLegacyLine("2026-09-09")
	if err != nil {
		t.Fatal(err)
	}
	keep, _ = SplitLegacy(all, today)
	var before, after bool
	for _, e := range keep {
		before = before || strings.Contains(e.Path, "1806Z")
		after = after || strings.Contains(e.Path, "1808Z")
	}
	if !before || !after {
		t.Fatalf("today's date did not behave as midnight at its start: %+v", keep)
	}

	// The two shapes, and only the two: an instant must be UTC and to the second.
	for _, bad := range []string{"last Tuesday", "2026-09-09T18:07:00+10:00", "2026-09-09T18:07Z", "2026-09-09 18:07:00Z", ""} {
		if _, err := NewLegacyLine(bad); err == nil {
			t.Fatalf("NewLegacyLine accepted %q", bad)
		}
	}
}

// The line applied to a change set and to a full walk, which are the two reads a bus
// gets, kept in one rule so they cannot draw it differently.
func TestTheLegacyLineLeavesOldNotesOffTheOpenListInBothReads(t *testing.T) {
	files := fixture()
	files["from-bo/2026-08-01T0001Z-before-the-line.md"] = `From: Bo
To: Ada
Date: Sat Aug  1 00:01:00 UTC 2026
Id: bo-aaaaaaaaaaaa
Subject: A note from before the bus adopted the tool

The body.
`
	root := writeBus(t, files)
	c, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	me := mustParticipant(t, c, "Ada")
	line := LegacyLine{Before: at("2026-09-01T00:00:00Z")}

	// The incremental read: the old note is in the change set, is not carried, and is
	// counted rather than listed.
	res, err := InboxSince(root, c, me,
		[]string{"from-bo/2026-08-01T0001Z-before-the-line.md", "from-bo/2026-09-07T0001Z-a-question-abcdef012345.md"},
		nil, 40, line)
	if err != nil {
		t.Fatal(err)
	}
	if res.Legacy != 1 {
		t.Fatalf("counted %d notes behind the line, want 1", res.Legacy)
	}
	for _, e := range res.Open {
		if strings.Contains(e.Path, "before-the-line") {
			t.Fatal("a note behind the line is carried on the open list")
		}
	}
	if len(res.Open) != 1 {
		t.Fatalf("open = %+v, want only the note on the new side of the line", res.Open)
	}
	// A note already ON the open list from before the line was drawn leaves it too: the
	// rule is about the note's date and not about how it arrived -- and the date is read
	// from the ENTRY, so no note is opened to draw the line over a carried one.
	before := NoteParses()
	res, err = InboxSince(root, c, me, nil,
		[]OpenEntry{{ID: "bo-aaaaaaaaaaaa", Kind: OpenNote, From: "Bo", Addr: "to",
			Date: "2026-08-01T00:01:00Z", Path: "from-bo/2026-08-01T0001Z-before-the-line.md",
			Subject: "A note from before the bus adopted the tool"}}, 40, line)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Open) != 0 || res.Legacy != 1 {
		t.Fatalf("a carried note behind the line stayed: open=%+v legacy=%d", res.Open, res.Legacy)
	}
	if got := NoteParses() - before; got != 0 {
		t.Fatalf("drawing the line over a carried entry parsed %d notes, want 0", got)
	}
	// An entry with NO recorded date still falls on the same side as its note, because the
	// day in its filename is read exactly as Note.legacyDay reads it.
	_, covered := SplitLegacy([]OpenEntry{{Kind: OpenNote, Path: "from-bo/2026-08-01T0001Z-before-the-line.md"}}, line)
	if covered != 1 {
		t.Fatalf("an entry dated only by its filename was not covered by the line")
	}

	// The full read: the same rule, over the whole bus, through the same function.
	tab := loadBus(t, root)
	keep, covered := SplitLegacy(OpenFromFull(tab.Inbox(me, 40), tab.Unreadable(me.Lane)), line)
	if covered != 1 {
		t.Fatalf("the full walk left %d notes off, want 1", covered)
	}
	for _, e := range keep {
		if strings.Contains(e.Path, "before-the-line") {
			t.Fatal("the full walk carried a note behind the line")
		}
	}
	// With no line at all, nothing is left off and the listing is what it always was.
	if _, covered := SplitLegacy(OpenFromFull(tab.Inbox(me, 40), nil), LegacyLine{}); covered != 0 {
		t.Fatalf("a run with no line left %d notes off", covered)
	}
	// A note whose date cannot be read AT ALL is never behind the line: a file that cannot
	// say when it was written cannot claim to predate anything, and the safe direction for
	// a note nobody can date is to carry it.
	files["from-bo/undated.md"] = "From: Bo\nTo: Ada\nSubject: No date line, and no minute in the filename\n\nThe body.\n"
	undated := loadBus(t, writeBus(t, files))
	all := OpenFromFull(undated.Inbox(me, 40), nil)
	_, covered = SplitLegacy(all, LegacyLine{Before: at("2030-01-01T00:00:00Z")})
	if covered != len(all)-1 {
		t.Fatalf("an undated note was taken as older than the line: %d of %d left off", covered, len(all))
	}
}
