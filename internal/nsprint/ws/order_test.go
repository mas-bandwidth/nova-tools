package ws_test

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/ws"
)

func TestComputeOrderTopologicalDependsOn(t *testing.T) {
	t.Parallel()

	cardIDs := []string{"card-3", "card-1", "card-2"}
	cards := map[string]*ws.CardOrderInfo{
		"card-1": {ID: "card-1", Issue: 101},
		"card-2": {ID: "card-2", Issue: 102, DependsOn: []string{"card-1"}},
		"card-3": {ID: "card-3", Issue: 103, DependsOn: []string{"card-2"}},
	}
	got := ws.ComputeOrder(cardIDs, cards, nil)
	want := []string{"card-1", "card-2", "card-3"}
	if len(got) != len(want) {
		t.Fatalf("ComputeOrder got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("at index %d: got %s, want %s", i, got[i], want[i])
		}
	}
}

func TestComputeOrderPathsOverlapTies(t *testing.T) {
	t.Parallel()

	// card-a (issue 10) and card-b (issue 20) overlap on "pkg/foo.go".
	// card-c (issue 5) touches "other/bar.go" (disjoint).
	cardIDs := []string{"card-b", "card-a", "card-c"}
	cards := map[string]*ws.CardOrderInfo{
		"card-a": {ID: "card-a", Issue: 10, Paths: []string{"pkg/foo.go"}},
		"card-b": {ID: "card-b", Issue: 20, Paths: []string{"pkg/foo.go"}},
		"card-c": {ID: "card-c", Issue: 5, Paths: []string{"other/bar.go"}},
	}
	got := ws.ComputeOrder(cardIDs, cards, nil)
	// card-c has issue 5, independent -> 1st.
	// card-a has issue 10, card-b has issue 20, paths overlap -> card-a comes before card-b.
	want := []string{"card-c", "card-a", "card-b"}
	if len(got) != len(want) {
		t.Fatalf("ComputeOrder got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("at index %d: got %s, want %s", i, got[i], want[i])
		}
	}
}

func TestComputeOrderLearnedMisses(t *testing.T) {
	t.Parallel()

	// card-low (issue 10) and card-high (issue 20).
	// Normally card-low comes before card-high.
	// But card-low missed card-high in an earlier run (card-high must precede card-low).
	cardIDs := []string{"card-low", "card-high"}
	cards := map[string]*ws.CardOrderInfo{
		"card-low":  {ID: "card-low", Issue: 10, Paths: []string{"file.go"}},
		"card-high": {ID: "card-high", Issue: 20, Paths: []string{"file.go"}},
	}
	misses := []ws.OrderMiss{
		{Stream: "test-stream", Card: "card-low", Missing: "card-high", Why: "ORDER: built on base, missing card-high"},
	}
	got := ws.ComputeOrder(cardIDs, cards, misses)
	want := []string{"card-high", "card-low"}
	if len(got) != len(want) {
		t.Fatalf("ComputeOrder got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("at index %d: got %s, want %s", i, got[i], want[i])
		}
	}
}
