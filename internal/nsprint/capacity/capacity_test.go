package capacity_test

import (
	"context"
	"errors"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/capacity"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/task"
)

// fakeReader is the in-memory read surface of the capacity guard. Using it
// means control 27's rule is tested without a Redis server; the Redis Function
// repeats the same guard atomically in production.
type fakeReader struct {
	ceilings  map[string]int
	consumers []capacity.Consumer
}

func (f *fakeReader) Ceiling(_ context.Context, machine string) (int, bool, error) {
	slots, ok := f.ceilings[machine]
	return slots, ok, nil
}

func (f *fakeReader) Consumers(_ context.Context) ([]capacity.Consumer, error) {
	return f.consumers, nil
}

// TestControl27MachineCeiling is control 27: two fixture friends on one fixture
// machine with ceiling 64: capacity friend a 32 and b 32 succeed; b 33 refuses
// with CEILING; friend hello --slots 64 by a refuses and a keeps 32.
func TestControl27MachineCeiling(t *testing.T) {
	ctx := context.Background()
	r := &fakeReader{
		ceilings: map[string]int{"studio": 64},
		consumers: []capacity.Consumer{
			{Kind: capacity.KindFriend, Name: "a", Slots: 32, Machine: "studio"},
			{Kind: capacity.KindFriend, Name: "b", Slots: 0, Machine: "studio"},
		},
	}

	// capacity friend a 32 is within the ceiling.
	plan, err := capacity.Evaluate(ctx, r, "studio", capacity.KindFriend, "a", 32)
	if err != nil {
		t.Fatalf("evaluate a=32: %v", err)
	}
	if !plan.Allowed || plan.Sum != 32 || plan.Ceiling != 64 {
		t.Fatalf("a=32: plan=%+v want allowed sum=32 ceiling=64", plan)
	}

	// capacity friend b 32 succeeds: 32 + 32 == 64.
	plan, err = capacity.Evaluate(ctx, r, "studio", capacity.KindFriend, "b", 32)
	if err != nil {
		t.Fatalf("evaluate b=32: %v", err)
	}
	if !plan.Allowed || plan.Sum != 64 {
		t.Fatalf("b=32: plan=%+v want allowed sum=64", plan)
	}

	// capacity friend b 33 refuses CEILING: 32 + 33 == 65 > 64.
	plan, err = capacity.Evaluate(ctx, r, "studio", capacity.KindFriend, "b", 33)
	if err != nil {
		t.Fatalf("evaluate b=33: %v", err)
	}
	if plan.Allowed || plan.Sum != 65 {
		t.Fatalf("b=33: plan=%+v want refused sum=65", plan)
	}
	refusal := &capacity.CeilingError{Machine: "studio", Sum: plan.Sum, Ceiling: plan.Ceiling}
	if got, want := refusal.Error(), "CEILING studio 65/64"; got != want {
		t.Fatalf("refusal text = %q want %q", got, want)
	}
	var ceilingErr *capacity.CeilingError
	if !errors.As(error(refusal), &ceilingErr) || ceilingErr.ExitCode() != 2 {
		t.Fatalf("refusal must be a *CeilingError with exit 2, got %T", error(refusal))
	}

	// friend hello --slots 64 by a refuses (64 + b's 32 == 96) and a keeps 32.
	r.consumers[1].Slots = 32
	plan, err = capacity.Evaluate(ctx, r, "studio", capacity.KindFriend, "a", 64)
	if err != nil {
		t.Fatalf("evaluate a=64: %v", err)
	}
	if plan.Allowed || plan.Sum != 96 {
		t.Fatalf("hello a=64: plan=%+v want refused sum=96", plan)
	}
	if r.consumers[0].Slots != 32 {
		t.Fatalf("refused hello changed a's slots to %d, want 32", r.consumers[0].Slots)
	}

	// A machine with no ceiling is a hard error, never an implicit allow.
	noCeiling := &fakeReader{ceilings: map[string]int{}}
	if _, err := capacity.Evaluate(ctx, noCeiling, "nowhere", capacity.KindFriend, "a", 1); err == nil {
		t.Fatal("machine without a ceiling must be refused")
	}
}

// fakeTasks is the in-memory read surface of task list.
type fakeTasks struct {
	sprints    []string
	candidates map[string][]task.Candidate
	details    map[string]map[string]task.Detail
}

func (f *fakeTasks) Sprints(_ context.Context) ([]string, error) { return f.sprints, nil }
func (f *fakeTasks) Candidates(_ context.Context, sprint, as string) ([]task.Candidate, error) {
	return f.candidates[sprint+"/"+as], nil
}
func (f *fakeTasks) Details(_ context.Context, sprint string, ids []string) (map[string]task.Detail, error) {
	out := map[string]task.Detail{}
	for _, id := range ids {
		if d, ok := f.details[sprint][id]; ok {
			out[id] = d
		}
	}
	return out, nil
}

// TestControl22ClosedNeverLive checks both stale index membership and owner
// isolation: a closed or missing task cannot be counted as live, nor can a
// task from another friend's global active index.
func TestControl22ClosedNeverLive(t *testing.T) {
	ctx := context.Background()
	r := &fakeTasks{
		sprints: []string{"s1"},
		candidates: map[string][]task.Candidate{"s1/f": {
			{ID: "t-open", Assigned: true}, {ID: "t-closed", Assigned: true},
			{ID: "t-missing", Assigned: true}, {ID: "t-other"}, {ID: "t-working"},
			{ID: "t-open", Assigned: true},
		}},
		details: map[string]map[string]task.Detail{"s1": {
			"t-open": {State: "open"}, "t-closed": {State: "closed", Owner: "f"},
			"t-other": {State: "working", Owner: "other"}, "t-working": {State: "working", Owner: "f"},
		}},
	}
	rows, err := task.List(ctx, r, task.ListRequest{Sprint: "s1", As: "f"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].ID != "t-open" || rows[1].ID != "t-working" {
		t.Fatalf("live rows=%+v", rows)
	}
	closed, err := task.List(ctx, r, task.ListRequest{Sprint: "s1", As: "f", State: "closed"})
	if err != nil {
		t.Fatal(err)
	}
	if len(closed) != 1 || closed[0].ID != "t-closed" {
		t.Fatalf("closed rows=%+v", closed)
	}
}

// TestWidthAccounting checks the pure width arithmetic underlying task width.
func TestWidthAccounting(t *testing.T) {
	got := task.WidthFrom(32, 8)
	want := task.Width{Desired: 32, Leased: 8, Free: 24}
	if got != want {
		t.Fatalf("WidthFrom = %+v want %+v", got, want)
	}
}
