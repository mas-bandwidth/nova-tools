//go:build functional

package stream

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/taskcard"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
	"github.com/redis/go-redis/v9"
)

// Rowan-opus cold read of #4410 at 69b5f712e (round 5). Each test asserts
// the invariant (no later member lands past an unfinished predecessor), so
// the ones about the ungated doors are red at 69b5f712e by design.

// P1: Stella's sequence and back. Select t1,t2, save; revise t1 onto t2;
// land merge refuses sequence-changed and merges nothing; undo the revision;
// land merge proceeds to the GitHub step.
func TestReadSequenceThenUndo_RowanOpus(t *testing.T) {
	t.Parallel()
	c := newRedis(t)
	ctx := context.Background()
	head := strings.Repeat("b", 40)
	for _, n := range []int{1, 2} {
		seed(t, c, n, head, int64(n*100), score("rowan", head, 10))
		c.HSet(ctx, fmt.Sprintf("task:t%d", n), "where", "merging", "state", "merging", "paths", fmt.Sprintf("internal/p%d.go", n), "ref", fmt.Sprintf("nova-tools#%d", n))
	}
	if _, err := ws.Reorder(ctx, c, strm, "test"); err != nil {
		t.Fatal(err)
	}
	rep, err := LandStream(ctx, c, Options{Repo: repo, Streams: []string{strm}, DryRun: true, MinScore: 8})
	if err != nil || len(rep.Members) != 2 || rep.Members[0].Task != "t1" {
		t.Fatalf("select %+v %v", rep.Members, err)
	}
	slug, _ := Slug(strm)
	if _, err := SaveBuilt(ctx, c, Landing{Repo: repo, Slug: slug, Streams: strm, Base: "dev", Branch: "stream/x", Head: head, Members: rep.Members, PR: 900, State: "open"}, "test"); err != nil {
		t.Fatal(err)
	}
	c.HSet(ctx, LandKey(repo, slug), "state", "open", "pr", 900, "head", head)
	if _, err := Record(ctx, c, repo, 900, RecordFields{Head: head, Base: "dev", CI: "green", Mergeable: "true"}); err != nil {
		t.Fatal(err)
	}
	t.Logf("step1 selected+saved tasks=%q", c.HGet(ctx, LandKey(repo, slug), "tasks").Val())
	c.HSet(ctx, "task:t1", "blocked_on", "t2")
	if _, err := ws.Reorder(ctx, c, strm, "test"); err != nil {
		t.Fatal(err)
	}
	snap := fmt.Sprint(c.HGetAll(ctx, LandKey(repo, slug)).Val(), c.HGetAll(ctx, "task:t1").Val(), c.HGetAll(ctx, "task:t2").Val())
	_, err = Merge(ctx, c, MergeOptions{Repo: repo, Streams: []string{strm}, By: "test"})
	var r *Refusal
	want := `ORDER CONFLICT stream="landing: streams + lander" why=sequence-changed was=t1,t2 now=t2,t1 escalate=coordinator`
	if !errors.As(err, &r) || r.Why != want {
		t.Fatalf("step2 %v", err)
	}
	if after := fmt.Sprint(c.HGetAll(ctx, LandKey(repo, slug)).Val(), c.HGetAll(ctx, "task:t1").Val(), c.HGetAll(ctx, "task:t2").Val()); after != snap {
		t.Fatalf("step2 refusal wrote: %s -> %s", snap, after)
	}
	t.Logf("step2 revised t1->t2: REFUSED %s; landing and tasks unchanged", r.Why)
	c.HDel(ctx, "task:t1", "blocked_on")
	if _, err := ws.Reorder(ctx, c, strm, "test"); err != nil {
		t.Fatal(err)
	}
	_, err = Merge(ctx, c, MergeOptions{Repo: repo, Streams: []string{strm}, By: "test"})
	if !errors.As(err, &r) || r.Why != "no GitHub client" {
		t.Fatalf("step3 after undo: %v", err)
	}
	t.Logf("step3 undo: land merge reaches %q", r.Why)
}

// twoLive seeds t1 (working, #1) ahead of t2 (merging, #2) on a shared path.
func twoLive(t *testing.T, c *redis.Client, t1where string) {
	ctx := context.Background()
	head := strings.Repeat("b", 40)
	seed(t, c, 2, head, 200, score("rowan", head, 10))
	c.HSet(ctx, "task:t2", "where", "merging", "paths", "internal/shared.go", "ref", "nova-tools#2")
	if t1where != "" {
		c.ZAdd(ctx, WSKey(strm, t1where), redis.Z{Score: 100, Member: "t1"})
		c.HSet(ctx, "task:t1", "stream", strm, "where", t1where, "state", t1where, "created_at", "100", "paths", "internal/shared.go", "ref", "nova-tools#1")
	}
}

// P2: the check-then-write window at card/task land --stream. The gate
// passes (t2 is the head); t0, older with a lower issue on the same path,
// arrives in ready before the write (an interleaved push); the FCALL lands
// t2 past it. Red at 69b5f712e: the check is not inside ns_tcard_land_stream.
func TestReadStreamLandGateWindow_RowanOpus(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := newRedis(t)
	twoLive(t, c, "")
	if h, err := StreamLandGate(ctx, c, strm); err != nil || h != nil {
		t.Fatalf("gate at t2 alone: %v %v", h, err)
	}
	c.ZAdd(ctx, WSKey(strm, "ready"), redis.Z{Score: 50, Member: "t0"})
	c.HSet(ctx, "task:t0", "stream", strm, "where", "ready", "state", "ready", "created_at", "50", "paths", "internal/shared.go", "ref", "nova-tools#1")
	if h, _ := StreamLandGate(ctx, c, strm); h != nil {
		t.Logf("the gate now would say: %s", h.Line())
	}
	l, err := taskcard.LandStream(ctx, c, strm, strings.Repeat("c", 40), "probe", "")
	if err == nil && len(l.Landed) > 0 {
		t.Fatalf("WINDOW: gate passed, t0 moved ahead before the write, ns_tcard_land_stream landed %v past t0 (ready)", l.Landed)
	}
}

