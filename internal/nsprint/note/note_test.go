package note

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestLineRefusesEmptyAndFoldsToOneLine(t *testing.T) {
	t.Parallel()
	at := time.UnixMilli(1700000000000)
	if _, err := Line("", at, "x"); err == nil {
		t.Fatal("empty by accepted")
	}
	if _, err := Line("merge-1", at, "  \n "); err == nil {
		t.Fatal("empty text accepted")
	}
	if _, err := Line("a b", at, "x"); err == nil {
		t.Fatal("by with a space accepted")
	}
	l, err := Line("merge-swarm-cards-1", at, "  the tree now\nexpects   taskcard.Opts.Fields ")
	if err != nil || l != "MERGE-NOTE by=merge-swarm-cards-1 at=1700000000000 the tree now expects taskcard.Opts.Fields" {
		t.Fatalf("line %q %v", l, err)
	}
	by, got, text, ok := Parse(l)
	if !ok || by != "merge-swarm-cards-1" || !got.Equal(at) || text != "the tree now expects taskcard.Opts.Fields" {
		t.Fatalf("parse %q %v %q %v", by, got, text, ok)
	}
	if _, _, _, ok := Parse("SCORE who=x"); ok {
		t.Fatal("a SCORE line parsed as a note")
	}
}

func TestPostLoadForCopyAndDrop(t *testing.T) {
	t.Parallel()
	mr := miniredis.RunT(t)
	c := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	at := time.UnixMilli(1700000000000)
	const s = "swarm: cards"
	if n, err := Post(ctx, c, StreamKey(s), "merge-1", "conflict in a.go: keep both", at); err != nil || n != 1 {
		t.Fatalf("post: %d %v", n, err)
	}
	if n, err := Post(ctx, c, StreamKey(s), "merge-1", "wrong pattern: no bash", at.Add(time.Second)); err != nil || n != 2 {
		t.Fatalf("post 2: %d %v", n, err)
	}
	c.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: "sp-1"})
	if _, err := Post(ctx, c, SprintKey("sp-1"), "rowan", "every stream: the moves API changed", at); err != nil {
		t.Fatal(err)
	}
	// A copy with a stream and no sprint field reads its stream's notes then
	// the default sprint's.
	got, err := ForCopy(ctx, c, s, "")
	if err != nil || len(got) != 3 || !strings.Contains(got[0], "conflict in a.go") || !strings.Contains(got[2], "by=rowan") {
		t.Fatalf("for copy: %v %v", got, err)
	}
	// A copy of another stream sees only the sprint's.
	got, err = ForCopy(ctx, c, "other", "sp-1")
	if err != nil || len(got) != 1 || !strings.Contains(got[0], "moves API") {
		t.Fatalf("other stream: %v %v", got, err)
	}
	// The landing drops the stream's notes; the sprint's stay.
	if n, err := Drop(ctx, c, StreamKey(s)); err != nil || n != 2 {
		t.Fatalf("drop: %d %v", n, err)
	}
	got, err = ForCopy(ctx, c, s, "sp-1")
	if err != nil || len(got) != 1 {
		t.Fatalf("after drop: %v %v", got, err)
	}
	if n, err := Drop(ctx, c, StreamKey("never")); err != nil || n != 0 {
		t.Fatalf("drop missing: %d %v", n, err)
	}
}
