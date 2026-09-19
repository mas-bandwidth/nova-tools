package swarm

import (
	"context"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/lanes"
)

// writeLanes lays the cards into their lanes the way nova-pulse cut does.
func writeLanes(t *testing.T, queue string, cards ...lanes.Card) {
	t.Helper()
	if _, err := lanes.Write(queue, cards); err != nil {
		t.Fatalf("write lanes: %v", err)
	}
}

func entryIDs(entries []lanes.Entry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.ID)
	}
	return out
}

func sameIDs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// red-lane-drains-before-green (docs/SPEC-JOBS.md section 5, line 112):
// nova-swarm pull drains red (fixes to a red bench or a red PR), then green
// (small, already-approved PRs), then small (the shortest step budget), then
// next; ordering inside a lane is source order.
func TestRedLaneDrainsBeforeGreen(t *testing.T) {
	queue := t.TempDir()
	// Source order inside red is rb then ra; both are the same size, but with no
	// scorer the rule keeps source order rather than guessing.
	writeLanes(t, queue,
		lanes.Card{ID: "rb", Kind: "fix", Red: true, Steps: 12, Body: "RESULT rb\n"},
		lanes.Card{ID: "ra", Kind: "fix", Red: true, Steps: 12, Body: "RESULT ra\n"},
		lanes.Card{ID: "gd", Kind: "read", Approved: true, Steps: 4, Body: "RESULT gd\n"},
		lanes.Card{ID: "sf", Kind: "read", Steps: 6, Body: "RESULT sf\n"},
		lanes.Card{ID: "nh", Kind: "fix", Steps: 21, Body: "RESULT nh\n"},
	)

	got, err := lanes.Drain(context.Background(), queue, nil, 0.9)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	want := []string{"rb", "ra", "gd", "sf", "nh"}
	if !sameIDs(entryIDs(got), want) {
		t.Fatalf("pull order = %v, want %v (red, then green, then small, then next; source order inside a lane)", entryIDs(got), want)
	}
}

// a-scorer-refusal-keeps-source-order (docs/SPEC-JOBS.md section 5, line 112):
// a tie the rule cannot break is asked of Jev as one typed decision behind the
// 0.9 floor, and a refusal keeps source order.
func TestAScorerRefusalKeepsSourceOrder(t *testing.T) {
	queue := t.TempDir()
	writeLanes(t, queue,
		lanes.Card{ID: "first", Kind: "fix", Red: true, Steps: 12, Body: "RESULT first\n"},
		lanes.Card{ID: "second", Kind: "fix", Red: true, Steps: 12, Body: "RESULT second\n"},
	)

	// The scorer answers, but at 0.4 it is under the 0.9 floor: a suggestion,
	// never an authorization, so source order stands.
	refusal := lanes.Scorer(func(ctx context.Context, state string, options []string) (string, float64, error) {
		return "second", 0.4, nil
	})
	got, err := lanes.Drain(context.Background(), queue, refusal, 0.9)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	want := []string{"first", "second"}
	if !sameIDs(entryIDs(got), want) {
		t.Fatalf("a refusal changed the order: got %v, want source order %v", entryIDs(got), want)
	}

	// An answer at or above the floor is the one typed decision the section
	// allows, and it may order the lane.
	answered := lanes.Scorer(func(ctx context.Context, state string, options []string) (string, float64, error) {
		return "second", 0.95, nil
	})
	got, err = lanes.Drain(context.Background(), queue, answered, 0.9)
	if err != nil {
		t.Fatalf("drain: %v", err)
	}
	want = []string{"second", "first"}
	if !sameIDs(entryIDs(got), want) {
		t.Fatalf("a confident answer was not taken: got %v, want %v", entryIDs(got), want)
	}
}