// P3a: task land --id (taskcard.Land) lands t2 past t1 working. Red at
// 69b5f712e (listed ungated in DOORS).
func TestReadTaskLandIDPassesPredecessor_RowanOpus(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := newRedis(t)
	twoLive(t, c, "working")
	if h, _ := StreamLandGate(ctx, c, strm); h != nil {
		t.Logf("the stream gate holds it: %s", h.Line())
	}
	r, err := taskcard.Land(ctx, c, "t2", "probe", strings.Repeat("c", 40), "")
	if err == nil {
		t.Fatalf("UNGATED: task land --id t2 -> %s while t1 working ahead", r.To)
	}
}

// P3b: ns_card_land (raw FCALL; the coordinator ACL grants it) lands card
// c2 past card c1 ready ahead of it on the same stream. Red at 69b5f712e.
func TestReadCardLandPassesPredecessor_RowanOpus(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := newRedis(t)
	const s, sp = "cards: order", "rd"
	body := func(label, issue string) []byte {
		return []byte("LABEL: " + label + "\nREPO: mas-bandwidth/nova-tools\nBASE: dev\nbase-sha: " + strings.Repeat("ab", 20) +
			"\nPATHS: internal/shared.go\nDEPENDS-ON: none\nDONE-WHEN: go test passes\nSTREAM: " + s +
			"\nORIGIN: mas-bandwidth/nova-tools#" + issue + "\nINVARIANT: the card holds one thing.\nCLASS-TEST: TestTheCard\n\nwhat and why\n")
	}
	for _, b := range [][]byte{body("c1", "1"), body("c2", "2")} {
		if v := card.Push(ctx, c, sp, b); v.Code != 0 {
			t.Fatalf("push %+v", v)
		}
	}
	if _, err := ws.Reorder(ctx, c, s, "test"); err != nil {
		t.Fatal(err)
	}
	orders, _ := ws.ReadOrders(ctx, c, []string{s})
	var ids []string
	for _, o := range orders[0].Order {
		ids = append(ids, o.ID+"@"+orders[0].Where[o.ID])
	}
	t.Logf("order %v", ids)
	k := func(x string) string { return "s:" + sp + ":" + x }
	reply, err := c.FCall(ctx, "ns_card_land", []string{k("card:c2"), k("pool"), k("waiting"), k("log"), k("idx:card:queued"), k("idx:card:landed")},
		"c2", strings.Repeat("d", 40)).Text()
	w := c.HMGet(ctx, k("card:c2"), "where", "state").Val()
	if err == nil && reply == "OK" {
		t.Fatalf("UNGATED: ns_card_land c2 -> %s %v while c1 ahead (%v)", reply, w, ids)
	}
	t.Logf("ns_card_land c2: %q %v %v", reply, err, w)
}

// P4: Position is a merge, not a pairwise order: a5 (#5, created 100) and u
// (no issue, created 200) are built a5,u; b3 (#3, created 400) is pushed
// later; the order becomes u,b3,a5, and land merge reads it as the selected
// members' sequence changing (CONFLICT, escalate) where b3 is only a new
// predecessor (WAIT). Logs the line.
func TestReadThirdCardFlipsTwo_RowanOpus(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	c := newRedis(t)
	head := strings.Repeat("b", 40)
	seed(t, c, 5, head, 100, score("rowan", head, 10))
	seed(t, c, 6, head, 200, score("rowan", head, 10))
	c.HSet(ctx, "task:t5", "where", "merging", "ref", "nova-tools#5", "paths", "internal/a.go")
	c.HSet(ctx, "task:t6", "where", "merging", "ref", "", "paths", "internal/u.go")
	ws.Reorder(ctx, c, strm, "test")
	rep, err := LandStream(ctx, c, Options{Repo: repo, Streams: []string{strm}, DryRun: true, MinScore: 8})
	if err != nil || len(rep.Members) != 2 {
		t.Fatalf("select %+v %v %v", rep.Members, rep.Order, err)
	}
	slug, _ := Slug(strm)
	SaveBuilt(ctx, c, Landing{Repo: repo, Slug: slug, Streams: strm, Base: "dev", Branch: "stream/x", Head: head, Members: rep.Members, PR: 900, State: "open"}, "test")
	c.HSet(ctx, LandKey(repo, slug), "state", "open", "pr", 900, "head", head)
	Record(ctx, c, repo, 900, RecordFields{Head: head, Base: "dev", CI: "green", Mergeable: "true"})
	t.Logf("built tasks=%q", c.HGet(ctx, LandKey(repo, slug), "tasks").Val())
	c.ZAdd(ctx, WSKey(strm, "ready"), redis.Z{Score: 400, Member: "t3"})
	c.HSet(ctx, "task:t3", "stream", strm, "where", "ready", "state", "ready", "created_at", "400", "ref", "nova-tools#3", "paths", "internal/b.go")
	_, err = Merge(ctx, c, MergeOptions{Repo: repo, Streams: []string{strm}, By: "test"})
	t.Logf("after t3 (#3, youngest) pushed: %v", err)
}
