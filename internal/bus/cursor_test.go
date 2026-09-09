package bus

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCursorRoundTripsAndRefusesWhatIsNotACommit(t *testing.T) {
	root := writeTable(t, nil)
	// A lane with no CURSOR is a reader who has not read yet, and that is not an error.
	got, err := ReadCursor(root, "from-rowan")
	if err != nil || got.Commit != "" {
		t.Fatalf("a lane with no cursor: %+v, %v", got, err)
	}
	sha := "0123456789abcdef0123456789abcdef01234567"
	if err := WriteCursor(root, "from-rowan", sha, 2, "", at("2026-09-09T12:34:56Z")); err != nil {
		t.Fatal(err)
	}
	got, err = ReadCursor(root, "from-rowan")
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
	if line := readFile(t, root, CursorPath("from-rowan")); line != sha+" 2026-09-09T12:34:56Z open=2\n" {
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
		write(t, root, CursorPath("from-rowan"), bad)
		if _, err := ReadCursor(root, "from-rowan"); err == nil {
			t.Fatalf("a cursor of %q was accepted", bad)
		}
	}
	// A cursor with two tokens is one written before the count existed. It reads, and says
	// so: nobody claimed anything about an OPEN file, so nothing is compared against one.
	write(t, root, CursorPath("from-rowan"), sha+" 2026-09-09T12:34:56Z\n")
	old, err := ReadCursor(root, "from-rowan")
	if err != nil || old.Commit != sha || old.Counted || old.Open != 0 {
		t.Fatalf("a cursor written before the count: %+v, %v", old, err)
	}
	// Two lines is a cursor that has been merged badly, and is a refusal rather than a
	// guess about which of the two reads is the real one.
	write(t, root, CursorPath("from-rowan"), sha+" 2026-09-09T12:34:56Z\n"+sha+" 2026-09-09T12:35:56Z\n")
	if _, err := ReadCursor(root, "from-rowan"); err == nil || !strings.Contains(err.Error(), "one line") {
		t.Fatalf("two cursor lines: %v", err)
	}
}

func TestOpenListRoundTripsAndKeepsItsOrder(t *testing.T) {
	root := writeTable(t, nil)
	if got, err := ReadOpen(root, "from-rowan"); err != nil || got != nil {
		t.Fatalf("a lane with no OPEN: %+v, %v", got, err)
	}
	want := []OpenEntry{
		{ID: "stella-abcdef012345", Path: "from-stella/b.md"},
		{ID: "", Path: "from-stella/a legacy note.md"},
		{ID: "stella-111111111111", Path: "from-stella/c.md"},
	}
	if err := WriteOpen(root, "from-rowan", want); err != nil {
		t.Fatal(err)
	}
	got, err := ReadOpen(root, "from-rowan")
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
	// A legacy note is written "-" and its path may hold spaces, so the first token is the
	// id and the REST of the line is the path -- the same shape a receipt line has.
	if line := readFile(t, root, OpenPath("from-rowan")); !strings.Contains(line, "- from-stella/a legacy note.md\n") {
		t.Fatalf("the legacy entry is %q", line)
	}
	// Nothing open REMOVES the file, rather than leaving a zero-length second spelling of
	// the state a lane starts in.
	if err := WriteOpen(root, "from-rowan", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(OpenPath("from-rowan")))); !os.IsNotExist(err) {
		t.Fatalf("an empty OPEN list left a file behind: %v", err)
	}
	write(t, root, OpenPath("from-rowan"), "no-path\n")
	if _, err := ReadOpen(root, "from-rowan"); err == nil {
		t.Fatal("an entry with no path was accepted")
	}
}

