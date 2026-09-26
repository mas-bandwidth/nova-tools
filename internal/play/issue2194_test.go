package play

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestIssue2194 covers two version-2 reader edges the format defines:
//  1. TestBlankAndCommentLinesIgnored — blank lines and `# comment` lines
//     between records are ignored on read.
//  2. TestEmptyAndUnprintableAuthorFramed — an empty author and one
//     containing an unprintable rune are written Go-quoted and read back equal.
func TestIssue2194(t *testing.T) {
	t.Parallel()

	t.Run("TestBlankAndCommentLinesIgnored", testBlankAndCommentLinesIgnored)
	t.Run("TestEmptyAndUnprintableAuthorFramed", testEmptyAndUnprintableAuthorFramed)
}

func testBlankAndCommentLinesIgnored(t *testing.T) {
	dir := t.TempDir()
	src := writeSource(t, dir, "story.txt", "Line one of the source.\nLine two.\n")
	passage := "Line one of the source."

	// Write a normal note first.
	n1, err := Annotate(src, "Emma", passage, "First note.")
	if err != nil {
		t.Fatalf("Annotate: %v", err)
	}

	// Build a version-2 sidecar with blank lines and comments injected.
	store, err := LoadStore(src)
	if err != nil {
		t.Fatalf("LoadStore: %v", err)
	}
	// Add a second note.
	n2 := Note{
		ID:        "bbbbbbbbbbbb",
		Author:    "Stella",
		Passage:   "Line two.",
		Note:      "Second note.",
		CreatedAt: time.Now().UTC(),
	}
	store.Notes = append(store.Notes, n2)

	// Manually write the sidecar with blank lines and comments.
	var b strings.Builder
	fmt.Fprintf(&b, "ANCHOR %s %s\n", store.Anchor.SourcePath, store.Anchor.SourceHash)
	fmt.Fprintf(&b, "VERSION 2\n")
	fmt.Fprintf(&b, "\n") // blank line before first note
	fmt.Fprintf(&b, "# comment before first note\n")
	fmt.Fprintf(&b, "NOTE id=%s author=%s created=%s\n",
		n1.ID, formatAuthor(n1.Author), n1.CreatedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "PASSAGE %s\n", escapeText(n1.Passage))
	fmt.Fprintf(&b, "BODY %s\n", escapeText(n1.Note))
	fmt.Fprintf(&b, "\n") // blank line between notes
	fmt.Fprintf(&b, "# comment between notes\n")
	fmt.Fprintf(&b, "NOTE id=%s author=%s created=%s\n",
		n2.ID, formatAuthor(n2.Author), n2.CreatedAt.Format(time.RFC3339))
	fmt.Fprintf(&b, "PASSAGE %s\n", escapeText(n2.Passage))
	fmt.Fprintf(&b, "BODY %s\n", escapeText(n2.Note))
	fmt.Fprintf(&b, "# trailing comment\n")

	nf := NoteFile(src)
	if err := os.WriteFile(nf, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}

	// Read it back.
	s2, err := LoadStore(src)
	if err != nil {
		t.Fatalf("LoadStore after injection: %v", err)
	}

	// Must yield exactly two notes, no more, no fewer.
	if len(s2.Notes) != 2 {
		t.Fatalf("got %d notes, want 2", len(s2.Notes))
	}
	if s2.Notes[0].Author != "Emma" || s2.Notes[0].Note != "First note." {
		t.Errorf("first note:\ngot  author=%q note=%q\nwant author=%q note=%q",
			s2.Notes[0].Author, s2.Notes[0].Note, "Emma", "First note.")
	}
	if s2.Notes[1].Author != "Stella" || s2.Notes[1].Note != "Second note." {
		t.Errorf("second note:\ngot  author=%q note=%q\nwant author=%q note=%q",
			s2.Notes[1].Author, s2.Notes[1].Note, "Stella", "Second note.")
	}
}

