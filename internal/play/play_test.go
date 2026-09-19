package play

import (
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func writeSource(t *testing.T, dir, name, body string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// Two participants annotate the same passage, reply, resume, and export.
// This is the red test for issue #222: the Export function does not exist yet.
func TestTwoParticipantsAnnotateReplyResumeExport(t *testing.T) {
	dir := t.TempDir()
	src := writeSource(t, dir, "story.txt", "The lantern room held a brass fitting.\nIt collected salt on every wind.\n")

	// Emma annotates.
	n1, err := Annotate(src, "Emma", "The lantern room held a brass fitting.", "I wonder what alloy this is.")
	if err != nil {
		t.Fatalf("Emma annotate: %v", err)
	}
	if n1.Author != "Emma" {
		t.Errorf("note author = %q, want Emma", n1.Author)
	}

	// Stella annotates the same passage.
	n2, err := Annotate(src, "Stella", "The lantern room held a brass fitting.", "Ship's brass, probably 70/30.")
	if err != nil {
		t.Fatalf("Stella annotate: %v", err)
	}

	// Emma replies to Stella.
	r1, err := ReplyTo(src, n2.ID, "Emma", "That would resist marine corrosion well.")
	if err != nil {
		t.Fatalf("Emma reply: %v", err)
	}
	if r1.Author != "Emma" {
		t.Errorf("reply author = %q, want Emma", r1.Author)
	}

	// Resume later: read the notes back.
	notes, anchorStatus, err := ReadNotes(src)
	if err != nil {
		t.Fatalf("ReadNotes: %v", err)
	}
	if len(notes) != 2 {
		t.Fatalf("notes = %d, want 2", len(notes))
	}
	if anchorStatus != "ANCHOR OK" {
		t.Errorf("anchor = %q, want ANCHOR OK", anchorStatus)
	}

	// Export the notes. This function does not exist yet -- red test.
	exported, err := Export(src)
	if err != nil {
		t.Fatalf("Export: %v", err)
	}
	if !strings.Contains(exported, "Emma") {
		t.Errorf("export missing Emma: %s", exported)
	}
	if !strings.Contains(exported, "Stella") {
		t.Errorf("export missing Stella: %s", exported)
	}
	if !strings.Contains(exported, n1.ID) {
		t.Errorf("export missing note id %s: %s", n1.ID, exported)
	}
	if !strings.Contains(exported, r1.ID) {
		t.Errorf("export missing reply id %s: %s", r1.ID, exported)
	}
}

// A changed source between sessions produces an explicit ANCHOR STALE,
// not a silent reassignment.
func TestStaleAnchorAfterSourceEdit(t *testing.T) {
	dir := t.TempDir()
	src := writeSource(t, dir, "story.txt", "The lantern room held a brass fitting.\n")

	_, err := Annotate(src, "Emma", "The lantern room held a brass fitting.", "Nice.")
	if err != nil {
		t.Fatalf("first annotate: %v", err)
	}

	// Edit the source -- this invalidates the anchor.
	if err := os.WriteFile(src, []byte("The lantern room held a copper fitting.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// A second annotation should fail with ANCHOR STALE.
	_, err = Annotate(src, "Stella", "The lantern room held a copper fitting.", "Changed.")
	if err == nil {
		t.Fatal("second annotate succeeded, want ANCHOR STALE error")
	}
	if !strings.Contains(err.Error(), "ANCHOR STALE") {
		t.Errorf("error = %q, want ANCHOR STALE", err.Error())
	}

	// ReadNotes should also report the stale anchor.
	_, status, err := ReadNotes(src)
	if err != nil {
		t.Fatalf("ReadNotes: %v", err)
	}
	if !strings.HasPrefix(status, "ANCHOR STALE") {
		t.Errorf("status = %q, want ANCHOR STALE", status)
	}
}

// Source path containing spaces must not trigger false ANCHOR STALE.
func TestPathWithSpaces(t *testing.T) {
	dir := t.TempDir()
	src := writeSource(t, dir, "story with spaces.txt", "A shared passage.\n")

	n1, err := Annotate(src, "Emma", "A shared passage.", "Note on file with spaces.")
	if err != nil {
		t.Fatalf("Annotate failed: %v", err)
	}

	notes, status, err := ReadNotes(src)
	if err != nil {
		t.Fatalf("ReadNotes failed: %v", err)
	}
	if status != "ANCHOR OK" {
		t.Fatalf("status = %q, want ANCHOR OK", status)
	}
	if len(notes) != 1 {
		t.Fatalf("got %d notes, want 1", len(notes))
	}

	r, err := ReplyTo(src, n1.ID, "Stella", "Reply on file with spaces.")
	if err != nil {
		t.Fatalf("ReplyTo failed: %v", err)
	}
	if r.Author != "Stella" {
		t.Errorf("reply author = %q, want Stella", r.Author)
	}

	notes, status, err = ReadNotes(src)
	if err != nil {
		t.Fatalf("ReadNotes after reply failed: %v", err)
	}
	if status != "ANCHOR OK" {
		t.Fatalf("status = %q, want ANCHOR OK", status)
	}
	if len(notes[0].Replies) != 1 {
		t.Fatalf("got %d replies, want 1", len(notes[0].Replies))
	}
}

// Multiline passage, note, and reply content must be preserved losslessly across
// save, load, and subsequent writes.
func TestMultilineNotePassageReplyRoundTrip(t *testing.T) {
	dir := t.TempDir()
	srcText := "Passage line 1\nPassage line 2\nPassage line 3\n"
	src := writeSource(t, dir, "story.txt", srcText)

	passage := "Passage line 1\nPassage line 2"
	noteText := "First note line.\n\nSecond note line with BODY and NOTE keywords."
	n1, err := Annotate(src, "Emma", passage, noteText)
	if err != nil {
		t.Fatalf("Annotate failed: %v", err)
	}

	// First reload
	notes, status, err := ReadNotes(src)
	if err != nil {
		t.Fatalf("ReadNotes failed: %v", err)
	}
	if status != "ANCHOR OK" {
		t.Fatalf("status = %q, want ANCHOR OK", status)
	}
	if len(notes) != 1 {
		t.Fatalf("got %d notes, want 1", len(notes))
	}
	if notes[0].Passage != passage {
		t.Errorf("Passage mismatch:\ngot:  %q\nwant: %q", notes[0].Passage, passage)
	}
	if notes[0].Note != noteText {
		t.Errorf("Note mismatch:\ngot:  %q\nwant: %q", notes[0].Note, noteText)
	}

	// Multiline reply
	replyText := "First reply line.\nSecond reply line.\n\nThird reply line."
	r1, err := ReplyTo(src, n1.ID, "Stella", replyText)
	if err != nil {
		t.Fatalf("ReplyTo failed: %v", err)
	}
	if r1.Note != replyText {
		t.Errorf("Reply returned mismatch:\ngot:  %q\nwant: %q", r1.Note, replyText)
	}

	// Second reload after reply write
	notes, status, err = ReadNotes(src)
	if err != nil {
		t.Fatalf("ReadNotes after reply failed: %v", err)
	}
	if status != "ANCHOR OK" {
		t.Fatalf("status = %q, want ANCHOR OK", status)
	}
	if notes[0].Passage != passage {
		t.Errorf("Passage after reply mismatch:\ngot:  %q\nwant: %q", notes[0].Passage, passage)
	}
	if notes[0].Note != noteText {
		t.Errorf("Note after reply mismatch:\ngot:  %q\nwant: %q", notes[0].Note, noteText)
	}
	if len(notes[0].Replies) != 1 {
		t.Fatalf("got %d replies, want 1", len(notes[0].Replies))
	}
	if notes[0].Replies[0].Note != replyText {
		t.Errorf("Reply after reload mismatch:\ngot:  %q\nwant: %q", notes[0].Replies[0].Note, replyText)
	}

	// Another write (second reply) to verify subsequent save preserves everything
	reply2Text := "Fourth reply line.\nFifth reply line."
	_, err = ReplyTo(src, n1.ID, "Emma", reply2Text)
	if err != nil {
		t.Fatalf("Second ReplyTo failed: %v", err)
	}

	notes, status, err = ReadNotes(src)
	if err != nil {
		t.Fatalf("ReadNotes after second reply failed: %v", err)
	}
	if notes[0].Passage != passage {
		t.Errorf("Passage after 2nd reply mismatch:\ngot:  %q\nwant: %q", notes[0].Passage, passage)
	}
	if notes[0].Note != noteText {
		t.Errorf("Note after 2nd reply mismatch:\ngot:  %q\nwant: %q", notes[0].Note, noteText)
	}
	if len(notes[0].Replies) != 2 {
		t.Fatalf("got %d replies, want 2", len(notes[0].Replies))
	}
	if notes[0].Replies[0].Note != replyText {
		t.Errorf("Reply 1 mismatch:\ngot:  %q\nwant: %q", notes[0].Replies[0].Note, replyText)
	}
	if notes[0].Replies[1].Note != reply2Text {
		t.Errorf("Reply 2 mismatch:\ngot:  %q\nwant: %q", notes[0].Replies[1].Note, reply2Text)
	}

	// Verify Export includes multiline content
	exported, err := Export(src)
	if err != nil {
		t.Fatalf("Export failed: %v", err)
	}
	if !strings.Contains(exported, passage) {
		t.Errorf("Export missing multiline passage:\n%s", exported)
	}
	if !strings.Contains(exported, noteText) {
		t.Errorf("Export missing multiline note:\n%s", exported)
	}
	if !strings.Contains(exported, replyText) {
		t.Errorf("Export missing multiline reply 1:\n%s", exported)
	}
	if !strings.Contains(exported, reply2Text) {
		t.Errorf("Export missing multiline reply 2:\n%s", exported)
	}
}

// Stale source must cause ReplyTo to refuse with ANCHOR STALE, leaving the sidecar
// file byte-identical to before the attempt. Missing source must also refuse and leave
// sidecar untouched.
func TestStaleSourceReplyRefusal(t *testing.T) {
	dir := t.TempDir()
	src := writeSource(t, dir, "story.txt", "The lantern room held a brass fitting.\n")

	n, err := Annotate(src, "Emma", "The lantern room held a brass fitting.", "Initial note.")
	if err != nil {
		t.Fatalf("Annotate: %v", err)
	}

	sidecarPath := NoteFile(src)
	sidecarBefore, err := os.ReadFile(sidecarPath)
	if err != nil {
		t.Fatalf("read sidecar before: %v", err)
	}

	// Edit source to invalidate anchor.
	if err := os.WriteFile(src, []byte("The lantern room held a copper fitting.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// ReplyTo must refuse with ANCHOR STALE.
	_, err = ReplyTo(src, n.ID, "Stella", "A reply to a modified source.")
	if err == nil {
		t.Fatal("ReplyTo succeeded on stale source, want ANCHOR STALE error")
	}
	if !strings.Contains(err.Error(), "ANCHOR STALE") {
		t.Errorf("err = %q, want ANCHOR STALE", err.Error())
	}

	// Sidecar must remain byte-identical after refusal.
	sidecarAfter, err := os.ReadFile(sidecarPath)
	if err != nil {
		t.Fatalf("read sidecar after: %v", err)
	}
	if !bytes.Equal(sidecarBefore, sidecarAfter) {
		t.Errorf("sidecar content modified after stale reply refusal:\nbefore:\n%s\nafter:\n%s",
			sidecarBefore, sidecarAfter)
	}

	// Delete source file completely.
	if err := os.Remove(src); err != nil {
		t.Fatal(err)
	}

	// ReplyTo must fail on missing source.
	_, err = ReplyTo(src, n.ID, "Stella", "A reply to a missing source.")
	if err == nil {
		t.Fatal("ReplyTo succeeded on missing source, want error")
	}

	// Sidecar must still remain byte-identical.
	sidecarAfterMissing, err := os.ReadFile(sidecarPath)
	if err != nil {
		t.Fatalf("read sidecar after missing: %v", err)
	}
	if !bytes.Equal(sidecarBefore, sidecarAfterMissing) {
		t.Errorf("sidecar content modified after missing source reply refusal:\nbefore:\n%s\nafter:\n%s",
			sidecarBefore, sidecarAfterMissing)
	}
}

// Verify existing sidecar fixture in internal/play/ can be loaded and read.
func TestExistingSidecarFixturePreserved(t *testing.T) {
	fixture := filepath.Join(".", "story.txt")
	store, err := LoadStore(fixture)
	if err != nil {
		t.Fatalf("LoadStore on fixture: %v", err)
	}
	if len(store.Notes) != 2 {
		t.Fatalf("fixture notes = %d, want 2", len(store.Notes))
	}
	if len(store.Notes[1].Replies) != 1 {
		t.Fatalf("fixture replies = %d, want 1", len(store.Notes[1].Replies))
	}
	wantReply := "That would resist marine corrosion well."
	if store.Notes[1].Replies[0].Note != wantReply {
		t.Errorf("reply body = %q, want %q", store.Notes[1].Replies[0].Note, wantReply)
	}
}

// Lines beginning with NOTE, REPLY, PASSAGE, and BODY in note, passage, and reply
// must not be confused with record delimiters.
func TestProseBeginningWithKeywordsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	srcText := "A shared passage for keyword test.\nWith multiple lines.\n"
	src := writeSource(t, dir, "story.txt", srcText)

	passage := "A shared passage for keyword test.\nWith multiple lines."
	// Note containing delimiter keywords at the beginning of internal lines
	noteText := "First line\nNOTE this is ordinary prose\nPASSAGE this is not a passage\nBODY this is not a body\nREPLY this is not a reply\nREPLY_BODY this is not a reply body\nLast line"

	n, err := Annotate(src, "Emma", passage, noteText)
	if err != nil {
		t.Fatalf("Annotate failed: %v", err)
	}

	notes, status, err := ReadNotes(src)
	if err != nil {
		t.Fatalf("ReadNotes failed: %v", err)
	}
	if status != "ANCHOR OK" {
		t.Fatalf("status = %q, want ANCHOR OK", status)
	}
	if len(notes) != 1 {
		t.Fatalf("got %d notes, want exactly 1 (must not invent extra notes from keyword lines)", len(notes))
	}
	if notes[0].Note != noteText {
		t.Errorf("Note mismatch:\ngot:  %q\nwant: %q", notes[0].Note, noteText)
	}

	// Reply containing keyword lines as well
	replyText := "Reply line 1\nNOTE ordinary reply note\nBODY ordinary reply body\nReply line 3"
	_, err = ReplyTo(src, n.ID, "Stella", replyText)
	if err != nil {
		t.Fatalf("ReplyTo failed: %v", err)
	}

	notes, status, err = ReadNotes(src)
	if err != nil {
		t.Fatalf("ReadNotes after reply failed: %v", err)
	}
	if len(notes) != 1 {
		t.Fatalf("got %d notes after reply, want 1", len(notes))
	}
	if notes[0].Note != noteText {
		t.Errorf("Note mismatch after reply:\ngot:  %q\nwant: %q", notes[0].Note, noteText)
	}
	if len(notes[0].Replies) != 1 {
		t.Fatalf("got %d replies, want 1", len(notes[0].Replies))
	}
	if notes[0].Replies[0].Note != replyText {
		t.Errorf("Reply mismatch after reload:\ngot:  %q\nwant: %q", notes[0].Replies[0].Note, replyText)
	}
}

// CRLF (\r\n) bytes in note and reply content must be preserved without normalization to LF.
func TestCRLFPreservation(t *testing.T) {
	dir := t.TempDir()
	src := writeSource(t, dir, "story.txt", "The lantern room held a brass fitting.\n")

	crlfNote := "First line\r\nSecond line\r\n\r\nFourth line\r\n"
	n, err := Annotate(src, "Emma", "The lantern room held a brass fitting.", crlfNote)
	if err != nil {
		t.Fatalf("Annotate failed: %v", err)
	}

	notes, _, err := ReadNotes(src)
	if err != nil {
		t.Fatalf("ReadNotes failed: %v", err)
	}
	if notes[0].Note != crlfNote {
		t.Errorf("CRLF note mismatch:\ngot:  %q\nwant: %q", notes[0].Note, crlfNote)
	}

	crlfReply := "Reply line 1\r\nReply line 2\r\n"
	_, err = ReplyTo(src, n.ID, "Stella", crlfReply)
	if err != nil {
		t.Fatalf("ReplyTo failed: %v", err)
	}

	notes, _, err = ReadNotes(src)
	if err != nil {
		t.Fatalf("ReadNotes after reply failed: %v", err)
	}
	if notes[0].Note != crlfNote {
		t.Errorf("CRLF note after reply mismatch:\ngot:  %q\nwant: %q", notes[0].Note, crlfNote)
	}
	if notes[0].Replies[0].Note != crlfReply {
		t.Errorf("CRLF reply mismatch:\ngot:  %q\nwant: %q", notes[0].Replies[0].Note, crlfReply)
	}
}

// Multi-word author names must be preserved losslessly for both notes and replies.
func TestFullAuthorNamesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	src := writeSource(t, dir, "story.txt", "The lantern room held a brass fitting.\n")

	author1 := "Test Reader"
	n, err := Annotate(src, author1, "The lantern room held a brass fitting.", "A note by Test Reader.")
	if err != nil {
		t.Fatalf("Annotate failed: %v", err)
	}
	if n.Author != author1 {
		t.Fatalf("Annotate returned author = %q, want %q", n.Author, author1)
	}

	notes, _, err := ReadNotes(src)
	if err != nil {
		t.Fatalf("ReadNotes failed: %v", err)
	}
	if notes[0].Author != author1 {
		t.Errorf("ReadNotes author = %q, want %q", notes[0].Author, author1)
	}

	author2 := "Second Reviewer With Long Name"
	r, err := ReplyTo(src, n.ID, author2, "A reply with full author name.")
	if err != nil {
		t.Fatalf("ReplyTo failed: %v", err)
	}
	if r.Author != author2 {
		t.Fatalf("ReplyTo returned author = %q, want %q", r.Author, author2)
	}

	notes, _, err = ReadNotes(src)
	if err != nil {
		t.Fatalf("ReadNotes after reply failed: %v", err)
	}
	if notes[0].Author != author1 {
		t.Errorf("ReadNotes author = %q, want %q", notes[0].Author, author1)
	}
	if len(notes[0].Replies) != 1 {
		t.Fatalf("got %d replies, want 1", len(notes[0].Replies))
	}
	if notes[0].Replies[0].Author != author2 {
		t.Errorf("ReadNotes reply author = %q, want %q", notes[0].Replies[0].Author, author2)
	}
}

// An author or a body containing double quotes, backslashes and a trailing
// space must survive annotate, read and reply byte for byte, and the sidecar
// bytes must be exactly what the version-2 format says: the author field is
// a Go-quoted string whenever it contains a space, a quote or a backslash,
// and the text records are a single escaped physical line.
func TestQuotedAuthorAndTextRoundTrip(t *testing.T) {
	dir := t.TempDir()
	src := writeSource(t, dir, "story.txt", "The lantern room held a brass fitting.\n")
	passage := "The lantern room held a brass fitting."

	noteAuthor := `Ada "The Reader" Lovelace\Byron `
	noteBody := "She said \"hello\" and left\\away\nsecond line ends in a space "

	n, err := Annotate(src, noteAuthor, passage, noteBody)
	if err != nil {
		t.Fatalf("Annotate failed: %v", err)
	}
	if n.Author != noteAuthor {
		t.Errorf("Annotate returned author = %q, want %q", n.Author, noteAuthor)
	}

	notes, status, err := ReadNotes(src)
	if err != nil {
		t.Fatalf("ReadNotes failed: %v", err)
	}
	if status != "ANCHOR OK" {
		t.Fatalf("status = %q, want ANCHOR OK", status)
	}
	if len(notes) != 1 {
		t.Fatalf("got %d notes, want 1", len(notes))
	}
	if notes[0].Author != noteAuthor {
		t.Errorf("note author after reload:\ngot  %q\nwant %q", notes[0].Author, noteAuthor)
	}
	if notes[0].Note != noteBody {
		t.Errorf("note body after reload:\ngot  %q\nwant %q", notes[0].Note, noteBody)
	}
	if notes[0].Passage != passage {
		t.Errorf("passage after reload:\ngot  %q\nwant %q", notes[0].Passage, passage)
	}

	replyAuthor := `Stella "Fixer" O'Hara\n`
	replyBody := "Quote: \"brass\"; path: C:\\ships\\brass\ntrailing space here "

	r, err := ReplyTo(src, n.ID, replyAuthor, replyBody)
	if err != nil {
		t.Fatalf("ReplyTo failed: %v", err)
	}
	if r.Author != replyAuthor {
		t.Errorf("ReplyTo returned author = %q, want %q", r.Author, replyAuthor)
	}

	notes, _, err = ReadNotes(src)
	if err != nil {
		t.Fatalf("ReadNotes after reply failed: %v", err)
	}
	if len(notes) != 1 {
		t.Fatalf("got %d notes after reply, want 1", len(notes))
	}
	if notes[0].Author != noteAuthor {
		t.Errorf("note author after reply rewrite:\ngot  %q\nwant %q", notes[0].Author, noteAuthor)
	}
	if notes[0].Note != noteBody {
		t.Errorf("note body after reply rewrite:\ngot  %q\nwant %q", notes[0].Note, noteBody)
	}
	if len(notes[0].Replies) != 1 {
		t.Fatalf("got %d replies, want 1", len(notes[0].Replies))
	}
	if notes[0].Replies[0].Author != replyAuthor {
		t.Errorf("reply author after reload:\ngot  %q\nwant %q", notes[0].Replies[0].Author, replyAuthor)
	}
	if notes[0].Replies[0].Note != replyBody {
		t.Errorf("reply body after reload:\ngot  %q\nwant %q", notes[0].Replies[0].Note, replyBody)
	}

	// The sidecar bytes must be exactly what the format documents.
	raw, err := os.ReadFile(NoteFile(src))
	if err != nil {
		t.Fatalf("read sidecar: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) != 7 {
		t.Fatalf("sidecar has %d lines, want 7:\n%s", len(lines), raw)
	}
	if !strings.HasPrefix(lines[0], "ANCHOR ") {
		t.Errorf("line 1 = %q, want an ANCHOR line", lines[0])
	}
	if lines[1] != "VERSION 2" {
		t.Errorf("line 2 = %q, want %q", lines[1], "VERSION 2")
	}
	wantNoteLine := "NOTE id=" + n.ID + " author=" + strconv.Quote(noteAuthor) +
		" created=" + n.CreatedAt.Format(time.RFC3339)
	if lines[2] != wantNoteLine {
		t.Errorf("NOTE line:\ngot  %q\nwant %q", lines[2], wantNoteLine)
	}
	if want := "PASSAGE " + escapeText(passage); lines[3] != want {
		t.Errorf("PASSAGE line:\ngot  %q\nwant %q", lines[3], want)
	}
	if want := "BODY " + escapeText(noteBody); lines[4] != want {
		t.Errorf("BODY line:\ngot  %q\nwant %q", lines[4], want)
	}
	wantReplyLine := "REPLY id=" + r.ID + " author=" + strconv.Quote(replyAuthor) +
		" created=" + r.CreatedAt.Format(time.RFC3339)
	if lines[5] != wantReplyLine {
		t.Errorf("REPLY line:\ngot  %q\nwant %q", lines[5], wantReplyLine)
	}
	if want := "REPLY_BODY " + escapeText(replyBody); lines[6] != want {
		t.Errorf("REPLY_BODY line:\ngot  %q\nwant %q", lines[6], want)
	}
}
