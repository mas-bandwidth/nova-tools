package main

import (
	"context"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
)

// TestReadPostScoreIsAStreamMemberAtOnce is nova-tools#4049's DONE-WHEN: a
// SCORE posted with read post is the member's read to `stream open
// --dry-run` at once, with no copy of pr:<name>:<n>:lines into the record's
// reads field; a HOLD posted the same way holds the member. read post and
// the lander meet in one place, the reads field ns_read_post appends to.
func TestReadPostScoreIsAStreamMemberAtOnce(t *testing.T) {
	t.Setenv(store.UserEnv, "")
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = c.Close() })
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	head := strings.Repeat("4049abcd", 5)
	if code, out, errOut := runSprint("pr", "record", "--redis", addr, "--repo", lsRepo, "--n", "4049",
		"--head", head, "--base", "dev", "--stream", lsStream, "--task", "t4049"); code != 0 {
		t.Fatalf("pr record: %d %q %q", code, out, errOut)
	}
	if err := c.ZAdd(ctx, "ws:"+lsStream+":merging", redis.Z{Score: 1, Member: "t4049"}).Err(); err != nil {
		t.Fatal(err)
	}
	if err := c.HSet(ctx, "task:t4049", "stream", lsStream, "state", "merging", "pr", "4049", "created_at", "1").Err(); err != nil {
		t.Fatal(err)
	}
	open := func() string {
		t.Helper()
		code, out, errOut := runSprint("stream", "open", "--redis", addr, "--repo", lsRepo, "--stream", lsStream, "--dry-run")
		if code != 0 {
			t.Fatalf("stream open --dry-run: %d %q %q", code, out, errOut)
		}
		return out
	}
	post := func(line string) {
		t.Helper()
		if code, out, errOut := runSprint("read", "post", "--repo", "nova-tools", "--n", "4049", "--line", line,
			"--no-github", "--redis", addr); code != 0 {
			t.Fatalf("read post %q: %d %q %q", line, code, out, errOut)
		}
	}

	if out := open(); !strings.Contains(out, "SKIP task=t4049 pr=#4049 why=no-read-at-head") {
		t.Fatalf("before any read the member is not skipped as unread:\n%s", out)
	}
	post("SCORE who=stella head=" + head + " score=9/10 gates=ci:ok,base:ok,scope:ok\n1. one file, PATHS = CHANGED")
	want := "ORDER 1 " + lsRepo + "#4049 head=" + head[:8] + " "
	out := open()
	if !strings.Contains(out, want) || !strings.Contains(out, " read=stella:9 task=t4049 ") || strings.Contains(out, "SKIP task=t4049") {
		t.Fatalf("the SCORE read post wrote is not the member's read (no copy step was made):\n%s", out)
	}
	post("HOLD who=emma head=" + head)
	if out := open(); !strings.Contains(out, "SKIP task=t4049 pr=#4049 why=hold:emma") {
		t.Fatalf("the HOLD read post wrote does not hold the member:\n%s", out)
	}
}