func TestIndexLineIsFiveFieldsAndCannotBeForged(t *testing.T) {
	// A roster name holding a tab would otherwise make one record look like two, which is
	// the same hole the printed lines close and is closed here the same way.
	e := IndexEntry{
		ID:   "rowan-0123456789ab",
		Path: "from-rowan/note.md",
		Date: "2026-09-09T12:34:56Z",
		To:   []string{"Stella\tCodex", "Glenn"},
		Lane: "from-rowan",
	}
	line := IndexLine(e)
	if n := strings.Count(line, "\t"); n != indexFields-1 {
		t.Fatalf("the line holds %d tabs, want %d: %q", n, indexFields-1, line)
	}
	if strings.Contains(line, "Stella\tCodex") {
		t.Fatalf("a tab in a name was not escaped: %q", line)
	}
	// An absent list is "-" and never empty, so no line ever ends in an invisible tab.
	if !strings.HasSuffix(line, "\t-") {
		t.Fatalf("an absent Re was not written as a dash: %q", line)
	}

	root := writeTable(t, nil)
	if err := AppendIndexLine(root, e); err != nil {
		t.Fatal(err)
	}
	entries, err := ReadLaneIndex(root, "from-rowan")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].ID != e.ID || entries[0].Path != e.Path || entries[0].Date != e.Date {
		t.Fatalf("round trip gave %+v", entries)
	}
	if len(entries[0].Re) != 0 {
		t.Fatalf("an absent Re read back as %v", entries[0].Re)
	}
	write(t, root, IndexPath("from-rowan"), "one\ttwo\n")
	if _, err := ReadLaneIndex(root, "from-rowan"); err == nil {
		t.Fatal("a two-field index line was accepted")
	}
}

// The catalogue answers a thread by id without opening a note, and falls back to the
// filesystem for the one thing it cannot hold: a note written before ids existed.
func TestIndexResolvesByIdAndLegacyPath(t *testing.T) {
	root := writeTable(t, map[string]string{
		"from-stella/INDEX":        "stella-abcdef012345\tfrom-stella/new.md\t2026-09-07T00:01:00Z\tRowan\t-\n",
		"from-stella/new.md":       "From: Stella\nTo: Rowan\nId: stella-abcdef012345\nSubject: s\n\nbody\n",
		"from-stella/old-one.md":   "From: Stella\nTo: Rowan\nSubject: s\n\nbody\n",
		"from-rowan/RECEIPTS":      "2026-09-09T12:34:56Z stella-abcdef012345\n",
		"from-stella/notanote.txt": "not a note\n",
	})
	c, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := ReadIndex(root, c)
	if err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"stella-abcdef012345", "from-stella/new.md", "from-stella/old-one.md"} {
		if !idx.resolves(target) {
			t.Errorf("%q does not resolve", target)
		}
	}
	for _, target := range []string{"stella-000000000000", "from-stella/never.md", "from-stella/notanote.txt", "participants.json", "new"} {
		if idx.resolves(target) {
			t.Errorf("%q resolves and should not", target)
		}
	}
}

// A rebuild writes the catalogue from the notes, which are the record either way. A legacy
// note contributes no line: it is addressed by path, and giving it a catalogue entry keyed
// on an id would be inventing the id.
func TestRebuildLaneIndexFromTheNotes(t *testing.T) {
	root := writeTable(t, map[string]string{
		"from-stella/b.md":   "From: Stella\nTo: Rowan\nDate: Mon Sep  7 00:02:00 UTC 2026\nId: stella-111111111111\nRe: new\nSubject: b\n\nbody\n",
		"from-stella/a.md":   "From: Stella\nTo: Rowan\nDate: Mon Sep  7 00:01:00 UTC 2026\nId: stella-abcdef012345\nSubject: a\n\nbody\n",
		"from-stella/old.md": "From: Stella\nTo: Rowan\nSubject: old\n\nbody\n",
	})
	c, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	tab := loadTable(t, root)
	n, err := RebuildLaneIndex(root, c, tab, "from-stella")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("rebuilt %d entries, want the 2 notes that have ids", n)
	}
	lines := strings.Split(strings.TrimRight(readFile(t, root, IndexPath("from-stella")), "\n"), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], "stella-abcdef012345\tfrom-stella/a.md\t") {
		t.Fatalf("the rebuilt catalogue is:\n%s", strings.Join(lines, "\n"))
	}
	// And a rebuilt catalogue agrees with the table it was built from.
	idx, err := ReadIndex(root, c)
	if err != nil {
		t.Fatal(err)
	}
	if ps := CheckIndex(c, tab, idx); len(ps) != 0 {
		t.Fatalf("a freshly rebuilt catalogue disagrees with the notes: %+v", ps)
	}
}

