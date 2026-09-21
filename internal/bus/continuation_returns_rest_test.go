package bus

import (
	"testing"
)

// TestWaitOnNoteContinuationReturnsTheRest pins docs/SPEC-BUS.md:110: "the next=<token>
// passed back as --after returns exactly the remainder once and ends with next=-."
func TestWaitOnNoteContinuationReturnsTheRest(t *testing.T) {
	t.Parallel()

	items := []BodyItem{
		{Commit: paginationOne, Path: "from-bo/a.md", Entry: OpenEntry{Path: "from-bo/a.md"}, Body: []byte("note-a")},
		{Commit: paginationTwo, Path: "from-bo/b.md", Entry: OpenEntry{Path: "from-bo/b.md"}, Body: []byte("note-b")},
		{Commit: paginationTwo, Path: "from-bo/c.md", Entry: OpenEntry{Path: "from-bo/c.md"}, Body: []byte("note-c")},
	}

	first, err := BodyPageFor(items, paginationRequest("", paginationBase, 1, 6))
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || first.Items[0].Path != "from-bo/a.md" {
		t.Fatalf("first page did not return a: %+v", first)
	}
	if first.Next == "" {
		t.Fatalf("first page did not carry a next= token: %+v", first)
	}

	remainder, err := BodyPageFor(items, paginationRequest(first.Next, paginationBase, 10, 100))
	if err != nil {
		t.Fatal(err)
	}

	if len(remainder.Items) != 2 {
		t.Fatalf("continuation returned %d items, want exactly the 2 remaining: %+v", len(remainder.Items), remainder)
	}
	if remainder.Items[0].Path != "from-bo/b.md" || remainder.Items[1].Path != "from-bo/c.md" {
		t.Fatalf("continuation did not return the remainder in order: %+v", remainder)
	}
	if remainder.Next != "" {
		t.Fatalf("the remainder did not end with next=-: Next=%q", remainder.Next)
	}
	if !remainder.Complete {
		t.Fatalf("the remainder is not Complete: %+v", remainder)
	}
}
