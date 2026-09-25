package main

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

// TestTaskCardDispatch: the card form is chosen by its flags, and the one
// task store's and the batch forms keep theirs.
func TestTaskCardDispatch(t *testing.T) {
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{[]string{"push", "--actor", "rowan", "--id", "x"}, true},
		{[]string{"push", "--id", "x", "--to", "a"}, false},
		{[]string{"take", "--actor", "rowan"}, true},
		{[]string{"take", "--as", "a"}, false},
		{[]string{"done", "--actor", "rowan", "--id", "x", "--evidence", "e"}, true},
		{[]string{"done", "--actor", "rowan", "--id", "x", "--token", "t"}, false},
		{[]string{"cancel", "--actor", "rowan", "--ids", "a,b"}, false},
		{[]string{"cancel", "--actor", "rowan", "--id", "x", "--why", "w"}, true},
		{[]string{"move", "--id", "x", "--to", "b", "--actor", "a"}, false},
		{[]string{"move", "--id", "x", "--to-friend", "b", "--actor", "a"}, true},
		{[]string{"front", "--id", "x", "--as", "a", "--actor", "a"}, false},
		{[]string{"land", "--id", "x"}, true},
		{[]string{"fsck"}, true},
		{[]string{"ls"}, true},
		{[]string{"migrate"}, true},
		{[]string{"list", "--actor", "a"}, false},
	} {
		if got := isTaskCard(tc.args[0], tc.args[1:]); got != tc.want {
			t.Errorf("isTaskCard(%v) = %v, want %v", tc.args, got, tc.want)
		}
	}
}

var cardLine = regexp.MustCompile(`^TASK (\w+) id=(\S+) from=(\S+) to=(\S+) ms=\d+\n$`)

