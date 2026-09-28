package bus

import (
	"strings"
	"testing"
)

// A relayed note whose inner From: differs from the outer. The note's sender is its
// OUTER header From — the relayer — and a From: line inside the body is data that routes
// nothing. #102: the send-side tolerance read the LAST From in the header region while
// every reader takes the FIRST, so a relay could be judged by the quoted author instead
// of the relayer. The reader's own walk is pinned here too: it must never pick up a body
// From.
func TestARelayedNoteIsAttributedToItsOuterFrom(t *testing.T) {
	t.Parallel()

	t.Run("a quoted header in the body is data, not the sender", func(t *testing.T) {
		t.Parallel()
		tab := loadBus(t, writeBus(t, fixture()))
		// Ada relays Bo's note: Ada's header, a blank line, then Bo's quoted header.
		text := "From: Ada\nTo: Bo\nSubject: Fwd: the review\n\nFrom: Bo\nTo: Dana\nSubject: the review\n\nbody\n"
		p, err := PrepareDraft(tab, text, at("2026-09-09T12:34:56Z"), "", "Ada")
		if err != nil {
			t.Fatalf("a relayed draft with a quoted From in its body was refused: %v", err)
		}
		if p.Sender.Name != "Ada" {
			t.Fatalf("the relay was attributed to %q, want the outer sender Ada", p.Sender.Name)
		}
		if p.Note.Header.From != "Ada" {
			t.Fatalf("the note's From is %q, want the outer From Ada", p.Note.Header.From)
		}
		// And the reader agrees: a strict parse of the stored note still says Ada.
		n, err := ParseNote(p.Path, p.Note.Render())
		if err != nil {
			t.Fatalf("the stored relay did not parse: %v", err)
		}
		if n.Header.From != "Ada" {
			t.Fatalf("a reader attributed the relay to %q, want the outer From Ada", n.Header.From)
		}
	})

	t.Run("a quoted From pasted into the header is a duplicate, not the sender", func(t *testing.T) {
		t.Parallel()
		tab := loadBus(t, writeBus(t, fixture()))
		// The quoted header arrived with no blank line, so the parser sees two From
		// lines. The duplicate is a header problem; --as must be judged against the
		// OUTER one, never the quoted inner author.
		text := "From: Ada\nTo: Bo\nSubject: Fwd: the review\nFrom: Bo\nTo: Dana\nSubject: the review\n\nbody\n"
		_, err := PrepareDraft(tab, text, at("2026-09-09T12:34:56Z"), "", "Ada")
		if err == nil {
			t.Fatal("want a refusal for the second From line, got none")
		}
		if strings.Contains(err.Error(), "send does not send one line's note as another") {
			t.Fatalf("--as was judged against the quoted inner From, not the outer one: %q", err)
		}
		if !strings.Contains(err.Error(), "a second From line") {
			t.Fatalf("the refusal should be the duplicate From line, got %q", err)
		}
	})
}
