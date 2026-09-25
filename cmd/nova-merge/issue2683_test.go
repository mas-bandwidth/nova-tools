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

	// The store cannot be reached: the record is pushed, the event is not, and the verb
	// says so and tells the caller to re-run it. Nothing reaches the stream.
	dead := miniredis.RunT(t)
	deadAddr := dead.Addr()
	dead.Close()
	exit, stdout, stderr = l.run("read", "--lane", l.lane, "--pr", "951", "--who", "emma",
		"--head", oid, "--verdict", "approve", "--redis", deadAddr)
	if exit != 0 {
		t.Fatalf("read --redis <unreachable>: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	contains(t, stderr, "READ NOTE")
	if n, err := rdb.XLen(ctx, friendread.Stream).Result(); err != nil || n != 0 {
		t.Fatalf("after a failed store write the stream holds %d entries (%v), want 0", n, err)
	}

	// The re-run the NOTE asks for: the same typed line with --redis becomes one
	// kind=read event on cards:done.
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

	if got["read"] == "" {
		t.Errorf("the event carries no read identity; a retry could not be told from a second read")
	}

	// A retry of the same read (the verb re-run once more after its event was posted)
	// appends nothing: the event's identity is the read, not the stream id XADD mints.
	exit, stdout, stderr = l.run("read", "--lane", l.lane, "--pr", "951", "--who", "emma",
		"--head", oid, "--verdict", "approve", "--redis", mr.Addr())
	if exit != 0 {
		t.Fatalf("read --redis retry: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	msgs, err = rdb.XRangeN(ctx, friendread.Stream, "-", "+", 10).Result()
	if err != nil {
		t.Fatalf("reading the stream back after the retry: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("after a retry the stream holds %d entries, want still the one read event", len(msgs))
	}

	// The fold the 1 s table runs, over everything on the stream, counts this friend
	// done=1 for the day -- from the store, with no GitHub scan, and once despite the
	// failed write and the retry.
	var entries []friendread.Entry
	for _, m := range msgs {
		fields := map[string]string{}
		for k, v := range m.Values {
			fields[k], _ = v.(string)
		}
		entries = append(entries, friendread.Entry{ID: m.ID, Fields: fields})
	}
	res, err := friendread.Count(entries, "2026-09-11", []string{"emma"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Friends) != 1 || res.Friends[0].Done != 1 {
		t.Fatalf("the store's done count for emma on 2026-09-11 = %+v, want one friend at done=1", res.Friends)
	}
}
