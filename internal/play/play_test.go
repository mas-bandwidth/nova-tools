package play

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