// TestTaskCardCLI walks one task through the verbs and proves the table
// (ZCARDs) and fsck after each, then a refusal, drift and the usage.
func TestTaskCardCLI(t *testing.T) {
	f := newSeat(t)
	t.Setenv("FRIEND_QUEUE_SPRINT", seatSprint)
	t.Setenv("NOVA_FRIEND", "") // a coordinator shell: --actor names itself
	ctx := context.Background()
	c := f.client
	const s = "nova-sprint + merge + bus"
	zc := func(w string) int64 { return c.ZCard(ctx, "ws:"+s+":"+w).Val() }

	code, out, errOut := runTaskCLI("push", "--actor", "rowan", "--id", "build-1", "--stream", s, "--friend", "a",
		"--kind", "build", "--ref", "nova-tools#3778", "--title", "tasks are cards", "--pr", "3790")
	if m := cardLine.FindStringSubmatch(out); code != 0 || errOut != "" || m == nil || m[4] != "ready" {
		t.Fatalf("push = %d %q %q", code, out, errOut)
	}
	code, out, _ = runTaskCLI("take", "--actor", "a")
	if code != 0 || !strings.HasPrefix(out, "TASK take n=1 ids=build-1 ms=") || zc("ready") != 0 || zc("working") != 1 {
		t.Fatalf("take = %d %q", code, out)
	}
	code, out, _ = runTaskCLI("beat", "--actor", "a", "--id", "build-1")
	if code != 0 || !strings.HasPrefix(out, "TASK beat id=build-1 lease_until=") {
		t.Fatalf("beat = %d %q", code, out)
	}
	code, out, _ = runTaskCLI("done", "--actor", "a", "--id", "build-1", "--evidence", "PR #3790")
	if m := cardLine.FindStringSubmatch(out); code != 0 || m == nil || m[3] != "working" || m[4] != "merging" || zc("merging") != 1 {
		t.Fatalf("done = %d %q", code, out)
	}
	code, out, _ = runTaskCLI("land", "--actor", "lander", "--id", "build-1", "--sha", "0123abcd")
	if m := cardLine.FindStringSubmatch(out); code != 0 || m == nil || m[4] != "landed" || zc("landed") != 1 || zc("merging") != 0 {
		t.Fatalf("land = %d %q", code, out)
	}
	code, out, _ = runTaskCLI("ls", "--stream", s, "--where", "landed")
	if code != 0 || !strings.HasPrefix(out, "build-1\nTASK ls n=1 where=landed ms=") {
		t.Fatalf("ls = %d %q", code, out)
	}
	code, out, _ = runTaskCLI("fsck", "--sprint", seatSprint)
	if code != 0 || !strings.Contains(out, " landed=1 ") || !strings.Contains(out, " drift=0 ") {
		t.Fatalf("fsck = %d %q", code, out)
	}

	// Off the graph: exit 1, nothing written.
	code, out, _ = runTaskCLI("unblock", "--actor", "a", "--id", "build-1")
	if code != 1 || !strings.HasPrefix(out, "TASK unblock REFUSED id=build-1 why=") || !strings.Contains(out, "OFFGRAPH landed -> ready") {
		t.Fatalf("unblock a landed task = %d %q", code, out)
	}
	// Drift by hand: fsck names it and exits 1.
	c.ZAdd(ctx, "ws:"+s+":ready", redis.Z{Score: 1, Member: "build-1"})
	code, out, _ = runTaskCLI("fsck", "--sprint", seatSprint)
	if code != 1 || !strings.Contains(out, "DRIFT stray ws:"+s+":ready build-1") || !strings.Contains(out, " drift=1 ") {
		t.Fatalf("fsck over drift = %d %q", code, out)
	}
	c.ZRem(ctx, "ws:"+s+":ready", "build-1")

	// migrate over a truth file, then fsck clean.
	c.HSet(ctx, "task:old-1", "kind", "read", "ref", "nova-tools#1", "title", "STREAM: "+s+" | read", "owner", "b", "state", "open", "created_at", "2026-09-25T07:00:00Z")
	c.SAdd(ctx, "sprint:"+seatSprint+":idx:b:open", "old-1")
	truth := filepath.Join(t.TempDir(), "truth.txt")
	if err := os.WriteFile(truth, []byte(s+"|ready|old-1|read|nova-tools#1|merged\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out, errOut = runTaskCLI("migrate", "--actor", "rowan", "--sprint", seatSprint, "--truth", truth)
	if code != 0 || !strings.HasPrefix(out, "TASK migrate scanned=") || !strings.Contains(out, " landed=2 ") {
		t.Fatalf("migrate = %d %q %q", code, out, errOut)
	}
	if code, out, _ = runTaskCLI("fsck", "--sprint", seatSprint); code != 0 {
		t.Fatalf("fsck after migrate = %d %q", code, out)
	}

	// Usage.
	if code, out, _ = runTaskCLI("land", "--help"); code != 0 || !strings.Contains(out, "nova-sprint task land") {
		t.Fatalf("help = %d %q", code, out)
	}
	if code, _, errOut = runTaskCLI("land", "--actor", "a", "--id", "x"); code != 2 || !strings.Contains(errOut, "--sha is required") {
		t.Fatalf("land without --sha = %d %q", code, errOut)
	}
}

// TestTaskMoveRefusesAPrimaryUnread (#3929, rowan-new specs/table-moves.md
// 2026-09-25: a copy's OK is the only event that advances a primary): the
// hand walk waiting -> ready -> working -> merging is refused at working for
// a task no friend holds, and a friendless working task cannot be moved to
// merging by hand; each refusal is exit 1 with nothing written. A friend's
// take keeps its own path.
func TestTaskMoveRefusesAPrimaryUnread(t *testing.T) {
	f := newSeat(t)
	t.Setenv("FRIEND_QUEUE_SPRINT", seatSprint)
	t.Setenv("NOVA_FRIEND", "")
	ctx := context.Background()
	c := f.client
	const s = "nova-sprint + merge + bus"
	zc := func(w string) int64 { return c.ZCard(ctx, "ws:"+s+":"+w).Val() }

	code, out, errOut := runTaskCLI("push", "--actor", "rowan", "--id", "prim-1", "--stream", s, "--waiting",
		"--kind", "build", "--ref", "nova-tools#3929", "--title", "a primary")
	if m := cardLine.FindStringSubmatch(out); code != 0 || m == nil || m[4] != "waiting" {
		t.Fatalf("push = %d %q %q", code, out, errOut)
	}
	code, out, _ = runTaskCLI("move", "--actor", "rowan", "--id", "prim-1", "--to-where", "ready")
	if m := cardLine.FindStringSubmatch(out); code != 0 || m == nil || m[4] != "ready" {
		t.Fatalf("waiting -> ready = %d %q", code, out)
	}
	for _, to := range []string{"working", "merging"} {
		code, out, _ = runTaskCLI("move", "--actor", "rowan", "--id", "prim-1", "--to-where", to)
		if code != 1 || !strings.HasPrefix(out, "TASK move REFUSED id=prim-1 why=") || (to == "working" && !strings.Contains(out, "NOCOPY")) {
			t.Fatalf("ready -> %s by hand = %d %q", to, code, out)
		}
		if w := c.HGet(ctx, "task:prim-1", "where").Val(); w != "ready" || zc("ready") != 1 || zc(to) != 0 {
			t.Fatalf("a refused move to %s wrote: where=%s", to, w)
		}
	}

	// A friendless task already in working (a record from before the copies)
	// cannot be moved to merging by hand either.
	c.HSet(ctx, "task:prim-2", "stream", s, "state", "working", "kind", "build", "created_at", "1000")
	c.ZAdd(ctx, "ws:"+s+":working", redis.Z{Score: 1000, Member: "prim-2"})
	code, out, _ = runTaskCLI("move", "--actor", "rowan", "--id", "prim-2", "--to-where", "merging")
	if code != 1 || !strings.Contains(out, "NOCOPY a primary enters merging only as a read copy's card end") {
		t.Fatalf("working -> merging by hand = %d %q", code, out)
	}
	if zc("merging") != 0 {
		t.Fatalf("a refused move wrote merging")
	}

	// A friend's take keeps its own path: ready -> working with the friend.
	code, out, errOut = runTaskCLI("push", "--actor", "rowan", "--id", "held-1", "--stream", s, "--friend", "a",
		"--kind", "build", "--ref", "nova-tools#3930", "--title", "a friend's task")
	if code != 0 {
		t.Fatalf("push held-1 = %d %q %q", code, out, errOut)
	}
	code, out, _ = runTaskCLI("take", "--actor", "a", "--id", "held-1")
	if code != 0 || !strings.Contains(out, "held-1") || c.HGet(ctx, "task:held-1", "where").Val() != "working" {
		t.Fatalf("friend take = %d %q", code, out)
	}
}
