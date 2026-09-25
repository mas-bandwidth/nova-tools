package main

// nova-tools#3370: a SPEC line's facts (who, rev, score, stream) go to Redis
// in the same call that stores the line, the second distinct 10 at the
// current rev moves the spec working -> done and releases every task waiting
// on spec:<repo>#<n>, and `spec list` reads only Redis.

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/spec"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

// specStore is a throwaway Redis with the library loaded and one open sprint
// first in sprint:order, with friend a able to hold tasks.
func specStore(t *testing.T) (string, *redis.Client, *store.Store, string) {
	t.Helper()
	t.Setenv(store.UserEnv, "")
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatalf("load library: %v", err)
	}
	const S = "spec-t"
	c.HSet(ctx, "s:"+S, "status", "open")
	c.ZAdd(ctx, "sprint:order", redis.Z{Score: 1, Member: S})
	c.SAdd(ctx, "friends", "a")
	c.HSet(ctx, "friend:a:desired", "slots", 4, "paused", "0")
	c.HSet(ctx, "friend:a:beat", "host", "fixture")
	return addr, c, store.New(c), S
}

func runSpecArgs(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := runSpec(context.Background(), args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestSpecMarkDoneOnTwoTensFiresSpecUnblock(t *testing.T) {
	addr, c, st, S := specStore(t)
	ctx := context.Background()
	const cond = "spec:nova-tools#3370"
	res, err := task.PushChecked(ctx, st, task.PushRequest{Sprint: S, ID: "b1", Kind: task.KindFix, Title: "build 1", To: "a", Actor: "a", DependsOn: cond})
	if err != nil || res.Waiting != 1 {
		t.Fatalf("push waiting on %s = %+v %v", cond, res, err)
	}
	state := func() string { return c.HGet(ctx, "task:b1", "state").Val() } // one task store (#3907)
	mark := func(who, rev, score string) (int, string, string) {
		return runSpecArgs(t, "mark", "mas-bandwidth/nova-tools#3370", "--rev", rev, "--who", who, "--score", score, "--stream", "nova-sprint", "--redis", addr)
	}

	// A 10 at an older rev never counts toward the current rev.
	if code, out, errOut := mark("rowan", "2", "10"); code != 0 || !strings.Contains(out, "answer=RECORDED state=working tens=1") {
		t.Fatalf("rev 2 rowan 10: exit %d out %q err %q", code, out, errOut)
	}
	if code, out, errOut := mark("stella", "3", "10"); code != 0 || !strings.Contains(out, "answer=NEW_REV state=working tens=1 released=0") {
		t.Fatalf("rev 3 stella 10: exit %d out %q err %q", code, out, errOut)
	}
	if state() != "waiting" {
		t.Fatalf("one 10 at rev 3 released the build: state %q", state())
	}
	// The same fact again writes nothing.
	before := c.LLen(ctx, "pr:nova-tools:3370:lines").Val()
	if code, out, _ := mark("stella", "3", "10"); code != 0 || !strings.Contains(out, "answer=SAME") || c.LLen(ctx, "pr:nova-tools:3370:lines").Val() != before {
		t.Fatalf("repeat mark: exit %d out %q lines %d -> %d", code, out, before, c.LLen(ctx, "pr:nova-tools:3370:lines").Val())
	}
	// A line at a rev older than the current one is refused with nothing written.
	if code, _, errOut := mark("emma", "2", "10"); code != 1 || !strings.Contains(errOut, "STALE_REV") {
		t.Fatalf("stale rev: exit %d err %q", code, errOut)
	}
	if c.HExists(ctx, "pr:nova-tools:3370", "spec_score:emma").Val() {
		t.Fatal("a refused stale-rev mark wrote its score")
	}
	// Under 10 is recorded and does not count.
	if code, out, _ := mark("emma", "3", "9"); code != 0 || !strings.Contains(out, "state=working tens=1") {
		t.Fatalf("emma 9: exit %d out %q", code, out)
	}
	// The second distinct 10 at rev 3: done, and the build is released in the same call.
	code, out, errOut := mark("emma", "3", "10")
	if code != 0 || !strings.Contains(out, "answer=DONE state=done tens=2 released=1 stream=nova-sprint") {
		t.Fatalf("second 10: exit %d out %q err %q", code, out, errOut)
	}
	if state() != "open" {
		t.Fatalf("done spec left its build %q; want open", state())
	}
	// The double link: the record names its set, the set holds the id, and only that set.
	rec := c.HGetAll(ctx, "pr:nova-tools:3370").Val()
	if rec["spec_state"] != "done" || rec["spec_rev"] != "3" || rec["spec_stream"] != "nova-sprint" || rec["spec_score:stella"] != "3 10" || rec["spec_score:emma"] != "3 10" || rec["spec_score:rowan"] != "2 10" {
		t.Fatalf("record = %v", rec)
	}
	if c.ZScore(ctx, "specs:nova-sprint:done", "nova-tools#3370").Err() != nil || c.ZScore(ctx, "specs:nova-sprint:working", "nova-tools#3370").Err() != redis.Nil {
		t.Fatal("done spec is not in exactly the done set")
	}
	if got := c.ZScore(ctx, "specs:nova-sprint:done", "nova-tools#3370").Val(); rec["spec_created_at"] == "" || got <= 0 {
		t.Fatalf("done score %v created_at %q; want the age score", got, rec["spec_created_at"])
	}
	lines := c.LRange(ctx, "pr:nova-tools:3370:lines", 0, -1).Val()
	if len(lines) != 4 || lines[3] != "SPEC who=emma rev=3 score=10" {
		t.Fatalf("lines = %q", lines)
	}
	// A third 10 is recorded and fires nothing again.
	if code, out, _ := mark("johnny", "3", "10"); code != 0 || !strings.Contains(out, "answer=RECORDED state=done tens=3 released=0") {
		t.Fatalf("third 10: exit %d out %q", code, out)
	}
}

func TestSpecsBlockFromRedis(t *testing.T) {
	addr, c, _, _ := specStore(t)
	ctx := context.Background()
	marks := []spec.Mark{
		{Repo: "nova-tools", N: "11", Who: "stella", Rev: 1, Score: 10, Stream: "s1"},
		{Repo: "nova-tools", N: "11", Who: "emma", Rev: 1, Score: 10, Stream: "s1"},
		{Repo: "nova-tools", N: "12", Who: "stella", Rev: 2, Score: 8, Stream: "s1"},
		{Repo: "rowan-tools", N: "5", Who: "emma", Rev: 1, Score: 10, Stream: "s2"},
	}
	for _, m := range marks {
		if r, err := spec.Do(ctx, c, m); err != nil || r.ExitCode() != 0 {
			t.Fatalf("mark %+v = %+v %v", m, r, err)
		}
	}
	code, out, errOut := runSpecArgs(t, "list", "--redis", addr)
	want := "specs | working | done\n" +
		"s1 | 1 | 1\n" +
		"s2 | 1 | 0\n" +
		"SPEC LIST streams=2 working=2 done=1\n"
	if code != 0 || out != want {
		t.Fatalf("spec list: exit %d err %q\n got %q\nwant %q", code, errOut, out, want)
	}
	code, out, _ = runSpecArgs(t, "list", "--stream", "s1", "--redis", addr)
	want = "specs | working | done\n" +
		"s1 | 1 | 1\n" +
		"working nova-tools#12\n" +
		"done nova-tools#11\n" +
		"SPEC LIST streams=1 working=1 done=1\n"
	if code != 0 || out != want {
		t.Fatalf("spec list --stream s1: exit %d\n got %q\nwant %q", code, out, want)
	}
}

// TestReadPostSpecLineWritesFacts: read post of a SPEC line on a spec issue
// (a record with no head) stores the line and its facts in one call.
func TestReadPostSpecLineWritesFacts(t *testing.T) {
	addr, c, _, _ := specStore(t)
	ctx := context.Background()
	sha := strings.Repeat("ab", 32)
	var out, errOut bytes.Buffer
	line := "SPEC who=stella rev=4 sha=" + sha + " score=10/10 gates=test:ok,deps:ok,paths:ok: fine"
	code := runRead(ctx, []string{"post", "--repo", "mas-bandwidth/nova-tools", "--n", "3364", "--line", line, "--no-github", "--redis", addr}, &out, &errOut)
	if code != 0 || !strings.Contains(out.String(), "kind=SPEC spec=RECORDED state=working tens=1") {
		t.Fatalf("read post SPEC: exit %d out %q err %q", code, out.String(), errOut.String())
	}
	rec := c.HGetAll(ctx, "pr:nova-tools:3364").Val()
	if rec["spec_score:stella"] != "4 10" || rec["spec_rev"] != "4" || rec["spec_state"] != "working" || rec["last_line"] != line {
		t.Fatalf("record = %v", rec)
	}
	if got := c.LRange(ctx, "pr:nova-tools:3364:lines", 0, -1).Val(); len(got) != 1 || got[0] != line {
		t.Fatalf("lines = %q", got)
	}
	// A SPEC line without rev= or score= is refused before anything is written.
	out.Reset()
	errOut.Reset()
	code = runRead(ctx, []string{"post", "--repo", "nova-tools", "--n", "3364", "--line", "SPEC who=emma sha=" + sha + " score=10", "--no-github", "--redis", addr}, &out, &errOut)
	if code != 1 || !strings.Contains(errOut.String(), "rev=") || c.LLen(ctx, "pr:nova-tools:3364:lines").Val() != 1 {
		t.Fatalf("SPEC without rev: exit %d err %q", code, errOut.String())
	}
}
