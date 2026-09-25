package main

import (
	"context"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

// TestTaskDoneLineVerb (nova-tools #3897): `task done` on a read of a PR
// without --line refuses exit 1 naming --line; --line with the flag close's
// fields is usage (exit 2); a valid --line stores the line and closes the
// card in one call, exit 0 with the line record on the receipt.
func TestTaskDoneLineVerb(t *testing.T) {
	t.Setenv(store.UserEnv, "")
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	const S = "s-3897"
	head := strings.Repeat("d", 40)
	pipe := c.TxPipeline()
	pipe.HSet(ctx, "s:"+S, "status", "open")
	pipe.SAdd(ctx, "friends", "emma")
	pipe.HSet(ctx, "friend:emma:desired", "slots", "4", "paused", "0")
	pipe.HSet(ctx, "friend:emma:beat", "host", "fixture")
	pipe.HSet(ctx, "s:"+S+":pr:nova-tools:7", "head", head)
	pipe.HSet(ctx, "pr:nova-tools:7", "head", head, "base", "dev")
	if _, err := pipe.Exec(ctx); err != nil {
		t.Fatal(err)
	}
	st := store.New(c)
	if got, err := task.Push(ctx, st, task.PushRequest{Sprint: S, ID: "read-7", Kind: task.KindRead, Title: "read nova-tools#7",
		Effects: task.EffectsNone, PayloadSHA: "r7", To: "emma", Repo: "nova-tools", PR: 7, Head: head}); err != nil || got != task.PushCreated {
		t.Fatalf("push: %s, %v", got, err)
	}
	claim, ok, err := task.Take(ctx, st, task.TakeRequest{Sprint: S, ID: "read-7", As: "emma", Actor: "emma"})
	if err != nil || !ok {
		t.Fatalf("take: %v, %v", ok, err)
	}
	base := []string{"task", "done", "--redis", addr, "--sprint", S, "--id", "read-7", "--token", claim.Token}

	code, stdout, stderr := runSprint(append(base, "--evidence", "read on the bus", "--verdict", "APPROVE", "--score", "9", "--head", head)...)
	if code != 1 || !strings.HasPrefix(stdout, "REFUSED NOLINE id=read-7 ") || !strings.Contains(stdout, "--line 'SCORE who=emma head="+head) {
		t.Fatalf("done without a line: exit %d stdout %q stderr %q", code, stdout, stderr)
	}
	line := "SCORE who=emma head=" + head + " score=9/10 gates=ci:ok,base:ok,scope:ok: fine"
	if code, _, stderr := runSprint(append(base, "--line", line, "--score", "9")...); code != 2 || !strings.Contains(stderr, "--line carries who, head and score") {
		t.Fatalf("--line with --score: exit %d stderr %q; want usage", code, stderr)
	}
	code, stdout, stderr = runSprint(append(base, "--line", line, "--mirror", t.TempDir())...)
	want := "DONE DONE id=read-7 LINE pr:nova-tools:7:line:" + head + ":emma:SCORE lines=1 scope=-"
	if code != 0 || strings.TrimSpace(stdout) != want {
		t.Fatalf("done with the line: exit %d stdout %q stderr %q; want %q", code, stdout, stderr, want)
	}
	if st := c.HGet(ctx, task.Key(S, "read-7"), "state").Val(); st != "closed" {
		t.Fatalf("card is %s after the read, want closed", st)
	}
}
