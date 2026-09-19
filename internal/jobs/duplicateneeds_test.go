package jobs

import "testing"

// A repeated need is one edge in the seeded graph (#1788). "a needs b" is true or
// false, never true twice: a node whose Needs names the same id more than once must
// not grow a second forward edge, a second reverse edge, or a second count in Edges.
func TestARepeatedNeedIsOneEdge(t *testing.T) {
	t.Run("a need named twice is one forward edge and one reverse edge", func(t *testing.T) {
		g, err := Seed([]Node{
			{ID: "a", Needs: []string{"b", "b"}},
			{ID: "b"},
		})
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
		if got := g.Edges(); got != 1 {
			t.Fatalf("Edges() = %d, want 1: the repeated need is one edge", got)
		}
		if got := g.Needs("a"); !sameIDs(got, []string{"b"}) {
			t.Fatalf("Needs(a) = %v, want [b]", got)
		}
		if got := g.Blocks("b"); !sameIDs(got, []string{"a"}) {
			t.Fatalf("Blocks(b) = %v, want [a]", got)
		}
	})

	t.Run("first occurrence keeps its position", func(t *testing.T) {
		g, err := Seed([]Node{
			{ID: "a", Needs: []string{"b", "c", "b"}},
			{ID: "b"},
			{ID: "c"},
		})
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
		if got := g.Edges(); got != 2 {
			t.Fatalf("Edges() = %d, want 2", got)
		}
		if got := g.Needs("a"); !sameIDs(got, []string{"b", "c"}) {
			t.Fatalf("Needs(a) = %v, want [b c] in seed order", got)
		}
	})

	t.Run("the four-fold receipt case is one edge", func(t *testing.T) {
		g, err := Seed([]Node{
			{ID: "a", Needs: []string{"b", "b", "b", "b"}},
			{ID: "b"},
		})
		if err != nil {
			t.Fatalf("seed: %v", err)
		}
		if got := g.Edges(); got != 1 {
			t.Fatalf("Edges() = %d, want 1: [b b b b] is one edge", got)
		}
	})

	t.Run("a repeated unresolvable need still refuses on the first occurrence", func(t *testing.T) {
		_, err := Seed([]Node{
			{ID: "a", Needs: []string{"z", "z"}},
		})
		if err == nil {
			t.Fatal("a repeated unresolvable need seeded without a refusal")
		}
		if got, want := err.Error(), "rule 2: a needs z which does not exist"; got != want {
			t.Fatalf("refusal = %q, want %q", got, want)
		}
	})
}
