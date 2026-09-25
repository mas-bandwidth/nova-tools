package read_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/read"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
)

// seedCopy is the work copy the fixture PR's branch rowan/7-thing names: the
// card 7-thing, taken by friend rowan (a live beat, a lease).
func seedCopy(t *testing.T, c *redis.Client) {
	t.Helper()
	ctx := context.Background()
	c.SAdd(ctx, "friends", "rowan")
	c.HSet(ctx, "friend:rowan:beat", "session", "test", "at", time.Now().UnixMilli())
	if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: "7-thing", Stream: "nova sprint migration", Friend: "rowan", By: "rowan"}); err != nil {
		t.Fatal(err)
	}
	if ids, err := taskcard.Take(ctx, c, "rowan", 1, "rowan", "7-thing"); err != nil || len(ids) != 1 {
		t.Fatalf("take %v %v", ids, err)
	}
}

// TestNoCardNoLanding is #3915's comment (Glenn 2026-09-25 1:50 PM ET): a
// SCORE on a PR whose head branch names no working copy is refused at read
// post with no-copy and nothing written; the same PR from a branch a taken
// copy names is accepted. A card nobody took (dealt, never taken) is not a
// copy.
func TestNoCardNoLanding(t *testing.T) {
	_, base, head := mirrorFixture(t, false)
	c := client(t)
	ctx := context.Background()
	seedRecord(t, c, base, head)
	score := "SCORE who=emma head=" + head + " score=9/10 gates=ci:ok,base:ok,scope:ok"
	post := func() (int, string, string) {
		var stdout, stderr strings.Builder
		code := read.Post(ctx, c, "nova-tools", "7", score, nil, &stdout, &stderr)
		return code, stdout.String(), stderr.String()
	}
	refused := func(when string) {
		t.Helper()
		code, stdout, stderr := post()
		if code != 1 || stdout != "" || !strings.Contains(stderr, "READ POST REFUSED repo=nova-tools n=7 no-copy: nova-tools#7") ||
			!strings.Contains(stderr, "remedy: take a card") {
			t.Fatalf("%s: exit %d stdout %q stderr %q; want REFUSED no-copy", when, code, stdout, stderr)
		}
		if n := c.LLen(ctx, read.LinesKey("nova-tools", "7")).Val(); n != 1 {
			t.Fatalf("%s: %d lines, a refused SCORE wrote", when, n)
		}
	}
	refused("no card at all")

	// dealt to rowan but never taken: no child ran it
	c.SAdd(ctx, "friends", "rowan")
	if _, err := taskcard.Push(ctx, c, taskcard.PushRequest{ID: "7-thing", Stream: "nova sprint migration", Friend: "rowan", By: "rowan"}); err != nil {
		t.Fatal(err)
	}
	refused("a card nobody took")

	c.HSet(ctx, "friend:rowan:beat", "session", "test", "at", time.Now().UnixMilli())
	if _, err := taskcard.Take(ctx, c, "rowan", 1, "rowan", "7-thing"); err != nil {
		t.Fatal(err)
	}
	// a branch that names another card is still no copy
	c.HSet(ctx, read.Key("nova-tools", "7"), "branch", "rowan/8-other")
	refused("a branch naming another card")

	c.HSet(ctx, read.Key("nova-tools", "7"), "branch", "rowan/7-thing")
	if code, stdout, stderr := post(); code != 0 || !strings.Contains(stdout, "kind=SCORE lines=2") {
		t.Fatalf("with the copy: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
	// the copy's lease lapsed (its child died): no live copy, no score
	c.HSet(ctx, "task:7-thing", "lease_until", time.Now().Add(-time.Minute).UnixMilli())
	code, _, stderr := post()
	if code != 1 || !strings.Contains(stderr, "no-copy") {
		t.Fatalf("lapsed copy: exit %d stderr %q", code, stderr)
	}
}
