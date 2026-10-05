package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// TestRecutKeepsIdLineageViaReplaces tests that recut creates a twin card,
// relinks dependants, and drops the old card with id lineage recorded via --replaces
// (docs/SPEC-SPRINT.md section 2, "A card replaced by its twin"; item 21).
func TestRecutKeepsIdLineageViaReplaces(t *testing.T) {
	t.Parallel()

	// 1. Re-cut for another tier (--tier pro):
	// Setup stream s1 with card "c1" (tier: flash) and "other".
	// Setup stream s2 with dep1, dep2 needing c1, and dep3 needing c1,other.
	w := newWorld(t, "reader-a", "reader-b")
	w.must(Add(w.s, AddReq{
		Stream: "s1",
		Cards: []CardAdd{
			{ID: "c1", Brief: "c1: work on flash (s1) tier: flash\nPATHS: a.go\n"},
			{ID: "other", Brief: "other: something else\n"},
		},
	}))
	w.must(Add(w.s, AddReq{
		Stream: "s2",
		Cards: []CardAdd{
			{ID: "dep1", Needs: []string{"c1"}, Brief: "dep1: depends on c1\n"},
			{ID: "dep2", Needs: []string{"c1"}, Brief: "dep2: depends on c1\n"},
			{ID: "dep3", Needs: []string{"c1", "other"}, Brief: "dep3: depends on c1 and other\n"},
		},
	}))

	require.Equal(t, Ready, w.s.Work.Placed("c1").Col)
	require.Equal(t, Waiting, w.s.Work.Placed("dep1").Col)
	require.Equal(t, Waiting, w.s.Work.Placed("dep2").Col)
	require.Equal(t, Waiting, w.s.Work.Placed("dep3").Col)

	// Re-cut c1 for tier pro
	p := Recut(w.s, RecutReq{
		Old:  "c1",
		Tier: cardhdr.RoutePro,
		Who:  "coordinator",
	})
	require.Empty(t, p.Refused, "recut must succeed")
	w.must(p)

	// Check that the old card was dropped with lineage recorded
	old := w.s.Work.Card("c1")
	require.NotNil(t, old)
	assert.False(t, old.Placed(), "the old card is off the table")
	assert.Equal(t, "dropped", old.F("outcome"))
	assert.Equal(t, "replaced by c1-pro", old.F("reason"), "drop reason records lineage")

	// Check that the twin card was created on the work table in s1
	twin := w.s.Work.Placed("c1-pro")
	require.NotNil(t, twin, "twin c1-pro is placed on the table")
	assert.Equal(t, "s1", twin.Row)
	assert.Equal(t, Ready, twin.Col)
	assert.Equal(t, "c1", twin.F(FieldReplaces), "twin records what it replaces")
	m, _ := cardhdr.ReadModel(twin.F("brief"))
	assert.Equal(t, cardhdr.RoutePro, m.Tier, "twin brief has updated tier pro")

	// Check that dependents were relinked
	assert.Equal(t, "c1-pro", w.s.Work.Placed("dep1").F("needs"))
	assert.Equal(t, "c1-pro", w.s.Work.Placed("dep2").F("needs"))
	assert.Equal(t, "c1-pro,other", w.s.Work.Placed("dep3").F("needs"))
	assert.Contains(t, w.s.Work.Placed("dep1").F(FieldRelinked), "c1 -> c1-pro")

	// Check no blocked judgment was raised
	for _, o := range w.s.Open {
		assert.NotEqual(t, NBlocked, o.Note.Type, "no blocked note raised for replaced card")
	}

	// 2. Re-cut for another scope (--brief-file / new brief):
	// Setup card c2 with dependent dep4
	w2 := newWorld(t, "reader-a", "reader-b")
	w2.must(Add(w2.s, AddReq{
		Stream: "s1",
		Cards: []CardAdd{
			{ID: "c2", Brief: "c2: initial scope\n"},
		},
	}))
	w2.must(Add(w2.s, AddReq{
		Stream: "s2",
		Cards: []CardAdd{
			{ID: "dep4", Needs: []string{"c2"}, Brief: "dep4: depends on c2\n"},
		},
	}))

	p2 := Recut(w2.s, RecutReq{
		Old:   "c2",
		New:   "c2-tb",
		Brief: "c2-tb: expanded scope\nPATHS: b.go\n",
		Who:   "coordinator",
	})
	require.Empty(t, p2.Refused)
	w2.must(p2)

	assert.Equal(t, "replaced by c2-tb", w2.s.Work.Card("c2").F("reason"))
	assert.Equal(t, "c2-tb", w2.s.Work.Placed("dep4").F("needs"))
	assert.Equal(t, "c2", w2.s.Work.Placed("c2-tb").F(FieldReplaces))

	// 3. Re-cut a card dropped before:
	// A card dropped earlier that left blocked judgments on dependents.
	// Recutting it closes the blocked judgments and relinks the dependents.
	w3 := newWorld(t, "reader-a", "reader-b")
	w3.must(Add(w3.s, AddReq{
		Stream: "s1",
		Cards: []CardAdd{
			{ID: "c3", Brief: "c3: first draft\n"},
		},
	}))
	w3.must(Add(w3.s, AddReq{
		Stream: "s2",
		Cards: []CardAdd{
			{ID: "dep5", Needs: []string{"c3"}, Brief: "dep5: depends on c3\n"},
		},
	}))
	// Drop c3, raising a blocked judgment on dep5
	w3.must(Drop(w3.s, DropReq{Sel: Sel{Only: []string{"c3"}}, Reason: "needs recut", Who: "coordinator"}))
	assert.Len(t, w3.s.Open, 1)
	assert.Equal(t, NBlocked, w3.s.Open[0].Note.Type)

	// Now recut the dropped card c3
	p3 := Recut(w3.s, RecutReq{
		Old:   "c3",
		New:   "c3-twin",
		Brief: "c3-twin: fixed\n",
		Who:   "coordinator",
	})
	require.Empty(t, p3.Refused)
	w3.must(p3)

	assert.Equal(t, "c3-twin", w3.s.Work.Placed("dep5").F("needs"))
	assert.Equal(t, "c3", w3.s.Work.Placed("c3-twin").F(FieldReplaces))
	// Blocked judgment was answered
	var blockedLeft int
	for _, o := range w3.s.Open {
		if o.Note.Type == NBlocked {
			blockedLeft++
		}
	}
	assert.Equal(t, 0, blockedLeft, "blocked judgments answered by recut")

	// 4. Refusals:
	for _, tc := range []struct {
		name string
		req  RecutReq
		why  string
	}{
		{"empty old", RecutReq{}, "one primary to re-cut"},
		{"ghost card", RecutReq{Old: "ghost", Tier: "pro"}, "no card ghost"},
		{"invalid tier", RecutReq{Old: "c3-twin", Tier: "superfast"}, "--tier wants"},
		{"missing tier and brief", RecutReq{Old: "c3-twin"}, "--tier <t> or --brief-file <f>"},
	} {
		res := Recut(w3.s, tc.req)
		require.NotEmpty(t, res.Refused, tc.name)
		assert.Contains(t, res.Refused[0].Why, tc.why, tc.name)
	}
}

func TestSetBriefTier(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		input    string
		tier     string
		expected string
	}{
		{
			name:     "replace existing tier on line 1",
			input:    "c1: work (s1) tier: flash\nPATHS: a.go\n",
			tier:     "pro",
			expected: "c1: work (s1) tier: pro\nPATHS: a.go\n",
		},
		{
			name:     "add tier to line 1 without tier",
			input:    "c1: work (s1)\nPATHS: a.go\n",
			tier:     "pro",
			expected: "c1: work (s1) tier: pro\nPATHS: a.go\n",
		},
		{
			name:     "single line brief",
			input:    "c1: work",
			tier:     "frontier",
			expected: "c1: work tier: frontier\n",
		},
	} {
		out := SetBriefTier(tc.input, tc.tier)
		assert.Equal(t, tc.expected, out, tc.name)
	}
}