// InboxSince over a change set, with the OPEN list doing the remembering.
func TestInboxSinceUsesOpenAndTouchesNothingElse(t *testing.T) {
	root := writeTable(t, map[string]string{
		"from-stella/old.md":        "From: Stella\nTo: Rowan\nDate: Mon Sep  7 00:01:00 UTC 2026\nId: stella-abcdef012345\nSubject: old\n\nA question?\n",
		"from-stella/new.md":        "From: Stella\nTo: Rowan\nDate: Tue Sep  8 00:01:00 UTC 2026\nId: stella-111111111111\nSubject: new\n\nAnother question?\n",
		"from-stella/not-for-me.md": "From: Stella\nTo: Glenn\nDate: Tue Sep  8 00:02:00 UTC 2026\nId: stella-222222222222\nSubject: nope\n\nFor Glenn.\n",
		"from-rowan/RECEIPTS":       "2026-09-09T12:34:56Z stella-abcdef012345\n",
	})
	c, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	me := mustParticipant(t, c, "Rowan")

	before := NoteParses()
	res, err := InboxSince(root, c, me,
		[]string{"from-stella/new.md", "from-stella/not-for-me.md"},
		[]OpenEntry{{ID: "stella-abcdef012345", Path: "from-stella/old.md"}}, 40, LegacyLine{})
	if err != nil {
		t.Fatal(err)
	}
	// Three files opened: the two that changed, and the one being carried. The note that
	// did not change and is not open -- there is none here, but the count is the claim --
	// is never touched.
	if got := NoteParses() - before; got != 3 {
		t.Fatalf("parsed %d notes for 2 changed and 1 open, want 3", got)
	}
	if len(res.Items) != 2 {
		t.Fatalf("listed %d items, want the new one and the carried one: %+v", len(res.Items), res.Items)
	}
	// Newest first, and the carried one is marked heard because RECEIPTS records it: heard
	// is not answered, and the receipt outlives the cursor because RECEIPTS is read whole.
	if res.Items[0].Note.Path != "from-stella/new.md" {
		t.Fatalf("the listing is not newest first: %s", res.Items[0].Note.Path)
	}
	if !res.Items[1].Heard {
		t.Fatalf("the receipted note is not marked heard")
	}
	if res.New != 1 || len(res.Open) != 2 {
		t.Fatalf("new=%d open=%d, want 1 and 2", res.New, len(res.Open))
	}

	// Now a reply of mine, in the change set, closes the carried one.
	write(t, root, "from-rowan/answer.md", "From: Rowan\nTo: Stella\nDate: Wed Sep  9 00:01:00 UTC 2026\nId: rowan-333333333333\nRe: stella-abcdef012345\nSubject: yes\n\nYes.\n")
	res, err = InboxSince(root, c, me,
		[]string{"from-rowan/answer.md"},
		[]OpenEntry{
			{ID: "stella-abcdef012345", Path: "from-stella/old.md"},
			{ID: "stella-111111111111", Path: "from-stella/new.md"},
		}, 40, LegacyLine{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Open) != 1 || res.Open[0].ID != "stella-111111111111" {
		t.Fatalf("the answered note is still open: %+v", res.Open)
	}
}

// An open note whose file has gone is NAMED and dropped, never dropped in silence.
func TestInboxSinceNamesAnOpenNoteThatVanished(t *testing.T) {
	root := writeTable(t, nil)
	c, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	res, err := InboxSince(root, c, mustParticipant(t, c, "Rowan"), nil,
		[]OpenEntry{{ID: "stella-abcdef012345", Path: "from-stella/gone.md"}}, 40, LegacyLine{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Unreadable) != 1 || !strings.Contains(res.Unreadable[0].Parse.Err.Error(), "no longer on the table") {
		t.Fatalf("a vanished open note was not named: %+v", res.Unreadable)
	}
	if len(res.Open) != 0 || len(res.Items) != 0 {
		t.Fatalf("a vanished open note is still carried: %+v", res.Open)
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

// A note I answered long ago, and that somebody EDITED today, does not come back into my
// open list. The reply that closed it is behind the cursor, so the change set cannot see
// it -- my own lane's catalogue can, in a line scan, which is what it is there for.
func TestANoteAnsweredBeforeTheCursorStaysAnswered(t *testing.T) {
	root := writeTable(t, map[string]string{
		"from-stella/old.md": "From: Stella\nTo: Rowan\nDate: Mon Sep  7 00:01:00 UTC 2026\nId: stella-abcdef012345\nSubject: old\n\nA question?\n",
		"from-rowan/answer.md": "From: Rowan\nTo: Stella\nDate: Mon Sep  7 01:00:00 UTC 2026\nId: rowan-333333333333\n" +
			"Re: stella-abcdef012345\nSubject: yes\n\nYes.\n",
		"from-rowan/INDEX": "rowan-333333333333\tfrom-rowan/answer.md\t2026-09-07T01:00:00Z\tStella\tstella-abcdef012345\n",
	})
	c, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	// The only thing in the change set is the answered note itself, edited.
	res, err := InboxSince(root, c, mustParticipant(t, c, "Rowan"), []string{"from-stella/old.md"}, nil, 40, LegacyLine{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 0 || len(res.Open) != 0 {
		t.Fatalf("an edit to an already-answered note reopened it: items=%d open=%+v", len(res.Items), res.Open)
	}
}

// The limit of that, stated as a test so nobody has to discover it. The catalogue holds the
// replies SEND wrote; a reply written BY HAND -- in a browser, which this table's whole form
// exists to allow -- has no line in it, and once it falls behind the cursor the incremental
// read cannot see it. An edit to the note it answered therefore RE-SHOWS that note.
//
// The direction is the whole point: re-show, never loss. A reader is asked twice, which is
// tiresome; a reader is never told a note is answered when it is not, and never loses one.
// And the repair is the one check --full names: a catalogue line for the hand-written reply.
func TestAHandWrittenReplyBehindTheCursorReShowsTheNote(t *testing.T) {
	files := map[string]string{
		"from-stella/old.md": "From: Stella\nTo: Rowan\nDate: Mon Sep  7 00:01:00 UTC 2026\nId: stella-abcdef012345\nSubject: old\n\nA question?\n",
		// A reply with no INDEX line: nobody ran send, somebody typed it.
		"from-rowan/answer.md": "From: Rowan\nTo: Stella\nDate: Mon Sep  7 01:00:00 UTC 2026\nId: rowan-333333333333\n" +
			"Re: stella-abcdef012345\nSubject: yes\n\nYes.\n",
	}
	root := writeTable(t, files)
	c, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	me := mustParticipant(t, c, "Rowan")

	// While the reply is still in the change set it closes the thread, exactly as a
	// catalogued one would.
	res, err := InboxSince(root, c, me, []string{"from-stella/old.md", "from-rowan/answer.md"}, nil, 40, LegacyLine{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 0 {
		t.Fatalf("a reply in the change set did not close its thread: %+v", res.Items)
	}

	// Once it is behind the cursor, it is invisible to this run, and the edited note is
	// shown again. Shown -- not lost: it is in the listing and in the open list.
	res, err = InboxSince(root, c, me, []string{"from-stella/old.md"}, nil, 40, LegacyLine{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 1 || res.Items[0].Note.Header.ID != "stella-abcdef012345" || len(res.Open) != 1 {
		t.Fatalf("the re-show is not what the docs say it is: items=%+v open=%+v", res.Items, res.Open)
	}

	// The repair check --full names, applied: one catalogue line for the hand-written
	// reply, and the thread is closed again from a line scan.
	write(t, root, "from-rowan/INDEX", "rowan-333333333333\tfrom-rowan/answer.md\t2026-09-07T01:00:00Z\tStella\tstella-abcdef012345\n")
	res, err = InboxSince(root, c, me, []string{"from-stella/old.md"}, nil, 40, LegacyLine{})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 0 || len(res.Open) != 0 {
		t.Fatalf("a catalogue line for the hand-written reply did not close the thread: %+v", res.Items)
	}

	// And check says so, in a warning that names what it costs in one's own lane.
	tab := loadTable(t, root)
	if err := os.Remove(filepath.Join(root, "from-rowan", "INDEX")); err != nil {
		t.Fatal(err)
	}
	idx, err := ReadIndex(root, c)
	if err != nil {
		t.Fatal(err)
	}
	var found string
	for _, p := range CheckIndex(c, tab, idx) {
		if p.Where == "from-rowan/answer.md" {
			found = p.Reason
		}
	}
	if !strings.Contains(found, "re-appear as open") || !strings.Contains(found, "--rebuild-index") {
		t.Fatalf("the warning does not say what a missing catalogue line costs in one's own lane: %q", found)
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
	root := writeTable(t, nil)
	const lane = "from-rowan"
	first := "1111111111111111111111111111111111111111"
	if err := WriteCursor(root, lane, first, 1, "", at("2026-09-09T12:00:00Z")); err != nil {
		t.Fatal(err)
	}
	full := filepath.Join(root, filepath.FromSlash(CursorPath(lane)))
	held, err := os.Open(full)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()

	second := "2222222222222222222222222222222222222222"
	if err := WriteCursor(root, lane, second, 7, "", at("2026-09-09T13:00:00Z")); err != nil {
		t.Fatal(err)
	}
	old, err := io.ReadAll(held)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(old), first) {
		t.Fatalf("the descriptor opened before the write sees %q; the file was rewritten in place, so a kill mid-write would leave neither the old file nor the new one", string(old))
	}
	// And the path itself holds the new cursor, whole: one line, four tokens, readable.
	got, err := ReadCursor(root, lane)
	if err != nil {
		t.Fatal(err)
	}
	if got.Commit != second || got.Open != 7 {
		t.Fatalf("the new cursor reads as %+v", got)
	}
	// The temporary is gone. It is a step in a write, never a file on the table.
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
	files["from-stella/CURSOR"] = "1111111111111111111111111111111111111111 2026-09-09T12:00:00Z open=0\n"
	files["from-stella/CURSOR"+TempSuffix] = "22222222222222222222222222222222222222"
	files["from-stella/OPEN"+TempSuffix] = "stella-abcdef012345 from-stella/half-a-l"
	tab := loadTable(t, writeTable(t, files))
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
	files["from-stella/notes.tmp"] = "not a state file\n"
	stray := loadTable(t, writeTable(t, files))
	found := false
	for _, p := range stray.Check() {
		if p.Where == "from-stella/notes.tmp" {
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
	root := writeTable(t, nil)
	sha := "0123456789abcdef0123456789abcdef01234567"
	if err := WriteCursor(root, "from-rowan", sha, 2, "2026-09-01", at("2026-09-09T12:34:56Z")); err != nil {
		t.Fatal(err)
	}
	if line := readFile(t, root, CursorPath("from-rowan")); line != sha+" 2026-09-09T12:34:56Z open=2 legacy=2026-09-01\n" {
		t.Fatalf("the cursor line is %q", line)
	}
	got, err := ReadCursor(root, "from-rowan")
	if err != nil {
		t.Fatal(err)
	}
	if got.Legacy != "2026-09-01" || !got.LegacyBefore().Equal(at("2026-09-01T00:00:00Z")) {
		t.Fatalf("the legacy line did not round trip: %+v", got)
	}
	// No line is no token, which is exactly the shape of every cursor written before the
	// line existed: three tokens, read the same way, LegacyBefore zero.
	if err := WriteCursor(root, "from-rowan", sha, 2, "", at("2026-09-09T12:34:56Z")); err != nil {
		t.Fatal(err)
	}
	if line := readFile(t, root, CursorPath("from-rowan")); strings.Contains(line, "legacy=") {
		t.Fatalf("a cursor with no line still wrote one: %q", line)
	}
	got, err = ReadCursor(root, "from-rowan")
	if err != nil || got.Legacy != "" || !got.LegacyBefore().IsZero() {
		t.Fatalf("a cursor with no legacy line: %+v, %v", got, err)
	}
	// The tokens are read by their PREFIX and not by their position, so a cursor may carry
	// the line without the count and the two may arrive in either order.
	for _, line := range []string{
		sha + " 2026-09-09T12:34:56Z legacy=2026-09-01\n",
		sha + " 2026-09-09T12:34:56Z legacy=2026-09-01 open=2\n",
	} {
		write(t, root, CursorPath("from-rowan"), line)
		got, err := ReadCursor(root, "from-rowan")
		if err != nil || got.Legacy != "2026-09-01" {
			t.Fatalf("cursor %q read as %+v, %v", line, got, err)
		}
	}
	// And a date that is not one is a refusal at the READ, like every other value on this
	// line that becomes a decision later.
	for _, bad := range []string{
		sha + " 2026-09-09T12:34:56Z legacy=last-tuesday\n",
		sha + " 2026-09-09T12:34:56Z legacy=2026-09-01T00:00:00Z\n",
		sha + " 2026-09-09T12:34:56Z legacy=\n",
	} {
		write(t, root, CursorPath("from-rowan"), bad)
		if _, err := ReadCursor(root, "from-rowan"); err == nil {
			t.Fatalf("a cursor of %q was accepted", bad)
		}
	}
	// A line WriteCursor cannot write is refused before it reaches the file, so a cursor on
	// the table is never a date nobody can read.
	if err := WriteCursor(root, "from-rowan", sha, 0, "last Tuesday", at("2026-09-09T12:34:56Z")); err == nil {
		t.Fatal("WriteCursor wrote a legacy line that is not a date")
	}
}

// The line applied to a change set and to a full walk, which are the two reads a table
// gets, kept in one rule so they cannot draw it differently.
func TestTheLegacyLineLeavesOldNotesOffTheOpenListInBothReads(t *testing.T) {
	files := fixture()
	files["from-stella/2026-08-01T0001Z-before-the-line.md"] = `From: Stella
To: Rowan
Date: Sat Aug  1 00:01:00 UTC 2026
Id: stella-aaaaaaaaaaaa
Subject: A note from before the table adopted the tool

The body.
`
	root := writeTable(t, files)
	c, err := LoadConfig(root)
	if err != nil {
		t.Fatal(err)
	}
	me := mustParticipant(t, c, "Rowan")
	line := LegacyLine{Before: at("2026-09-01T00:00:00Z")}

	// The incremental read: the old note is in the change set, is not carried, and is
	// counted rather than listed.
	res, err := InboxSince(root, c, me,
		[]string{"from-stella/2026-08-01T0001Z-before-the-line.md", "from-stella/2026-09-07T0001Z-a-question-abcdef012345.md"},
		nil, 40, line)
	if err != nil {
		t.Fatal(err)
	}
	if res.Legacy != 1 {
		t.Fatalf("counted %d notes behind the line, want 1", res.Legacy)
	}
	for _, it := range res.Items {
		if strings.Contains(it.Note.Path, "before-the-line") {
			t.Fatal("a note behind the line is in the listing")
		}
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
	// rule is about the note's date and not about how it arrived.
	res, err = InboxSince(root, c, me, nil,
		[]OpenEntry{{ID: "stella-aaaaaaaaaaaa", Path: "from-stella/2026-08-01T0001Z-before-the-line.md"}}, 40, line)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Open) != 0 || res.Legacy != 1 {
		t.Fatalf("a carried note behind the line stayed: open=%+v legacy=%d", res.Open, res.Legacy)
	}

	// The full read: the same rule, over the whole table.
	tab := loadTable(t, root)
	keep, covered := SplitLegacy(tab.Inbox(me, 40), line)
	if covered != 1 {
		t.Fatalf("the full walk left %d notes off, want 1", covered)
	}
	for _, it := range keep {
		if strings.Contains(it.Note.Path, "before-the-line") {
			t.Fatal("the full walk listed a note behind the line")
		}
	}
	// With no line at all, nothing is left off and the listing is what it always was.
	if _, covered := SplitLegacy(tab.Inbox(me, 40), LegacyLine{}); covered != 0 {
		t.Fatalf("a run with no line left %d notes off", covered)
	}
	// A note whose date cannot be read AT ALL is never behind the line: a file that cannot
	// say when it was written cannot claim to predate anything, and the safe direction for
	// a note nobody can date is to carry it.
	files["from-stella/undated.md"] = "From: Stella\nTo: Rowan\nSubject: No date line, and no minute in the filename\n\nThe body.\n"
	undated := loadTable(t, writeTable(t, files))
	_, covered = SplitLegacy(undated.Inbox(me, 40), LegacyLine{Before: at("2030-01-01T00:00:00Z")})
	if covered != len(undated.Inbox(me, 40))-1 {
		t.Fatalf("an undated note was taken as older than the line: %d of %d left off", covered, len(undated.Inbox(me, 40)))
	}
}
