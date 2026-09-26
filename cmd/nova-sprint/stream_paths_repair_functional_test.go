//go:build functional

package main

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

// pathsStore is a throwaway store with the fn library loaded, and its
// task push (task push --paths) and nova-sprint verbs run with --redis.
func pathsStore(t *testing.T) (*redis.Client, func(id, stream, paths string, more ...string) (int, string), func(args ...string) (int, string)) {
	t.Helper()
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	if err := fn.Load(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	push := func(id, stream, paths string, more ...string) (int, string) {
		t.Helper()
		args := append([]string{"push", "--redis", addr, "--actor", "rowan", "--id", id, "--stream", stream, "--title", id,
			"--paths", paths}, more...)
		code, out, errOut := runTaskCLI(args...)
		return code, out + errOut
	}
	cli := func(args ...string) (int, string) {
		t.Helper()
		if len(args) > 1 && args[0] == "scope" && args[1] == "park" {
			args = append(args, "--checkpoint", t.TempDir()+"/park.tsv")
		}
		code, out, errOut := runCLI(append(args, "--redis", addr)...)
		return code, out + errOut
	}
	return c, push, cli
}

// TestPathsRepairWritesTheOverlap is the reader's live-store simulation
// (#4322, fix round 4): a1 lib/core in s1, b1 lib/core/x.go in s2, c1 app/c
// in s3, with stream_paths and ws:paths deleted. ws check --repair writes
// all three streams' paths, prints the pair and exits 1 (nothing left
// unbuilt); pushes that overlap neither side pass (lib/zz into s1, app/d
// into s3, z/new into a new s4); lib/core/y.go into s3 is refused naming s1
// alone (it does not overlap lib/core/x.go) and --join s1 passes;
// lib/core/x.go into s3 is refused naming s1 and s2; ws check keeps
// reporting the pair until the refusal's remedy (park s2) runs, then is
// clean.
func TestPathsRepairWritesTheOverlap(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c, push, cli := pathsStore(t)
	for _, r := range [][3]string{{"a1", "s1", "lib/core"}, {"b1", "s2", "lib/b"}, {"c1", "s3", "app/c"}} {
		if code, out := push(r[0], r[1], r[2]); code != 0 {
			t.Fatalf("push %s: %d %q", r[0], code, out)
		}
	}
	// b1's PATHS as a store cut before #4322 holds them, past the gate
	c.HSet(ctx, "task:b1", "paths", "lib/core/x.go")
	for _, id := range []string{"a1", "b1", "c1"} {
		c.HDel(ctx, "task:"+id, ws.PathsField)
	}
	c.Del(ctx, ws.PathsKey)

	code, out := cli("ws", "check", "--repair")
	if code != 1 || !strings.HasPrefix(out, "PATHS OVERLAP stream=s1 other=s2 paths=lib/core,lib/core/x.go\nCHECK streams=3 cards=3 overlaps=1 stale=0 repaired=3 records=3 unbuilt=0 refused=0 ms=") {
		t.Fatalf("ws check --repair: %d %q", code, out)
	}
	if got := c.HGetAll(ctx, ws.PathsKey).Val(); len(got) != 3 || got["s1"] != "lib/core" || got["s2"] != "lib/core/x.go" || got["s3"] != "app/c" {
		t.Fatalf("ws:paths after the repair = %v", got)
	}

	for _, r := range [][3]string{{"a2", "s1", "lib/zz"}, {"c2", "s3", "app/d"}, {"d1", "s4", "z/new"}} {
		if code, out := push(r[0], r[1], r[2]); code != 0 || !strings.HasPrefix(out, "TASK push id="+r[0]+" ") {
			t.Fatalf("unrelated push %s %s into %s: %d %q", r[0], r[2], r[1], code, out)
		}
	}
	code, out = push("c3", "s3", "lib/core/y.go")
	want := `REFUSED PATHS overlap stream=s1 paths=lib/core,lib/core/y.go remedy="--join s1" id=c3 ms=`
	if code != 1 || !strings.HasPrefix(out, want) {
		t.Fatalf("lib/core/y.go into s3: %d %q; want prefix %q", code, out, want)
	}
	if code, out := push("c3", "s3", "lib/core/y.go", remedyArgs(t, out)...); code != 0 || c.HGet(ctx, "task:c3", "stream").Val() != "s1" {
		t.Fatalf("lib/core/y.go --join s1: %d %q", code, out)
	}
	code, out = push("c4", "s3", "lib/core/x.go")
	want = `REFUSED PATHS overlap stream=s1 also=s2 paths=lib/core,lib/core/x.go remedy="nova-sprint scope park --stream s2" id=c4 ms=`
	if code != 1 || !strings.HasPrefix(out, want) || c.Exists(ctx, "task:c4").Val() != 0 {
		t.Fatalf("lib/core/x.go into s3: %d %q; want prefix %q and nothing written", code, out, want)
	}

	if code, out := cli("ws", "check"); code != 1 || !strings.HasPrefix(out, "PATHS OVERLAP stream=s1 other=s2 paths=lib/core,lib/core/x.go\nCHECK streams=4 cards=7 overlaps=1 stale=0 ") {
		t.Fatalf("ws check before the park: %d %q", code, out)
	}
	if code, out := cli(remedyArgs(t, out)...); code != 0 || !strings.Contains(out, "PARKED") {
		t.Fatalf("scope park --stream s2: %d %q", code, out)
	}
	if code, out := cli("ws", "check"); code != 0 || !strings.HasPrefix(out, "CHECK streams=3 cards=6 overlaps=0 stale=0 ") {
		t.Fatalf("ws check after the park: %d %q", code, out)
	}
	if code, out := push("c4", "s3", "lib/core/x.go", "--join", "s1"); code != 0 {
		t.Fatalf("lib/core/x.go --join s1 after the park: %d %q", code, out)
	}
}

// TestCardEndPathsNeverChangeTheStreams is the card end door (#4322, fix
// round 4): a card end's --paths on a live record (a fail moves the primary
// to review) is recorded as result_paths on the primary; its PATHS,
// stream_paths and ws:paths are unchanged, ws check --repair finds nothing
// to change, and another stream may still take those paths.
func TestCardEndPathsNeverChangeTheStreams(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c, push, cli := pathsStore(t)
	if code, out := push("e1", "ends", "ends/own", "--waiting"); code != 0 {
		t.Fatalf("push e1: %d %q", code, out)
	}
	c.HSet(ctx, "bench:b:desired", "slots", "1")
	if code, out := cli("card", "deal", "--to", "bench:b", "--ids", "e1", "--actor", "rowan"); code != 0 {
		t.Fatalf("deal: %d %q", code, out)
	}
	if code, out := cli("card", "work", "--as", "bench:b", "--fill"); code != 0 {
		t.Fatalf("work: %d %q", code, out)
	}
	code, out := cli("card", "end", "--id", "e1~1", "--fail", "other", "--paths", "evil/pkg")
	if code != 0 || !strings.HasPrefix(out, "ENDED e1~1 primary=e1 from=working to=review ") {
		t.Fatalf("card end --paths: %d %q", code, out)
	}
	rec := c.HGetAll(ctx, "task:e1").Val()
	if rec["paths"] != "ends/own" || rec[ws.PathsField] != "ends/own" || rec["result_paths"] != "evil/pkg" || rec["where"] != "review" {
		t.Fatalf("task:e1 paths=%q stream_paths=%q result_paths=%q where=%q", rec["paths"], rec[ws.PathsField], rec["result_paths"], rec["where"])
	}
	if got := c.HGet(ctx, ws.PathsKey, "ends").Val(); got != "ends/own" {
		t.Fatalf("ws:paths ends = %q after the card end", got)
	}
	if code, out := cli("ws", "check", "--repair"); code != 0 || !strings.Contains(out, " overlaps=0 stale=0 repaired=0 records=0 unbuilt=0 refused=0 ") {
		t.Fatalf("ws check --repair after the card end: %d %q", code, out)
	}
	if code, out := push("o1", "other", "evil/pkg"); code != 0 {
		t.Fatalf("another stream takes evil/pkg: %d %q", code, out)
	}
}

// TestPathsRefusalNeverSuggestsALoop is the reader's loop (#4322, fix round
// 5): s1 holds lib/core and s2 lib/core/x.go (a pair ws check --repair
// wrote); lib/core/x.go into s3 with --join s1, with --join s2 and with no
// --join is refused each time naming both s1 and s2, with remedy scope park
// and never --join, and nothing is written; lib/core/z.go, which s1 alone
// holds, still gets --join s1, and that remedy run as printed passes; the
// park remedy run as printed lets --join s1 take lib/core/x.go.
func TestPathsRefusalNeverSuggestsALoop(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c, push, cli := pathsStore(t)
	for _, r := range [][3]string{{"a1", "s1", "lib/core"}, {"b1", "s2", "lib/b"}} {
		if code, out := push(r[0], r[1], r[2]); code != 0 {
			t.Fatalf("push %s: %d %q", r[0], code, out)
		}
	}
	c.HSet(ctx, "task:b1", "paths", "lib/core/x.go")
	for _, id := range []string{"a1", "b1"} {
		c.HDel(ctx, "task:"+id, ws.PathsField)
	}
	c.Del(ctx, ws.PathsKey)
	if code, out := cli("ws", "check", "--repair"); code != 1 || !strings.HasPrefix(out, "PATHS OVERLAP stream=s1 other=s2 paths=lib/core,lib/core/x.go\n") {
		t.Fatalf("ws check --repair: %d %q", code, out)
	}

	want := `REFUSED PATHS overlap stream=s1 also=s2 paths=lib/core,lib/core/x.go remedy="nova-sprint scope park --stream s2" id=c1 ms=`
	var park string
	for _, join := range [][]string{{"--join", "s1"}, {"--join", "s2"}, nil} {
		code, out := push("c1", "s3", "lib/core/x.go", join...)
		if code != 1 || !strings.HasPrefix(out, want) || strings.Contains(out, "--join") || c.Exists(ctx, "task:c1").Val() != 0 {
			t.Fatalf("lib/core/x.go into s3 %v: %d %q; want prefix %q, no --join and nothing written", join, code, out, want)
		}
		park = out
	}

	code, out := push("c2", "s3", "lib/core/z.go")
	if want := `REFUSED PATHS overlap stream=s1 paths=lib/core,lib/core/z.go remedy="--join s1" id=c2 ms=`; code != 1 || !strings.HasPrefix(out, want) {
		t.Fatalf("lib/core/z.go into s3: %d %q; want prefix %q", code, out, want)
	}
	if code, out := push("c2", "s3", "lib/core/z.go", remedyArgs(t, out)...); code != 0 || c.HGet(ctx, "task:c2", "stream").Val() != "s1" {
		t.Fatalf("lib/core/z.go with its remedy: %d %q", code, out)
	}

	if code, out := cli(remedyArgs(t, park)...); code != 0 || !strings.Contains(out, "PARKED") {
		t.Fatalf("the park remedy: %d %q", code, out)
	}
	if code, out := push("c1", "s3", "lib/core/x.go", "--join", "s1"); code != 0 || c.HGet(ctx, "task:c1", "stream").Val() != "s1" {
		t.Fatalf("lib/core/x.go --join s1 after the park: %d %q", code, out)
	}
}
