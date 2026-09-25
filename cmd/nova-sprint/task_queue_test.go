package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
)

// TestTaskQueueSubverbs drives the #3206 PR A subverbs through the CLI: a
// push with an unmet DEPENDS-ON prints waiting and is not counted ready,
// resolve makes it ready, and friend down refuses the next push with exit 7.
func TestTaskQueueSubverbs(t *testing.T) {
	f := newSeat(t)
	f.as("a")
	ctx := context.Background()
	run := func(args ...string) (int, string) {
		var out, errOut bytes.Buffer
		var code int
		if args[0] == "friend" {
			code = runFriend(ctx, args[1:], &out, &errOut)
		} else {
			code = runTask(ctx, args, &out, &errOut)
		}
		return code, strings.TrimSpace(out.String() + errOut.String())
	}
	if code, out := run("push", "--id", "w1", "--kind", "fix", "--to", "a", "--depends-on", "mas-bandwidth/nova-tools#1"); code != 0 || out != "PUSH CREATED id=w1 waiting=1" {
		t.Fatalf("push waiting = %d %q", code, out)
	}
	if code, out := run("counts", "--as", "a"); code != 0 || out != "0 0 0" {
		t.Fatalf("counts = %d %q; want 0 0 0", code, out)
	}
	if code, out := run("resolve", "--on", "mas-bandwidth/nova-tools#1", "--asserted"); code != 0 || out != "RESOLVE READY id=mas-bandwidth/nova-tools#1 1" {
		t.Fatalf("resolve = %d %q", code, out)
	}
	if code, out := run("counts", "--as", "a"); code != 0 || out != "0 1 0" {
		t.Fatalf("counts after resolve = %d %q; want 0 1 0", code, out)
	}
	if code, out := run("owners", "--prefix", "w"); code != 0 || out != "w1 a open" {
		t.Fatalf("owners = %d %q", code, out)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	f.client.HSet(ctx, "friend:a", "at", now)
	f.client.HSet(ctx, "friend:b", "at", now)
	if code, out := run("friend", "down", "--as", "a", "--reason", "out of credits", "b"); code != 0 ||
		out != "FRIEND DOWN b\nREBALANCE friend=b from=- to=down moved=0" {
		t.Fatalf("friend down = %d %q", code, out)
	}
	if code, out := run("push", "--id", "p2", "--kind", "fix", "--to", "b"); code != 7 || !strings.HasPrefix(out, "PUSH DOWN id=p2 to=b down=out of credits") {
		t.Fatalf("push to a down friend = %d %q", code, out)
	}
	if code, out := run("push", "--move", "--id", "w1", "--to", "b"); code != 6 || !strings.HasPrefix(out, "MOVE DOWN id=w1") {
		t.Fatalf("move to a down friend = %d %q", code, out)
	}
	if code, out := run("friend", "up", "--as", "a", "b"); code != 0 ||
		out != "FRIEND UP b\nREBALANCE friend=b from=down to=up moved=0" {
		t.Fatalf("friend up = %d %q", code, out)
	}
	if code, out := run("push", "--move", "--id", "w1", "--to", "b"); code != 0 || out != "MOVE MOVED id=w1 a" {
		t.Fatalf("move = %d %q", code, out)
	}
	if code, out := run("close", "--id", "w1", "--evidence", "not needed"); code != 0 || out != "CLOSE CLOSED-NOW id=w1 0" {
		t.Fatalf("close = %d %q", code, out)
	}
}

// TestFriendDownRebalancesAtOnce (#4145): `friend down` moves the friend's
// ready task onto the up friend's queue in the same call and prints the one
// REBALANCE line naming it; the reconciler's next pass has nothing to redo.
func TestFriendDownRebalancesAtOnce(t *testing.T) {
	f := newSeat(t)
	f.as("a")
	ctx := context.Background()
	now := time.Now().UTC().Format(time.RFC3339)
	f.client.HSet(ctx, "friend:a", "at", now)
	f.client.HSet(ctx, "friend:b", "at", now)
	var out, errOut bytes.Buffer
	if code := runTask(ctx, []string{"push", "--id", "r1", "--kind", "fix", "--to", "b"}, &out, &errOut); code != 0 {
		t.Fatalf("push = %d %q %q", code, out.String(), errOut.String())
	}
	if got := f.client.ZRange(ctx, "friend:b:cards:ready", 0, -1).Val(); strings.Join(got, ",") != "r1" {
		t.Fatalf("b ready %v, want r1", got)
	}
	out.Reset()
	errOut.Reset()
	code := runFriend(ctx, []string{"down", "--as", "a", "--reason", "out of credits", "b"}, &out, &errOut)
	want := "FRIEND DOWN b\nREBALANCE friend=b from=- to=down moved=1 r1:a"
	if got := strings.TrimSpace(out.String() + errOut.String()); code != 0 || got != want {
		t.Fatalf("friend down = %d %q, want %q", code, got, want)
	}
	if got := f.client.ZRange(ctx, "friend:a:cards:ready", 0, -1).Val(); strings.Join(got, ",") != "r1" {
		t.Fatalf("a ready %v, want r1 moved by the verb", got)
	}
	if n := f.client.ZCard(ctx, "friend:b:cards:ready").Val(); n != 0 {
		t.Fatalf("b ready %d, want 0", n)
	}
	if got := f.client.HGet(ctx, "friend:status:seen", "b").Val(); got != "down" {
		t.Fatalf("seen b %q, want down so the duty does not repeat the move", got)
	}
}
