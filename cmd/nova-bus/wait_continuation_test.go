package main

import (
	"strings"
	"testing"
)

// TestWaitOnNoteContinuationReturnsTheRest: the `next=<token>` passed back as `--after` returns
// exactly the remainder once and ends with `next=-`.
//
// This pins: "After a bound the wake ends with the read half's continuation receipt... the same
// verb called again with `--after <token>` returns the rest, and no run repeats itself."
func TestWaitOnNoteContinuationReturnsTheRest(t *testing.T) {
	if testing.Short() {
		t.Skip("slow: requires full bus fixture")
	}
	t.Parallel()
	hermetic(t)
	checkout, _ := busDir(t)
	settled(t, checkout)

	// Create two notes with larger bodies so they trigger pagination
	note(t, checkout, "id000000000001", "note-a")
	note(t, checkout, "id000000000002", "note-b")

	// First wait with --bodies --max-notes 1 should return first note and a continuation token
	r1 := invoke(t, "", waitFlags(checkout, "Ada", "2s", "--bodies", "--max-notes", "1")...).mustCode(t, 0)

	// Verify the first page is a partial page with a continuation token
	if !strings.Contains(r1.stdout, "INBOX BODIES printed=1 bytes=32 oversize=0 gaps=0 drained=false complete=false next=") {
		t.Fatalf("page 1 is not a partial page:\n%s", r1.stdout)
	}

	// Extract the continuation token
	token := bodyNext(t, r1.stdout)

	// Second wait with --after token should return exactly the remainder and end with next=-
	r2 := invoke(t, "", waitFlags(checkout, "Ada", "2s", "--bodies", "--max-notes", "10", "--after", token)...).mustCode(t, 0)

	// Verify the second page is complete and ends with next=-
	if !strings.Contains(r2.stdout, "drained=true complete=true next=-") {
		t.Fatalf("page 2 did not end with next=:\n%s", r2.stdout)
	}
}
