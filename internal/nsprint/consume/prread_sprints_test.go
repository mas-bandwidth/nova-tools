package consume

import (
	"strings"
	"testing"
)

// TestPRReadJoinsSprintOpenedLater is the quack-0925d defect (2026-09-25):
// the sprint was opened after pr-to-read started, so s:quack-0925d:log had
// no group pr-to-read and none of its six PRs' first `pr head` events
// queued a read. A sprint opened (SADD sprints) after the consumer's first
// pass, with a first-head event already on its log, gets the group on the
// next pass (one PRREAD JOINED sprint=<S> from=0 line), and that pending
// event queues the first read; a third pass joins and queues nothing more.
// A sprint whose lease:route:<S> another router holds is skipped, no error.
func TestPRReadJoinsSprintOpenedLater(t *testing.T) {
	t.Parallel()

	a := newFirstReadFixture(t, "join-a")
	a.up("rowan", "1", 0, 0)
	a.up("stella", "1", 0, 0)
	all := &PRReadSprints{Store: a.pr.Store, Instance: "t-reconciler", Actor: "reconciler", Out: a.out}

	if _, err := all.Pass(a.ctx); err != nil {
		t.Fatalf("pass 1: %v", err)
	}
	if !hasGroup(t, a, "join-a", RulePRToRead) {
		t.Fatal("pass 1: s:join-a:log has no group pr-to-read")
	}
	if !strings.Contains(a.out.String(), "PRREAD JOINED sprint=join-a from=0\n") {
		t.Fatalf("pass 1 output %q, want PRREAD JOINED sprint=join-a from=0", a.out.String())
	}

	// join-b is opened after the first pass; its first head is written
	// before anyone has joined its log.
	pipe := a.client.TxPipeline()
	pipe.SAdd(a.ctx, "sprints", "join-b")
	pipe.HSet(a.ctx, "s:join-b", "status", "open")
	pipe.HSet(a.ctx, "s:join-b:policy", "readers", "1")
	if _, err := pipe.Exec(a.ctx); err != nil {
		t.Fatal(err)
	}
	b := &firstReadFixture{t: t, ctx: a.ctx, client: a.client, S: "join-b"}
	head := "5555555555555555555555555555555555555555"
	b.cardPR("c-3750", "mas-bandwidth/nova-tools", 3750, head, "internal/y.go")
	if hasGroup(t, a, "join-b", RulePRToRead) {
		t.Fatal("s:join-b:log has pr-to-read before any pass")
	}

	a.out.Reset()
	if _, err := all.Pass(a.ctx); err != nil {
		t.Fatalf("pass 2: %v", err)
	}
	if !hasGroup(t, a, "join-b", RulePRToRead) {
		t.Fatal("pass 2: s:join-b:log has no group pr-to-read")
	}
	out := a.out.String()
	if strings.Count(out, "PRREAD JOINED") != 1 || !strings.Contains(out, "PRREAD JOINED sprint=join-b from=0\n") {
		t.Fatalf("pass 2 output %q, want one PRREAD JOINED sprint=join-b from=0", out)
	}
	if got := a.queue("stella"); len(got) != 1 || got[0] != "read-3750-55555555" {
		t.Fatalf("q:stella = %v, want [read-3750-55555555]", got)
	}
	if !strings.Contains(out, "READ QUEUED n=3750 head=55555555 to=stella\n") {
		t.Fatalf("pass 2 output %q, want READ QUEUED n=3750", out)
	}

	a.out.Reset()
	if _, err := all.Pass(a.ctx); err != nil {
		t.Fatalf("pass 3: %v", err)
	}
	if a.out.Len() != 0 || len(a.queue("stella")) != 1 {
		t.Fatalf("pass 3 printed %q, q:stella %v; want nothing new", a.out.String(), a.queue("stella"))
	}

	// join-c is served by its own router: held, skipped, not an error.
	pipe = a.client.TxPipeline()
	pipe.SAdd(a.ctx, "sprints", "join-c")
	pipe.HSet(a.ctx, "s:join-c", "status", "open")
	if _, err := pipe.Exec(a.ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.client.FCall(a.ctx, FunctionRouteLeaseTake, nil, "join-c", "other-router", "tok", "h", "60000").Err(); err != nil {
		t.Fatal(err)
	}
	a.out.Reset()
	if _, err := all.Pass(a.ctx); err != nil {
		t.Fatalf("pass 4 with join-c held by another router: %v", err)
	}
	if hasGroup(t, a, "join-c", RulePRToRead) {
		t.Fatal("pass 4 joined join-c, which another router holds")
	}
}

func hasGroup(t *testing.T, f *firstReadFixture, sprint, group string) bool {
	t.Helper()
	groups, err := f.client.XInfoGroups(f.ctx, "s:"+sprint+":log").Result()
	if err != nil {
		if strings.Contains(err.Error(), "no such key") {
			return false
		}
		t.Fatal(err)
	}
	for _, g := range groups {
		if g.Name == group {
			return true
		}
	}
	return false
}
