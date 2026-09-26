//go:build functional

package stream

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/redis/go-redis/v9"
)

// TestOrderGateWaitConflictAndMergeRevalidates is the work order gate's
// contract (nova-tools #4322, Stella's read of #4410): t1 before t2 in the
// computed live order (shared paths, lower issue), t2 in merging with a
// passing read.
//   - t1 working: t2 is held, `ORDER WAIT ... before=t1 where=working held=t2`.
//   - t1 waiting on a record that ended done/fail: `ORDER CONFLICT
//     why=never-finishes ... escalate=coordinator`, a distinct line; on one
//     that ended done/ok t1 is satisfied and merely waiting: ORDER WAIT.
//   - t1 and t2 both merging and read, on each other: `ORDER CONFLICT
//     why=cycle`, both held, the merging scores unchanged (never reordered).
//   - t1 and t2 selected and built into an open landing; t0 then lands
//     ahead of them (t1 DEPENDS-ON t0): land merge refuses ORDER WAIT
//     before=t0 before any GitHub call, the landing still open.
func TestOrderGateWaitConflictAndMergeRevalidates(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	head := strings.Repeat("b", 40)
	setup := func(t *testing.T, t1Where string) *redis.Client {
		c := newRedis(t)
		seed(t, c, 2, head, 200, score("rowan", head, 10))
		c.HSet(ctx, "task:t2", "where", "merging", "paths", "internal/shared.go", "ref", "nova-tools#2")
		if t1Where == "merging" {
			seed(t, c, 1, head, 100, score("rowan", head, 10))
		} else {
			c.ZAdd(ctx, WSKey(strm, t1Where), redis.Z{Score: 100, Member: "t1"})
		}
		c.HSet(ctx, "task:t1", "stream", strm, "where", t1Where, "state", t1Where, "created_at", "100",
			"paths", "internal/shared.go", "ref", "nova-tools#1")
		return c
	}
	dry := func(t *testing.T, c *redis.Client) Report {
		rep, err := LandStream(ctx, c, Options{Repo: repo, Streams: []string{strm}, DryRun: true, MinScore: 8})
		if err != nil {
			t.Fatal(err)
		}
		return rep
	}
	const q = `"landing: streams + lander"`

	t.Run("wait", func(t *testing.T) {
		t.Parallel()
		rep := dry(t, setup(t, "working"))
		want := "ORDER WAIT stream=" + q + " before=t1 where=working held=t2"
		if len(rep.Members) != 0 || len(rep.Order) != 1 || rep.Order[0].Line() != want ||
			fmt.Sprint(rep.Skips) != "[{t2 2 order-wait:t1}]" {
			t.Fatalf("members %v skips %v order %v", rep.Members, rep.Skips, rep.Order)
		}
		t.Logf("%s | SKIP %v", rep.Order[0].Line(), rep.Skips)
	})
	t.Run("never-finishes", func(t *testing.T) {
		t.Parallel()
		c := setup(t, "waiting")
		c.HSet(ctx, "task:t1", "blocked_on", "x")
		c.HSet(ctx, "task:x", "where", "done", "where_ok", "fail", "stream", strm)
		rep := dry(t, c)
		want := "ORDER CONFLICT stream=" + q + " why=never-finishes before=t1 where=waiting dep=x dep_where=done/fail held=t2 escalate=coordinator"
		if len(rep.Members) != 0 || len(rep.Order) != 1 || rep.Order[0].Line() != want {
			t.Fatalf("members %v order %v", rep.Members, rep.Order)
		}
		t.Logf("%s", rep.Order[0].Line())
	})
	t.Run("done-dep-waits", func(t *testing.T) {
		t.Parallel()
		c := setup(t, "waiting")
		c.HSet(ctx, "task:t1", "blocked_on", "x")
		c.HSet(ctx, "task:x", "where", "done", "where_ok", "ok", "stream", strm)
		rep := dry(t, c)
		want := "ORDER WAIT stream=" + q + " before=t1 where=waiting held=t2"
		if len(rep.Members) != 0 || len(rep.Order) != 1 || rep.Order[0].Line() != want {
			t.Fatalf("members %v order %v", rep.Members, rep.Order)
		}
		t.Logf("dependency done/ok: %s", rep.Order[0].Line())
	})
	t.Run("cycle", func(t *testing.T) {
		t.Parallel()
		c := setup(t, "merging")
		c.HSet(ctx, "task:t1", "blocked_on", "t2")
		c.HSet(ctx, "task:t2", "blocked_on", "t1")
		before := fmt.Sprint(c.ZRangeWithScores(ctx, WSKey(strm, "merging"), 0, -1).Val())
		rep := dry(t, c)
		if len(rep.Members) != 0 || len(rep.Order) != 1 || rep.Order[0].Kind != OrderConflict || rep.Order[0].Why != "cycle" ||
			!strings.HasSuffix(rep.Order[0].Line(), " held=t1,t2 escalate=coordinator") {
			t.Fatalf("members %v order %v", rep.Members, rep.Order)
		}
		if after := fmt.Sprint(c.ZRangeWithScores(ctx, WSKey(strm, "merging"), 0, -1).Val()); after != before {
			t.Fatalf("the gate reordered: %s -> %s", before, after)
		}
		t.Logf("%s; merging scores unchanged %s", rep.Order[0].Line(), before)
	})
	t.Run("merge-revalidates", func(t *testing.T) {
		t.Parallel()
		c := setup(t, "merging")
		rep := dry(t, c)
		if len(rep.Members) != 2 || len(rep.Order) != 0 || rep.Members[0].Task != "t1" || rep.Members[1].Task != "t2" {
			t.Fatalf("selection: members %v order %v", rep.Members, rep.Order)
		}
		l := Landing{Repo: repo, Slug: "landing-streams-lander", Streams: strm, Base: "dev", Branch: "stream/x", Head: head,
			Members: rep.Members, PR: 900, State: "open"}
		if _, err := SaveBuilt(ctx, c, l, "test"); err != nil {
			t.Fatal(err)
		}
		slug, _ := Slug(strm)
		c.HSet(ctx, LandKey(repo, slug), "state", "open", "pr", "900", "head", head)
		if _, err := Record(ctx, c, repo, 900, RecordFields{Head: head, Base: "dev", CI: "green", Mergeable: "true"}); err != nil {
			t.Fatal(err)
		}
		// after the selection: t0 is pushed ready and t1 now depends on it
		c.ZAdd(ctx, WSKey(strm, "ready"), redis.Z{Score: 50, Member: "t0"})
		c.HSet(ctx, "task:t0", "stream", strm, "where", "ready", "state", "ready", "created_at", "50", "ref", "nova-tools#3")
		c.HSet(ctx, "task:t1", "blocked_on", "t0")
		_, err := Merge(ctx, c, MergeOptions{Repo: repo, Streams: []string{strm}, By: "test"})
		var r *Refusal
		want := "ORDER WAIT stream=" + q + " before=t0 where=ready held=t1,t2"
		if !errors.As(err, &r) || r.Why != want {
			t.Fatalf("land merge after t0 moved ahead: %v", err)
		}
		if st := c.HGet(ctx, LandKey(repo, slug), "state").Val(); st != "open" {
			t.Fatalf("landing state %q after the refusal", st)
		}
		t.Logf("land merge: %v", err)
	})
}
