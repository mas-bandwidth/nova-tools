package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestNewPathSilentMemberGoesDown (the 4806 read, M2): a member that stops
// beating goes down at the tick after its beat lapses, and its cards go round
// the fleet to the member that beats; the member that beats is never taken
// down. R2's read (the member's cells with each card's primary, and the beats
// of its members) goes on the wire, and its step applies: before, the read was
// refused REQUEST on every tick and the member stayed up with its cards.
func TestNewPathSilentMemberGoesDown(t *testing.T) {
	t.Parallel()
	na := newNPApp(t)
	for _, l := range npTicking {
		na.ok(l)
	}
	na.ok("tick")
	queue := func(m string) int {
		var q struct {
			Cards []struct{ ID string } `json:"cards"`
		}
		if err := json.Unmarshal([]byte(na.ok("queue --as "+m+" --json")), &q); err != nil {
			t.Fatal(err)
		}
		return len(q.Cards)
	}
	if a, b := queue("m1"), queue("m2"); a != 3 || b != 3 {
		t.Fatalf("the deal: m1 %d, m2 %d, want 3 each", a, b)
	}
	// m1 beats every second; m2 is silent past its 15 s
	for i := 0; i < 12; i++ {
		na.ok("fleet beat m1")
		na.ok("tick")
	}
	var w struct {
		Tables map[string]map[string]map[string]string `json:"tables"`
	}
	if err := json.Unmarshal([]byte(na.ok("where --json")), &w); err != nil {
		t.Fatal(err)
	}
	if got := w.Tables["fleet"]["m2"]["status"]; got != "down" {
		t.Errorf("m2, silent: status %q, want down", got)
	}
	if got := w.Tables["fleet"]["m1"]["status"]; got != "up" {
		t.Errorf("m1, beating: status %q, want up", got)
	}
	if a, b := queue("m1"), queue("m2"); a != 6 || b != 0 {
		t.Errorf("after m2 went down: m1 %d, m2 %d, want 6 and 0", a, b)
	}
	in := na.ok("inbox")
	if !strings.Contains(in, "m2 down: no beat") || strings.Contains(in, "refused") {
		t.Errorf("the inbox:\n%s", in)
	}
}
