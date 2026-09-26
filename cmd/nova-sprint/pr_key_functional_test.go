//go:build functional

package main

import (
	"context"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fn"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/testutil"
	"github.com/redis/go-redis/v9"
)

func TestPRRecordThenReadPostHitOneKeyOnBothSpellings(t *testing.T) {
	t.Setenv(store.UserEnv, "")
	addr := testutil.Start(t)
	c := redis.NewClient(&redis.Options{Addr: addr})
	defer c.Close()
	ctx := context.Background()
	if err := fn.Load(ctx, c); err != nil {
		t.Fatal(err)
	}
	head := "4eeab789c3fe4eeab789c3fe4eeab789c3fe4eea"
	for i, spell := range [][2]string{
		{"mas-bandwidth/rowan-tools", "rowan-tools"}, // tonight's pair
		{"rowan-tools", "mas-bandwidth/rowan-tools"},
		{"rowan-tools", "rowan-tools"},
		{"mas-bandwidth/rowan-tools", "mas-bandwidth/rowan-tools"},
	} {
		n := strconv.Itoa(375 + i)
		key := "pr:rowan-tools:" + n
		code, out, errOut := runSprint("pr", "record", "--redis", addr, "--repo", spell[0], "--n", n,
			"--head", head, "--base", "main", "--stream", "redis: store")
		if code != 0 || !strings.HasPrefix(out, "PR RECORD "+key+" ") || !strings.Contains(out, "created=true") {
			t.Fatalf("%v: pr record exit %d out %q err %q", spell, code, out, errOut)
		}
		typed := "HOLD who=rowan head=" + head + " score=6/10 gates=ci:pending,base:ok,scope:ok"
		code, out, errOut = runSprint("read", "post", "--repo", spell[1], "--n", n, "--line", typed, "--no-github", "--redis", addr)
		if code != 0 || !strings.Contains(out, "READ POST repo=rowan-tools n="+n+" kind=HOLD lines=1") {
			t.Fatalf("%v: read post exit %d out %q err %q", spell, code, out, errOut)
		}
		code, out, errOut = runSprint("pr", "lines", "--redis", addr, "--repo", spell[1], "--n", n,
			"--add", "SCORE who=emma head="+head+" score=9/10")
		if code != 0 || !strings.HasPrefix(out, "PR LINES "+key+" lines=2 ") {
			t.Fatalf("%v: pr lines exit %d out %q err %q", spell, code, out, errOut)
		}
		if got := c.HGet(ctx, key, "head").Val(); got != head {
			t.Fatalf("%v: %s head %q", spell, key, got)
		}
		if got := c.LLen(ctx, key+":lines").Val(); got != 1 {
			t.Fatalf("%v: %s:lines has %d", spell, key, got)
		}
		if got := c.HGet(ctx, key, "reads").Val(); got != typed+"\nSCORE who=emma head="+head+" score=9/10" {
			t.Fatalf("%v: %s reads %q", spell, key, got)
		}
	}
	if keys := c.Keys(ctx, "pr:mas-bandwidth/*").Val(); len(keys) != 0 {
		t.Fatalf("owner-form keys written: %v", keys)
	}
}
