//go:build functional

package stream

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

// TestLandPRMergesAMemberOnlyAsTheHead is the work order at `land pr <n>`
// (MemberHeadGate, immediately before the merge): PR 7 is stream member t2
// while t1 is ahead of it in the live order (working), so the pass refuses
// ORDER WAIT before=t1 and merges nothing; once t1 has landed t2 is the
// head and it merges. The stream PR itself (kind=stream) merges as before
// with t1 still ahead.
func TestLandPRMergesAMemberOnlyAsTheHead(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	seedPair := func(c *redis.Client, kind string) {
		c.ZAdd(ctx, WSKeyAt(0, strm, "working"), redis.Z{Score: 100, Member: "t1"})
		c.HSet(ctx, "task:t1", "stream", strm, "where", "working", "state", "working", "created_at", "100", "ref", "nova-tools#1")
		c.ZAdd(ctx, WSKeyAt(0, strm, "merging"), redis.Z{Score: 200, Member: "t2"})
		c.HSet(ctx, "task:t2", "stream", strm, "where", "merging", "state", "merging", "created_at", "200", "ref", "nova-tools#2")
		c.SAdd(ctx, "ws:names", strm)
		c.HSet(ctx, PRKey("o/r", 7), "head", prHead, "stream", strm, "task", "t2", "kind", kind)
	}
	f, c := newFakeForge(), newRedis(t)
	ghLeg(t, c, "gh", "green", "wf:ci", "green 1 2026-09-26T12:00:00Z")
	seedPair(c, "")
	_, log, _, err := runLandPR(t, f, c)
	var r *Refusal
	want := `ORDER WAIT stream="landing: streams + lander" before=t1 where=working held=t2`
	if !errors.As(err, &r) || r.Why != want || len(f.merges) != 0 {
		t.Fatalf("member behind t1: %v, merges %v, log %q", err, f.merges, log)
	}
	t.Logf("member t2 behind t1: %v", err)
	c.ZRem(ctx, WSKeyAt(0, strm, "working"), "t1")
	c.ZAdd(ctx, WSKeyAt(0, strm, "landed"), redis.Z{Score: 100, Member: "t1"})
	c.HSet(ctx, "task:t1", "where", "landed")
	rep, log, _, err := runLandPR(t, f, c)
	if err != nil || rep.State != "merged" || len(f.merges) != 1 || !strings.Contains(log, "PR 7 CLAIMED t2 until ") ||
		!strings.Contains(log, "PR 7 MERGED "+f.mergeSHA+"\n") || rep.CardMove != "merging->landed" {
		t.Fatalf("t2 as the head: %+v %v %q", rep, err, log)
	}
	if n, _ := c.HLen(ctx, WSKeyAt(0, strm, "landing")).Result(); n != 0 {
		t.Fatalf("the landing left a claim: %v", c.HGetAll(ctx, WSKeyAt(0, strm, "landing")).Val())
	}
	t.Logf("t2 as the head: claimed, merged, landed: %s", strings.ReplaceAll(strings.TrimSpace(log), "\n", " | "))

	f2, c2 := newFakeForge(), newRedis(t)
	ghLeg(t, c2, "gh", "green", "wf:ci", "green 1 2026-09-26T12:00:00Z")
	seedPair(c2, "stream")
	if rep, _, _, err := runLandPR(t, f2, c2); err != nil || rep.State != "merged" {
		t.Fatalf("the stream PR (kind=stream): %+v %v", rep, err)
	}
	t.Logf("t2 merges once t1 landed; the stream PR merges with t1 ahead")
}
