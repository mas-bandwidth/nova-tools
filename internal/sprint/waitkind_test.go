package sprint

import "testing"

// TestWaitOfConsolidation verifies that WaitOf is the single path for all wait
// mechanisms. This consolidates held, sentinel, and needs into one unified function.
func TestWaitOfConsolidation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		card *Card
		want WaitKind
	}{
		{"held card", &Card{ID: "c1", Row: "s1", Col: Waiting, Score: 1, Fields: map[string]string{"held": "stamp"}}, WaitRelease},
		{"sentinel", &Card{ID: "c2", Row: "s1", Col: Waiting, Score: 2, Fields: map[string]string{"kind": "sentinel"}}, WaitRelease},
		{"needs", &Card{ID: "c3", Row: "s1", Col: Waiting, Score: 3, Fields: map[string]string{"needs": "card1,card2"}}, WaitNeeds},
		{"waiting", &Card{ID: "c4", Row: "s1", Col: Waiting, Score: 4, Fields: map[string]string{"kind": "primary"}}, WaitPosition},
		{"landed", &Card{ID: "c5", Row: "s1", Col: Landed, Score: 5, Fields: map[string]string{"kind": "primary"}}, WaitNone},
		{"nil", nil, WaitNone},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := WaitOf(tt.card)
			if got != tt.want {
				t.Errorf("WaitOf(%v) = %s, want %s", tt.card, got, tt.want)
			}
		})
	}
}
