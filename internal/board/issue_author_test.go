package board

import (
	"context"
	"testing"
	"time"
)

// A comment whose as= names somebody other than its author folds on the AUTHOR
// and not the as= label. The as= remains visible as a display label but the
// card's owner is the comment author. This closes the security finding where
// anybody who may comment on the issue may take or close any card under any name.
func TestTheIssueBackendFoldsOnTheCommentAuthorNotTheAsLabel(t *testing.T) {
	i, err := NewIssue("mas-bandwidth/schema#876", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	// The comment author is "alice" but the as= says "bob".
	// The fold must use "alice" as the owner, not "bob".
	recorded := `[{"id":1,"user":{"login":"alice"},"body":"card 0123456789abcdef0123456789abcdef as=bob at=2026-09-10T09:00:00Z override=false hash=aaaaaaaaaaaa owner=bob by=2026-09-12T09:00:00Z default=bob\\x20files\\x20it: a card"},{"id":2,"user":{"login":"alice"},"body":"taken 0123456789abcdef0123456789abcdef ev=0000000000a1 after=0123456789abcdef0123456789abcdef as=bob at=2026-09-10T09:05:00Z override=false"}]`
	i.run = func(ctx context.Context, stdin string, args ...string) (string, error) {
		return recorded, nil
	}
	log, err := i.Events()
	if err != nil {
		t.Fatal(err)
	}
	if len(log.Lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(log.Lines))
	}
	if len(log.Authors) != 2 {
		t.Fatalf("got %d authors, want 2", len(log.Authors))
	}
	if log.Authors[0] != "alice" {
		t.Errorf("author[0] = %q, want alice", log.Authors[0])
	}
	if log.Authors[1] != "alice" {
		t.Errorf("author[1] = %q, want alice", log.Authors[1])
	}
	b := Derive(log, mustTime(t, "2026-09-10T11:00:00Z"), 10*time.Minute)
	if len(b.Cards) != 1 {
		t.Fatalf("got %d cards, want 1: unparsed=%d", len(b.Cards), b.Unparsed)
	}
	// The owner must be the comment author "alice", not the as= label "bob".
	if b.Cards[0].Owner != "alice" {
		t.Errorf("owner = %q, want the comment author alice, not the as= label bob", b.Cards[0].Owner)
	}
}
