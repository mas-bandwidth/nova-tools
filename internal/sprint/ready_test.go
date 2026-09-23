package sprint

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/deal"
)

// TestBlockedOnOpenDependency: B depends on A, and A is still open -- B is not ready.
func TestBlockedOnOpenDependency(t *testing.T) {
	a := Task{ID: "a", State: StateOpen}
	b := Task{ID: "b", State: StateOpen, DependsOn: []string{"a"}}
	if reason := Blocked(b, []Task{a, b}); reason == "" {
		t.Fatalf("b depends on open a; wants a reason, got none")
	}
}

// TestBlockedOnDependencyClosedButNotLanded: A closed on an "ok" card -- not merely OK, the
// evidence must say landed or merged (#2636) -- so B still waits.
func TestBlockedOnDependencyClosedButNotLanded(t *testing.T) {
	a := Task{ID: "a", State: StateClosed, Evidence: "cards:done 1-0 ok at 2026-09-22T18:00:00Z"}
	b := Task{ID: "b", State: StateOpen, DependsOn: []string{"a"}}
	reason := Blocked(b, []Task{a, b})
	if reason == "" {
		t.Fatalf("a closed on an ok card, not a landing; b wants a reason, got none")
	}
	if !strings.Contains(reason, "a") {
		t.Fatalf("reason %q does not name the dependency", reason)
	}
}

// TestNotBlockedWhenDependencyLandedOrMerged: A closed with evidence naming a merge or a
// landing is what clears B.
func TestNotBlockedWhenDependencyLandedOrMerged(t *testing.T) {
	for _, evidence := range []string{"o/n#1 merged abc123", "cards:done 2-0 landed at 2026-09-22T18:00:00Z"} {
		a := Task{ID: "a", State: StateClosed, Evidence: evidence}
		b := Task{ID: "b", State: StateOpen, DependsOn: []string{"a"}}
		if reason := Blocked(b, []Task{a, b}); reason != "" {
			t.Fatalf("evidence %q wants no reason, got %q", evidence, reason)
		}
	}
}

// TestBlockedOnSharedPathWithAWorkingTask: two tasks that touch the same file are never
// dealt at once; the task already working is not itself blocked by holding it.
func TestBlockedOnSharedPathWithAWorkingTask(t *testing.T) {
	working := Task{ID: "w", State: StateWorking, Paths: []string{"internal/sprint/refill.go"}}
	next := Task{ID: "n", State: StateOpen, Paths: []string{"internal/sprint/refill.go"}}
	if reason := Blocked(next, []Task{working, next}); reason == "" {
		t.Fatalf("n shares a path with working w; wants a reason, got none")
	}
	if reason := Blocked(working, []Task{working, next}); reason != "" {
		t.Fatalf("the working task is blocked by its own path: %q", reason)
	}
}

// TestDealOrderAThenCThenBOnlyAfterALands is #2636's control: three tasks, A with no
// dependency, B depending on A, C independent and lower priority (it unblocks nothing, so
// it scores under A). The deal order is A, C, then B only once A closes landed or merged --
// never B before A, and never a false wait on C for a dependency that is not its own.
func TestDealOrderAThenCThenBOnlyAfterALands(t *testing.T) {
	store := NewFakeStore()
	store.SetPresent("bench1", true)
	ctx := context.Background()
	now := time.Date(2026, 9, 22, 20, 0, 0, 0, time.UTC)
	if err := store.PutSprint(ctx, Sprint{Name: "s", Goal: "g", OpenedAt: now}); err != nil {
		t.Fatal(err)
	}
	tasks := []Task{
		{ID: "a", Kind: KindFix, Ref: "o/n#1", State: StateOpen, Paths: []string{"pkg/a.go"}, CreatedAt: now},
		{ID: "b", Kind: KindFix, Ref: "o/n#2", State: StateOpen, Paths: []string{"pkg/b.go"}, DependsOn: []string{"a"}, CreatedAt: now},
		{ID: "c", Kind: KindFix, Ref: "o/n#3", State: StateOpen, Paths: []string{"pkg/c.go"}, CreatedAt: now.Add(-time.Hour)},
	}
	for _, task := range tasks {
		if err := store.PutTask(ctx, task); err != nil {
			t.Fatal(err)
		}
		if err := store.AddTask(ctx, "s", task.ID); err != nil {
			t.Fatal(err)
		}
	}
	table := []deal.Capabilities{{Name: "bench1", Kind: deal.KindBench, Width: 4}}

	// Pass 1: only A and C are ready. B is not in the ready set at all (it is BLOCKED, not
	// merely unmatched), and the order among the two that are ready is A then C -- A
	// unblocks B, C unblocks nothing.
	res, err := Refill(ctx, store, "s", table, 0, now, true)
	if err != nil {
		t.Fatalf("Refill: %v", err)
	}
	if len(res.Placements) != 2 {
		t.Fatalf("pass 1 placed %d tasks, wants 2 (a, c): %+v", len(res.Placements), res.Placements)
	}
	if res.Placements[0].Task.ID != "a" || res.Placements[1].Task.ID != "c" {
		t.Fatalf("pass 1 order is %s, %s; wants a, c", res.Placements[0].Task.ID, res.Placements[1].Task.ID)
	}
	if reason, ok := res.Blocked["b"]; !ok || !strings.Contains(reason, "a") {
		t.Fatalf("b wants to be reported BLOCKED on a, got %q (present=%v)", reason, ok)
	}

	// A closes, but only on an "ok" card -- B still waits (#2636: not merely ok). This is a
	// real write to the store (a dry Refill never persists anything), so a is genuinely
	// closed for the next pass; c is still open and unowned (the dry runs above wrote
	// nothing), so c alone is ready.
	closedOK := store.tasks["a"]
	closedOK.State = StateClosed
	closedOK.Evidence = "cards:done 9-0 ok at " + now.Format(time.RFC3339)
	closedOK.DoneAt = now
	if err := store.PutTask(ctx, closedOK); err != nil {
		t.Fatal(err)
	}
	res, err = Refill(ctx, store, "s", table, 0, now, true)
	if err != nil {
		t.Fatalf("Refill after ok-only close: %v", err)
	}
	if len(res.Placements) != 1 || res.Placements[0].Task.ID != "c" {
		t.Fatalf("pass 2 wants only c dealt (a is closed, b still blocked), got: %+v", res.Placements)
	}
	if reason, ok := res.Blocked["b"]; !ok || !strings.Contains(reason, "not landed or merged") {
		t.Fatalf("b wants to stay BLOCKED on a's ok-only evidence, got %q (present=%v)", reason, ok)
	}

	// A's evidence is corrected to a landing -- now B is ready and gets dealt too.
	closedLanded := store.tasks["a"]
	closedLanded.Evidence = "o/n#1 merged " + short("abcdef123456")
	if err := store.PutTask(ctx, closedLanded); err != nil {
		t.Fatal(err)
	}
	res, err = Refill(ctx, store, "s", table, 0, now, true)
	if err != nil {
		t.Fatalf("Refill after landing: %v", err)
	}
	if len(res.Placements) != 2 {
		t.Fatalf("pass 3 wants c and b both dealt once a landed, got: %+v", res.Placements)
	}
	got := []string{res.Placements[0].Task.ID, res.Placements[1].Task.ID}
	if got[0] != "c" || got[1] != "b" {
		t.Fatalf("pass 3 order is %v; wants c, b (c is older, b unblocks nothing new)", got)
	}
	if _, ok := res.Blocked["b"]; ok {
		t.Fatalf("b is still reported BLOCKED after a landed: %v", res.Blocked)
	}
}
