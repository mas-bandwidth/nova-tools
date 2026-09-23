package main

// issue2683_test.go is the red-first check of nova-tools #2683: the 1 s table's
// friend columns come from the store -- the sprint task hashes and the read events --
// so there is no per-tick GitHub call. The done column is the half this tool feeds:
// a typed line at the keyboard is the read (#2678), and `nova-merge read` is where
// this tool parses it. With --redis naming the store, the verb writes one kind=read
// event to cards:done beside the immutable record, and internal/friendread folds
// that entry into the friend's done for the day.

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/friendread"
)

func TestIssue2683(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	oid := setupPR(t, l, 951, "feature-a", "a.txt", false)
	mr := miniredis.RunT(t)
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	defer rdb.Close()

	// A typed line with no store named writes nothing: there is nowhere to write,
	// and the lane-branch record stays the only truth.
	exit, stdout, stderr := l.run("read", "--lane", l.lane, "--pr", "951", "--who", "emma",
		"--head", oid, "--verdict", "approve")
	if exit != 0 {
		t.Fatalf("read without --redis: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	if n, err := rdb.XLen(ctx, friendread.Stream).Result(); err != nil || n != 0 {
		t.Fatalf("with no --redis the stream holds %d entries (%v), want 0: no store was named", n, err)
	}

	// The same typed line with --redis becomes one kind=read event on cards:done.
	exit, stdout, stderr = l.run("read", "--lane", l.lane, "--pr", "951", "--who", "emma",
		"--head", oid, "--verdict", "approve", "--redis", mr.Addr())
	if exit != 0 {
		t.Fatalf("read --redis: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stdout, "READ OK")
	msgs, err := rdb.XRangeN(ctx, friendread.Stream, "-", "+", 10).Result()
	if err != nil {
		t.Fatalf("reading the stream back: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("the stream holds %d entries, want the one read event", len(msgs))
	}
	got := map[string]string{}
	for k, v := range msgs[0].Values {
		s, ok := v.(string)
		if !ok {
			t.Fatalf("event field %s is %T, not a string", k, v)
		}
		got[k] = s
	}
	for name, want := range map[string]string{
		"event":   friendread.EventRead,
		"who":     "emma",
		"verdict": "APPROVE",
		"head":    oid,
		"pr":      "951",
		"at":      "2026-09-11T13:00:00Z",
	} {
		if got[name] != want {
			t.Errorf("event field %s = %q, want %q", name, got[name], want)
		}
	}

	// The fold the 1 s table runs counts this friend done=1 for the day -- from the
	// store, with no GitHub scan.
	res, err := friendread.Count([]friendread.Entry{{ID: msgs[0].ID, Fields: got}}, "2026-09-11", []string{"emma"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Friends) != 1 || res.Friends[0].Done != 1 {
		t.Fatalf("the store's done count for emma on 2026-09-11 = %+v, want one friend at done=1", res.Friends)
	}
}
