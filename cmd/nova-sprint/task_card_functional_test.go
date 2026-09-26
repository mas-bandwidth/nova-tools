//go:build functional

package main

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws/wstest"
)

// TestTaskCardDispatch: the card form is chosen by its flags, and the one
// task store's and the batch forms keep theirs.
// runCLI runs one nova-sprint argv and returns its exit code, stdout and
// stderr.
func runCLI(args ...string) (int, string, string) {
	var stdout, stderr bytes.Buffer
	code := run(args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// runTaskCLI runs one task verb argv and returns its exit code, stdout and
// stderr.
func runTaskCLI(args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := runTask(context.Background(), args, &out, &errOut)
	return code, out.String(), errOut.String()
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

	code, out, errOut := runTaskCLI("push", "--as", "rowan", "--ids", "build-1", "--stream", s, "--to", "a",
		"--kind", "build", "--ref", "nova-tools#3778", "--title", "tasks are cards", "--pr", "3790")
	if m := cardLine.FindStringSubmatch(out); code != 0 || errOut != "" || m == nil || m[4] != "ready" {
		t.Fatalf("push = %d %q %q", code, out, errOut)
	}
	code, out, _ = runTaskCLI("take", "--as", "a")
	if code != 0 || !strings.HasPrefix(out, "TASK take n=1 ids=build-1 ms=") || zc("ready") != 0 || zc("working") != 1 {
		t.Fatalf("take = %d %q", code, out)
	}
	code, out, _ = runTaskCLI("beat", "--as", "a", "--ids", "build-1")
	if code != 0 || !strings.HasPrefix(out, "TASK beat id=build-1 lease_until=") {
		t.Fatalf("beat = %d %q", code, out)
	}
	code, out, _ = runTaskCLI("done", "--as", "a", "--ids", "build-1", "--evidence", "PR #3790")
	if m := cardLine.FindStringSubmatch(out); code != 0 || m == nil || m[3] != "working" || m[4] != "merging" || zc("merging") != 1 {
		t.Fatalf("done = %d %q", code, out)
	}
	// the last card's landing lands the stream's stop with it (#4318): two in landed
	code, out, _ = runTaskCLI("land", "--as", "lander", "--ids", "build-1", "--sha", "0123abcd")
	if m := cardLine.FindStringSubmatch(out); code != 0 || m == nil || m[4] != "landed" || zc("landed") != 2 || zc("merging") != 0 {
		t.Fatalf("land = %d %q", code, out)
	}
	// ls lists the set as it is: the card and the stream's stop
	code, out, _ = runTaskCLI("ls", "--stream", s, "--where", "landed")
	if code != 0 || !strings.HasPrefix(out, "build-1\n"+ws.SentinelID(s)+"\nTASK ls n=2 where=landed ms=") {
		t.Fatalf("ls = %d %q", code, out)
	}
	code, out, _ = runTaskCLI("fsck", "--sprint", seatSprint)
	if code != 0 || !strings.Contains(out, " landed=2 ") || !strings.Contains(out, " drift=0 ") {
		t.Fatalf("fsck = %d %q", code, out)
	}

	// Off the graph: exit 1, nothing written.
	code, out, _ = runTaskCLI("unblock", "--as", "a", "--ids", "build-1")
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

	if code, out, _ = runTaskCLI("land", "--help"); code != 0 || !strings.Contains(out, "nova-sprint task land") {
		t.Fatalf("help = %d %q", code, out)
	}
	if code, _, errOut = runTaskCLI("land", "--as", "a", "--ids", "x"); code != 2 || !strings.Contains(errOut, "--sha is required") {
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

	code, out, errOut := runTaskCLI("push", "--as", "rowan", "--ids", "prim-1", "--stream", s, "--waiting",
		"--kind", "build", "--ref", "nova-tools#3929", "--title", "a primary")
	if m := cardLine.FindStringSubmatch(out); code != 0 || m == nil || m[4] != "waiting" {
		t.Fatalf("push = %d %q %q", code, out, errOut)
	}
	code, out, _ = runTaskCLI("move", "--as", "rowan", "--ids", "prim-1", "--where", "ready")
	if m := cardLine.FindStringSubmatch(out); code != 0 || m == nil || m[4] != "ready" {
		t.Fatalf("waiting -> ready = %d %q", code, out)
	}
	for _, to := range []string{"working", "merging"} {
		code, out, _ = runTaskCLI("move", "--as", "rowan", "--ids", "prim-1", "--where", to)
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
	code, out, _ = runTaskCLI("move", "--as", "rowan", "--ids", "prim-2", "--where", "merging")
	if code != 1 || !strings.Contains(out, "NOCOPY a primary enters merging only as a read copy's card end") {
		t.Fatalf("working -> merging by hand = %d %q", code, out)
	}
	if zc("merging") != 0 {
		t.Fatalf("a refused move wrote merging")
	}

	// A friend's take keeps its own path: ready -> working with the friend.
	code, out, errOut = runTaskCLI("push", "--as", "rowan", "--ids", "held-1", "--stream", s, "--to", "a",
		"--kind", "build", "--ref", "nova-tools#3930", "--title", "a friend's task")
	if code != 0 {
		t.Fatalf("push held-1 = %d %q %q", code, out, errOut)
	}
	code, out, _ = runTaskCLI("take", "--as", "a", "--ids", "held-1")
	if code != 0 || !strings.Contains(out, "held-1") || c.HGet(ctx, "task:held-1", "where").Val() != "working" {
		t.Fatalf("friend take = %d %q", code, out)
	}
}

// TestTaskFsckNamesAndRepairsAMissingSentinel (#4318): a registered stream
// with no sentinel (written straight into the keys, as every stream before
// this change was) is a NOSENTINEL drift line naming the remedy, and task
// fsck --repair creates it; a second fsck is clean.
func TestTaskFsckNamesAndRepairsAMissingSentinel(t *testing.T) {
	t.Parallel()
	addr, c := wstest.Start(t)
	ctx := context.Background()
	fsck := func(args ...string) (int, string, string) {
		return runTaskCLI(append([]string{"fsck", "--redis", addr, "--sprint", "s1"}, args...)...)
	}
	if err := c.SAdd(ctx, "ws:names", "fleet").Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.ZAdd(ctx, "ws:order", redis.Z{Score: 1, Member: "fleet"}).Err(); err != nil {
		t.Fatal(err)
	}
	code, out, _ := fsck()
	if code != 1 || !strings.Contains(out, "DRIFT NOSENTINEL stream=fleet id=fleet:sentinel (no record; nova-sprint task fsck --repair creates it)\n") || !strings.Contains(out, " drift=1 ") {
		t.Fatalf("fsck without the stop = %d %q", code, out)
	}
	code, out, _ = fsck("--repair")
	if code != 0 || !strings.HasPrefix(out, "SENTINEL created stream=fleet id=fleet:sentinel\nTASK fsck repair sentinels=1 created=1\n") || !strings.Contains(out, " drift=0 ") {
		t.Fatalf("fsck --repair = %d %q", code, out)
	}
	if w, _ := c.HGet(ctx, "task:fleet:sentinel", "where").Result(); w != "waiting" {
		t.Fatalf("stop where %q, want waiting", w)
	}
	code, out, _ = fsck("--repair")
	if code != 0 || !strings.HasPrefix(out, "TASK fsck repair sentinels=0 created=0\n") {
		t.Fatalf("second repair = %d %q", code, out)
	}
}
