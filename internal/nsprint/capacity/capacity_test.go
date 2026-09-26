package capacity_test

import (
	"context"
	"errors"
	"strings"
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
	t.Parallel()

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

// TestWidthAccounting checks the pure width arithmetic underlying task width.
func TestWidthAccounting(t *testing.T) {
	t.Parallel()

	got := task.WidthFrom(32, 8, 0)
	want := task.Width{Desired: 32, Leased: 8, Free: 24}
	if got != want {
		t.Fatalf("WidthFrom = %+v want %+v", got, want)
	}
}

// TestNormalizeTiersIsTheThreeModelTypes: --tiers is what a worker
// advertises it can run, each name one of frontier, pro, flash (Glenn
// 2026-09-26); duplicates drop, "" clears, any other word is refused naming
// the three.
func TestNormalizeTiersIsTheThreeModelTypes(t *testing.T) {
	t.Parallel()
	for raw, want := range map[string]string{"frontier,pro": "frontier,pro", "frontier pro flash": "frontier,pro,flash",
		"pro, pro": "pro", "flash": "flash", "": capacity.Clear, "  ": capacity.Clear} {
		got, err := capacity.NormalizeTiers(raw)
		if err != nil || got != want {
			t.Errorf("NormalizeTiers(%q) = %q, %v; want %q", raw, got, err, want)
		}
	}
	for _, raw := range []string{"turbo", "pro,turbo", "Frontier", "pro;rm"} {
		if got, err := capacity.NormalizeTiers(raw); err == nil || got != "" || !strings.Contains(err.Error(), "frontier, pro or flash") {
			t.Errorf("NormalizeTiers(%q) = %q, %v; want a refusal naming frontier, pro or flash", raw, got, err)
		}
	}
}