func testEmptyAndUnprintableAuthorFramed(t *testing.T) {
	dir := t.TempDir()
	src := writeSource(t, dir, "story.txt", "The quick brown fox.\n")
	passage := "The quick brown fox."

	// 1. Empty author on Annotate.
	n1, err := Annotate(src, "", passage, "Anonymous note.")
	if err != nil {
		t.Fatalf("Annotate with empty author: %v", err)
	}
	if n1.Author != "" {
		t.Errorf("Annotate author = %q, want empty", n1.Author)
	}

	// Verify the sidecar writes `author=""`.
	raw, err := os.ReadFile(NoteFile(src))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `author=""`) {
		t.Errorf("sidecar missing author=\"\":\n%s", raw)
	}

	// Reload and verify round-trip.
	s, err := LoadStore(src)
	if err != nil {
		t.Fatalf("LoadStore: %v", err)
	}
	if len(s.Notes) != 1 || s.Notes[0].Author != "" {
		t.Errorf("after reload: got %d notes, author=%q; want 1 note, empty author",
			len(s.Notes), func() string {
				if len(s.Notes) > 0 {
					return s.Notes[0].Author
				}
				return "(no notes)"
			}())
	}

	// 2. Unprintable rune in author.
	unprintableAuthor := "User\x01Admin" // contains a control character
	n2, err := Annotate(src, unprintableAuthor, passage, "Unprintable author note.")
	if err != nil {
		t.Fatalf("Annotate with unprintable author: %v", err)
	}
	if n2.Author != unprintableAuthor {
		t.Errorf("Annotate author = %q, want %q", n2.Author, unprintableAuthor)
	}

	// Verify the sidecar writes the author as a Go-quoted string.
	raw, err = os.ReadFile(NoteFile(src))
	if err != nil {
		t.Fatal(err)
	}
	wantQuoted := strconv.Quote(unprintableAuthor)
	if !strings.Contains(string(raw), "author="+wantQuoted) {
		t.Errorf("sidecar missing author=%s:\n%s", wantQuoted, raw)
	}

	// Reload and verify round-trip.
	s, err = LoadStore(src)
	if err != nil {
		t.Fatalf("LoadStore after unprintable: %v", err)
	}
	if len(s.Notes) != 2 {
		t.Fatalf("got %d notes, want 2", len(s.Notes))
	}
	if s.Notes[1].Author != unprintableAuthor {
		t.Errorf("unprintable author after reload:\ngot  %q\nwant %q", s.Notes[1].Author, unprintableAuthor)
	}

	// 3. Empty author on ReplyTo.
	r1, err := ReplyTo(src, n1.ID, "", "Anonymous reply.")
	if err != nil {
		t.Fatalf("ReplyTo with empty author: %v", err)
	}
	if r1.Author != "" {
		t.Errorf("ReplyTo author = %q, want empty", r1.Author)
	}

	// Reload and verify reply author.
	s, err = LoadStore(src)
	if err != nil {
		t.Fatalf("LoadStore after reply: %v", err)
	}
	if len(s.Notes[0].Replies) != 1 {
		t.Fatalf("got %d replies, want 1", len(s.Notes[0].Replies))
	}
	if s.Notes[0].Replies[0].Author != "" {
		t.Errorf("reply author after reload:\ngot  %q\nwant empty", s.Notes[0].Replies[0].Author)
	}

	// 4. Unprintable rune in reply author.
	unprintableReplyAuthor := "Bot\x02Handler"
	r2, err := ReplyTo(src, n1.ID, unprintableReplyAuthor, "Bot reply.")
	if err != nil {
		t.Fatalf("ReplyTo with unprintable author: %v", err)
	}
	if r2.Author != unprintableReplyAuthor {
		t.Errorf("ReplyTo author = %q, want %q", r2.Author, unprintableReplyAuthor)
	}

	// Reload and verify reply author round-trip.
	s, err = LoadStore(src)
	if err != nil {
		t.Fatalf("LoadStore after unprintable reply: %v", err)
	}
	if len(s.Notes[0].Replies) != 2 {
		t.Fatalf("got %d replies, want 2", len(s.Notes[0].Replies))
	}
	if s.Notes[0].Replies[1].Author != unprintableReplyAuthor {
		t.Errorf("unprintable reply author after reload:\ngot  %q\nwant %q", s.Notes[0].Replies[1].Author, unprintableReplyAuthor)
	}
}
