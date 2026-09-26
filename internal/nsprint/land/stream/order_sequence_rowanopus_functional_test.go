//go:build functional

package stream

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

// The reader's tests (Rowan-opus cold read of #4410 at cce8edc8e), copied
// under the reader's names; this round adds t.Parallel (the class test) and
// the selection test's assertion (its sequence is the computed order).
//
// Rowan-opus cold read of #4410 at cce8edc8e: the temporal sequence with the
// real ns_ws_move, then an older t0 appearing between selection and merge
// with NO DEPENDS-ON edit (ordered by paths + lower issue only).
func TestOrderSequenceRealMoves_RowanOpus(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	head := strings.Repeat("b", 40)
	c := newRedis(t)
	seed(t, c, 2, head, 200, score("rowan", head, 10))
	c.HSet(ctx, "task:t2", "where", "merging", "paths", "internal/shared.go", "ref", "nova-tools#2")
	c.ZAdd(ctx, WSKey(strm, "working"), redis.Z{Score: 100, Member: "t1"})
	c.HSet(ctx, "task:t1", "stream", strm, "where", "working", "state", "working", "created_at", "100", "paths", "internal/shared.go", "ref", "nova-tools#1")
	dry := func() Report {
		rep, err := LandStream(ctx, c, Options{Repo: repo, Streams: []string{strm}, DryRun: true, MinScore: 8})
		if err != nil {
			t.Fatal(err)
		}
		return rep
	}
	rep := dry()
	if len(rep.Members) != 0 || len(rep.Order) != 1 || !strings.Contains(rep.Order[0].Line(), "before=t1 where=working held=t2") {
		t.Fatalf("step1 members %v order %v", rep.Members, rep.Order)
	}
	t.Logf("step1 t1 working: %s", rep.Order[0].Line())
	lr, err := taskcard.Land(ctx, c, "t1", "probe", strings.Repeat("c", 40), "")
	t.Logf("step2 taskcard.Land t1 (working -> landed): %+v %v", lr, err)
	if err != nil {
		t.Fatal(err)
	}
	rep = dry()
	if len(rep.Members) != 1 || rep.Members[0].Task != "t2" || len(rep.Order) != 0 {
		t.Fatalf("step2 members %v skips %v order %v", rep.Members, rep.Skips, rep.Order)
	}
	t.Logf("step2 t1 landed: t2 selected")
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
	// t0: older, lower issue, shares the path; no DEPENDS-ON edit anywhere.
	c.ZAdd(ctx, WSKey(strm, "ready"), redis.Z{Score: 50, Member: "t0"})
	c.HSet(ctx, "task:t0", "stream", strm, "where", "ready", "state", "ready", "created_at", "50", "paths", "internal/shared.go", "ref", "nova-tools#1")
	_, err = Merge(ctx, c, MergeOptions{Repo: repo, Streams: []string{strm}, By: "test"})
	var r *Refusal
	if !errors.As(err, &r) || !strings.HasPrefix(r.Why, "ORDER WAIT") || !strings.Contains(r.Why, "before=t0") {
		t.Fatalf("step3 merge after older t0 appeared: %v", err)
	}
	t.Logf("step3 merge re-check: %v", err)
}

// never-finishes as the brief phrases it: t1 itself retired fail (no live
// place), t2 merging; (a) no edge, (b) t2 DEPENDS-ON t1; and t1 done/ok.
func TestOrderRetiredPredecessor_RowanOpus(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	head := strings.Repeat("b", 40)
	for _, tc := range []struct{ name, ok, dep string }{{"fail-noedge", "fail", ""}, {"fail-edge", "fail", "t1"}, {"ok-edge", "ok", "t1"}, {"ok-noedge", "ok", ""}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := newRedis(t)
			seed(t, c, 2, head, 200, score("rowan", head, 10))
			c.HSet(ctx, "task:t2", "where", "merging", "paths", "internal/shared.go", "ref", "nova-tools#2")
			if tc.dep != "" {
				c.HSet(ctx, "task:t2", "blocked_on", tc.dep)
			}
			c.HSet(ctx, "task:t1", "stream", strm, "where", "done", "where_ok", tc.ok, "state", "done", "created_at", "100", "paths", "internal/shared.go", "ref", "nova-tools#1")
			rep, err := LandStream(ctx, c, Options{Repo: repo, Streams: []string{strm}, DryRun: true, MinScore: 8})
			var lines []string
			for _, h := range rep.Order {
				lines = append(lines, h.Line())
			}
			t.Logf("%s: err=%v members=%v skips=%v order=%v", tc.name, err, rep.Members, rep.Skips, lines)
		})
	}
}

// Selection-time twin of Stella's e343a0a85f35: stored merging scores that
// disagree with the computed order (ORDER DRIFT) give Members in score order;
// the set-based gate passes it, so the build merges in the drifted sequence.
func TestSelectionSequenceFollowsScoresNotOrder_RowanOpus(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	head := strings.Repeat("b", 40)
	c := newRedis(t)
	seed(t, c, 1, head, 100, score("rowan", head, 10))
	seed(t, c, 2, head, 300, score("rowan", head, 10))
	c.HSet(ctx, "task:t1", "where", "merging", "paths", "internal/one.go", "ref", "nova-tools#1")
	c.HSet(ctx, "task:t2", "where", "merging", "paths", "internal/two.go", "ref", "nova-tools#2")
	// the drift: t1 (older, #1) is first in the computed order (created_at
	// is the position's one key, round 6) but its stored score puts it last
	c.ZAdd(ctx, WSKey(strm, "merging"), redis.Z{Score: 500, Member: "t1"})
	orders, _ := ws.ReadOrders(ctx, c, []string{strm})
	rep, err := LandStream(ctx, c, Options{Repo: repo, Streams: []string{strm}, DryRun: true, MinScore: 8})
	var ids, ord []string
	for _, m := range rep.Members {
		ids = append(ids, m.Task)
	}
	for _, o := range orders[0].Order {
		ord = append(ord, o.ID)
	}
	t.Logf("computed order %v; selected sequence %v; order lines %d; err %v", ord, ids, len(rep.Order), err)
	if err != nil || strings.Join(ids, ",") != "t1,t2" || strings.Join(ord[:2], ",") != "t1,t2" {
		t.Fatalf("the selected sequence %v is not the computed order %v", ids, ord)
	}
}
