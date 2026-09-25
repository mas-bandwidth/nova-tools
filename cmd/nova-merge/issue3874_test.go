package main

// issue3874_test.go: nova-merge read --redis posts the read to the one read record
// (nova-tools #3874), the typed line records ns_line_post writes and the stream
// lander reads. It replaces #2683's kind=read event on cards:done behind a 7-day
// claim key: no stream entry and no cards:done:read:* key is written.

import (
	"context"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/land/stream"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

func TestIssue3874ReadPostsTheReadRecord(t *testing.T) {
	t.Parallel()
	l := newLab(t)
	oid := setupPR(t, l, 951, "feature-a", "a.txt", false)
	addr := testutil.Start(t)
	ctx := context.Background()
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	defer rdb.Close()
	if err := fn.Load(ctx, rdb); err != nil {
		t.Fatal(err)
	}
	read := func(extra ...string) (int, string, string) {
		return l.run(append([]string{"read", "--lane", l.lane, "--pr", "951", "--who", "emma", "--head", oid}, extra...)...)
	}

	// Usage: an approve in the store is a SCORE and needs --score; --score needs the
	// store; the store keys a read by PR.
	for _, args := range [][]string{
		{"--verdict", "approve", "--redis", addr},
		{"--verdict", "approve", "--redis", addr, "--score", "11"},
		{"--verdict", "approve", "--score", "9"},
	} {
		if exit, _, stderr := read(args...); exit != 2 {
			t.Errorf("%v: exit %d, want 2\n%s", args, exit, stderr)
		}
	}
	if exit, _, stderr := l.run("read", "--lane", l.lane, "--branch", "feature-a", "--who", "emma", "--head", oid,
		"--verdict", "hold", "--redis", addr); exit != 2 || !strings.Contains(stderr, "a branch entry has no PR") {
		t.Errorf("--branch --redis: exit %d\n%s", exit, stderr)
	}

	// No PR record in the store: refused, nothing written.
	exit, _, stderr := read("--verdict", "approve", "--score", "9", "--redis", addr)
	if exit != 1 || !strings.Contains(stderr, "READ REFUSED pr:n:951 has no head") {
		t.Fatalf("no record: exit %d\n%s", exit, stderr)
	}
	if err := rdb.HSet(ctx, "pr:n:951", "repo", "n", "n", "951", "head", oid, "base", "main", "state", "open", "ci", "green").Err(); err != nil {
		t.Fatal(err)
	}

	// The read is one line record, and a re-run replaces it: one read, not two.
	for i := 0; i < 2; i++ {
		exit, stdout, stderr := read("--verdict", "approve", "--score", "9", "--note", "fine", "--redis", addr)
		if exit != 0 {
			t.Fatalf("read --redis run %d: exit %d\n%s\n%s", i, exit, stdout, stderr)
		}
		contains(t, stdout, "READ OK")
		contains(t, stdout, "record=pr:n:951:line:"+oid+":emma:SCORE")
	}
	rec := rdb.HGetAll(ctx, "pr:n:951:line:"+oid+":emma:SCORE").Val()
	if rec["score"] != "9" || rec["text"] != "SCORE who=emma head="+oid+" score=9/10\nfine" {
		t.Fatalf("line record %v", rec)
	}
	if n := rdb.ZCard(ctx, "pr:n:951:line:"+oid).Val(); n != 1 {
		t.Fatalf("%d line records at the head, want 1", n)
	}
	recs, err := stream.LoadPRs(ctx, rdb, "n", []int{951})
	if err != nil || len(recs[0].Reads) != 1 || stream.ReadAt(recs[0].Reads, oid).Who != "emma" {
		t.Fatalf("the lander sees %q %v, want emma's one read", recs[0].Reads, err)
	}

	// A hold replaces the approve as emma's current read: the lander is held.
	if exit, stdout, stderr := read("--verdict", "hold", "--redis", addr); exit != 0 {
		t.Fatalf("hold: exit %d\n%s\n%s", exit, stdout, stderr)
	}
	recs, _ = stream.LoadPRs(ctx, rdb, "n", []int{951})
	if len(recs[0].Reads) != 1 || stream.ReadAt(recs[0].Reads, oid).Held != "emma" {
		t.Fatalf("after the hold the lander sees %q", recs[0].Reads)
	}

	// No event stream and no claim keys.
	if n := rdb.Exists(ctx, "cards:done").Val(); n != 0 {
		t.Fatal("cards:done was written")
	}
	if keys := rdb.Keys(ctx, "cards:done:read:*").Val(); len(keys) != 0 {
		t.Fatalf("claim keys: %v", keys)
	}

	// A store that cannot be reached refuses the verb.
	exit, _, stderr = read("--verdict", "hold", "--redis", "127.0.0.1:1")
	if exit != 1 || !strings.Contains(stderr, "READ REFUSED the read record could not be written") {
		t.Fatalf("unreachable store: exit %d\n%s", exit, stderr)
	}
}
