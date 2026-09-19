package play

import (
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
