package bus

import (
	"testing"
)

// TestWaitOnNoteCursorAdvancesOnlyPastAcknowledged pins docs/SPEC-BUS.md's complete-batch
// cursor contract: the cursor is the acknowledgement, so with --advance it moves only past
// the last whole batch whose bytes fully reached the caller, a crash mid-batch redelivers
// from that batch, and a run without --advance moves no cursor.
func TestWaitOnNoteCursorAdvancesOnlyPastAcknowledged(t *testing.T) {
	items := []BodyItem{
		{Commit: paginationOne, Path: "from-bo/a.md", Entry: OpenEntry{Path: "from-bo/a.md"}, Body: []byte("a")},
		{Commit: paginationTwo, Path: "from-bo/b.md", Entry: OpenEntry{Path: "from-bo/b.md"}, Body: []byte("b")},
		{Commit: paginationTwo, Path: "from-bo/c.md", Entry: OpenEntry{Path: "from-bo/c.md"}, Body: []byte("c")},
	}
	const cursor = paginationBase

	t.Run("a cut mid-batch advances only past the last whole batch", func(t *testing.T) {
		// MaxNotes=2 cuts inside paginationTwo: a.md and b.md print, c.md is left for a
		// continuation. Only paginationOne is a whole batch fully delivered, so that is
		// the furthest the cursor may move.
		request := paginationRequest("", cursor, 2, 1<<20)
		request.Advance = true
		page, err := BodyPageFor(items, request)
		if err != nil {
			t.Fatal(err)
		}
		if page.SafeFrontier != paginationOne {
			t.Fatalf("a page cut inside %s advanced the cursor into the partial batch: SafeFrontier=%s want %s", paginationTwo, page.SafeFrontier, paginationOne)
		}
		if page.Next == "" {
			t.Fatalf("a page cut mid-batch must leave a continuation: %+v", page)
		}
		_, expected, err := BodyContinuation(page.Next)
		if err != nil {
			t.Fatal(err)
		}
		if expected != paginationOne {
			t.Fatalf("the continuation expects cursor %s, want the last whole-batch frontier %s", expected, paginationOne)
		}
	})

	t.Run("the next run redelivers the unfinished batch from the frontier", func(t *testing.T) {
		// The reader advanced to the safe frontier and then crashed. The next run starts
		// a fresh snapshot from that cursor, and the partially delivered batch must be
		// delivered in full, not skipped.
		fresh := paginationRequest("", paginationOne, 10, 1<<20)
		fresh.Snapshot.Base = paginationOne
		fresh.Snapshot.Head = paginationTwo
		fresh.Advance = true
		page, err := BodyPageFor(items[1:], fresh)
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) != 2 || !page.Complete || page.SafeFrontier != paginationTwo {
			t.Fatalf("the next run did not redeliver the whole unfinished batch: %+v", page)
		}
	})

	t.Run("without --advance the cursor does not move", func(t *testing.T) {
		page, err := BodyPageFor(items, paginationRequest("", cursor, 2, 1<<20))
		if err != nil {
			t.Fatal(err)
		}
		if page.Next == "" {
			t.Fatalf("a page cut mid-batch must leave a continuation: %+v", page)
		}
		_, expected, err := BodyContinuation(page.Next)
		if err != nil {
			t.Fatal(err)
		}
		if expected != cursor {
			t.Fatalf("a run without --advance moved the cursor to %s, want it unchanged at %s", expected, cursor)
		}
	})
}
