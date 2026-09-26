package ws

import (
	"strings"
	"testing"
)

// TestPositionPlacesACardWithNoIssueByCreatedAt pins the tie-break: every
// card has a position, and it is pairwise-stable: created_at is the one
// key (older first), the issue number only breaks a tie (a numbered card
// before one with none, then the lower number, then id), so an older card
// with no issue (t0) comes before a younger numbered one, never last, and a
// younger #3 pushed later stands after an older #5 and an older card with
// no issue instead of reordering them (the round-5 read's a5 u b3); with no
// created_at anywhere it is issue order, then the rest by id (the old order).
func TestPositionPlacesACardWithNoIssueByCreatedAt(t *testing.T) {
	t.Parallel()
	ids := func(cs []OrderCard) string {
		var out []string
		for _, c := range cs {
			out = append(out, c.ID)
		}
		return strings.Join(out, " ")
	}
	cases := []struct {
		name  string
		cards []OrderCard
		want  string
	}{
		{"older no-issue first", []OrderCard{{ID: "t2", Issue: 2, Created: 200}, {ID: "t0", Created: 50}, {ID: "t1", Issue: 1, Created: 100},
			{ID: "t9", Created: 300}, {ID: "s:sentinel", Sentinel: true, Created: 1}}, "t0 t1 t2 t9 s:sentinel"},
		{"created_at first: a younger #3 does not pass an older #5", []OrderCard{{ID: "a", Issue: 5, Created: 100}, {ID: "b", Issue: 3, Created: 400}, {ID: "u", Created: 200}}, "a u b"},
		{"pairwise-stable: the pair keeps its order without the third", []OrderCard{{ID: "a", Issue: 5, Created: 100}, {ID: "u", Created: 200}}, "a u"},
		{"same created_at: issue order", []OrderCard{{ID: "b", Issue: 3, Created: 100}, {ID: "a", Issue: 5, Created: 100}, {ID: "u", Created: 100}}, "b a u"},
		{"tie goes to the numbered", []OrderCard{{ID: "u", Created: 100}, {ID: "n", Issue: 7, Created: 100}}, "n u"},
		{"no created_at: issue order, then id", []OrderCard{{ID: "z"}, {ID: "y", Issue: 2}, {ID: "x", Issue: 1}, {ID: "w"}}, "x y w z"},
	}
	for _, tc := range cases {
		if got := ids(Position(tc.cards)); got != tc.want {
			t.Fatalf("%s: %s, want %s", tc.name, got, tc.want)
		}
	}
	order, _, err := Order([]OrderCard{{ID: "t2", Issue: 2, Created: 200, Paths: []string{"a.go"}}, {ID: "t0", Created: 50, Paths: []string{"a.go"}}})
	if err != nil || order[0].ID != "t0" {
		t.Fatalf("Order with an older t0 with no issue: %v %v", order, err)
	}
	t.Logf("positions pinned; Order: t0 (no issue, created 50) before t2 (#2, created 200)")
}
