package worklang_test

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/worklang"
)

// The needs/blocks graph and readiness contract of docs/SPEC-WORKLANG.md, seen
// red first: four tests, no network and no model call, every refusal named by
// the field it refuses and the id it names.
func TestWorklangNeedsGraph(t *testing.T) {
	// worklang-needs-absent-node-is-a-refusal: a `:needs` naming an absent id is
	// refused naming the field and the id; neither the graph nor the journal moves.
	t.Run("worklang-needs-absent-node-is-a-refusal", func(t *testing.T) {
		p := parsePlan(t, `(:plan :version 1
  (:node :id "a" :kind go-fix :needs ("b"))
  (:node :id "c" :kind docs))`)
		if _, err := p.Graph(); err == nil {
			t.Fatal("a :needs naming an absent node was dropped instead of refused")
		} else {
			ref := assertRefusal(t, err)
			msg := ref.Error()
			if !strings.Contains(msg, ":needs") {
				t.Errorf("absent-need refusal does not name the field :needs: %s", msg)
			}
			if !strings.Contains(msg, "b") {
				t.Errorf("absent-need refusal does not name the id b: %s", msg)
			}
		}
		// The refusal left the reader's data untouched: the same plan still
		// parses, and a plan whose needs resolve still builds.
		good := parsePlan(t, `(:plan :version 1
  (:node :id "a" :kind go-fix :needs ("c"))
  (:node :id "c" :kind docs))`)
		if _, err := good.Graph(); err != nil {
			t.Fatalf("a resolvable :needs was refused: %v", err)
		}
	})

	// worklang-needs-cycle-refuses-at-load: a two-node cycle is refused by
	// validator rule 3 before publication, the refusal the `:deps` field already
	// carries.
	t.Run("worklang-needs-cycle-refuses-at-load", func(t *testing.T) {
		p := parsePlan(t, `(:plan :version 1
  (:node :id "a" :kind go-fix :needs ("b"))
  (:node :id "b" :kind go-fix :needs ("a")))`)
		_, err := p.Graph()
		if err == nil {
			t.Fatal("a :needs cycle built a graph instead of being refused")
		}
		ref := assertRefusal(t, err)
		for _, want := range []string{"rule 3", ":needs", "cycle", "a", "b"} {
			if !strings.Contains(ref.Error(), want) {
				t.Errorf("cycle refusal %q does not name %q", ref.Error(), want)
			}
		}
	})

	// worklang-blocks-and-needs-are-one-edge: `A :needs (B)` and `B :blocks (A)`
	// expand to the same ready order.
	t.Run("worklang-blocks-and-needs-are-one-edge", func(t *testing.T) {
		byNeeds := parsePlan(t, `(:plan :version 1
  (:node :id "a" :kind go-fix :needs ("b"))
  (:node :id "b" :kind docs :blocks ()))`)
		byBlocks := parsePlan(t, `(:plan :version 1
  (:node :id "a" :kind go-fix :needs ())
  (:node :id "b" :kind docs :blocks ("a")))`)

		gn, err := byNeeds.Graph()
		if err != nil {
			t.Fatalf("the :needs spelling was refused: %v", err)
		}
		gb, err := byBlocks.Graph()
		if err != nil {
			t.Fatalf("the :blocks spelling was refused: %v", err)
		}

		if got := gn.Needs("a"); !sameIDs(got, []string{"b"}) {
			t.Errorf("needs(a) = %v, want [b]", got)
		}
		if got := gb.Needs("a"); !sameIDs(got, []string{"b"}) {
			t.Errorf("derived needs(a) = %v, want [b]", got)
		}
		if got := gn.Blocks("b"); !sameIDs(got, []string{"a"}) {
			t.Errorf("blocks(b) = %v, want [a]", got)
		}
		if got := gb.Blocks("b"); !sameIDs(got, []string{"a"}) {
			t.Errorf("blocks(b) = %v, want [a]", got)
		}
		if !sameIDs(gn.ReadySet(), gb.ReadySet()) {
			t.Errorf("ready order differs: needs=%v blocks=%v", gn.ReadySet(), gb.ReadySet())
		}
		if !sameIDs(gn.ReadySet(), []string{"b"}) {
			t.Errorf("ready set = %v, want [b] (a is blocked by its open need)", gn.ReadySet())
		}
	})

	// worklang-ready-excludes-a-node-whose-need-is-open: e02 is printed but not
	// pullable while e01 is an open PR, and becomes pullable when e01 settles
	// after merging and going green; a revert of e01 flags e02 needs-broken.
	t.Run("worklang-ready-excludes-a-node-whose-need-is-open", func(t *testing.T) {
		p := parsePlan(t, `(:plan :version 1
  (:node :id "e01" :kind go-fix)
  (:node :id "e02" :kind go-fix :needs ("e01")))`)
		g, err := p.Graph()
		if err != nil {
			t.Fatalf("graph: %v", err)
		}

		// e02 is admitted (printed) even though it is not ready.
		if !sameIDs(g.Order(), []string{"e01", "e02"}) {
			t.Fatalf("order = %v, want both nodes admitted", g.Order())
		}
		ready, blocker := g.Ready("e02")
		if ready {
			t.Fatal("e02 is ready while its need e01 is an open PR")
		}
		if blocker == nil || blocker.Need != "e01" || blocker.State != "open" {
			t.Fatalf("e02 blocker = %+v, want e01 open", blocker)
		}

		// e01 settles after merging and going green: e02 becomes pullable.
		g.Settle("e01")
		if ready, blocker = g.Ready("e02"); !ready {
			t.Fatalf("e02 is not ready after e01 merged and went green: blocker=%+v", blocker)
		}
		if !sameIDs(g.ReadySet(), []string{"e02"}) {
			t.Fatalf("ready set = %v, want [e02]", g.ReadySet())
		}

		// A revert of e01 flags e02 needs-broken, never silently ready.
		g.Revert("e01")
		if !g.NeedsBroken("e02") {
			t.Fatal("e02 was not flagged needs-broken after its need e01 was reverted")
		}
		if ready, blocker = g.Ready("e02"); ready {
			t.Fatal("a needs-broken e02 is ready")
		} else if blocker == nil || blocker.State != "needs-broken" {
			t.Fatalf("e02 blocker = %+v, want state needs-broken", blocker)
		}
	})
}

func parsePlan(t *testing.T, body string) *worklang.Plan {
	t.Helper()
	p, err := worklang.ParsePlan("work.work", []byte(body), worklang.DefaultLimits())
	if err != nil {
		t.Fatalf("parse plan: %v", err)
	}
	return p
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
