package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLevelTargetRequiresBacklogProgress(t *testing.T) {
	t.Parallel()
	up := []string{"a", "b", "c"}
	for _, tc := range []struct {
		name  string
		n     map[string]int
		from  string
		avoid []string
		want  string
	}{
		{"refused shortest", map[string]int{"a": 2, "b": 1, "c": 0}, "a", []string{"c"}, ""},
		{"refused negative shortest", map[string]int{"a": 0, "b": -1, "c": -2}, "a", []string{"c"}, ""},
		{"legal mean target", map[string]int{"a": 3, "b": 1, "c": 0}, "a", []string{"c"}, "b"},
		{"shortest preferred", map[string]int{"a": 2, "b": 1, "c": 0}, "a", nil, "c"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newRound(up, "0")
			to := r.levelTo(up, tc.n, map[string]int{"a": 4, "b": 3, "c": 1}, map[string]int{"a": 4, "b": 4, "c": 2}, tc.from, tc.avoid)
			assert.Equal(t, tc.want, to)
			if to == "" {
				assert.Equal(t, uint64(0), r.count, "a refused move consumes no turn")
			} else {
				assert.Greater(t, tc.n[tc.from]-tc.n[to], 1)
				assert.Equal(t, to, indexPast(up, r.value()))
			}
		})
	}
}

func TestLevelStopsWhenOnlyNonprogressTargetsRemain(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name         string
		olderRefuses bool
	}{
		{"all ready cards refuse shortest", true},
		{"older card can move safely", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := fleetWorld(t, 0, 2, "a", "b")
			w.must(FleetStep(w.s, FleetReq{Op: "up", Member: "c", Width: 1}))
			refusal := map[string]string{FieldStagingTake + "1": ProviderTake{Member: "c"}.String()}
			putWorkCard(w, "newest", "a", Ready, 4, refusal)
			var olderFields map[string]string
			if tc.olderRefuses {
				olderFields = refusal
			}
			putWorkCard(w, "older", "a", Ready, 3, olderFields)
			for _, member := range []string{"a", "b"} {
				for _, suffix := range []string{"1", "2"} {
					putWorkCard(w, member+suffix, member, Working, 1, nil)
				}
			}
			putWorkCard(w, "bready", "b", Ready, 2, nil)
			putWorkCard(w, "c1", "c", Working, 1, nil)
			before, beforeHad := w.s.Fleet.Prop(PropDealIndex)
			p := FleetStep(w.s, FleetReq{Op: "level"})
			if tc.olderRefuses {
				assert.Empty(t, p.Units)
			} else {
				require.Len(t, p.Units, 1)
				assert.Equal(t, "older.w1", p.Units[0].Key)
			}
			w.must(p)
			assert.Equal(t, "a", w.s.Fleet.Card("newest.w1").Row)
			assert.Equal(t, 2, w.s.Fleet.Card("newest.w1").Int("gen"))
			assert.Equal(t, "b", w.s.Fleet.Card("bready.w1").Row)
			after, afterHad := w.s.Fleet.Prop(PropDealIndex)
			if tc.olderRefuses {
				assert.Equal(t, "a", w.s.Fleet.Card("older.w1").Row)
				assert.Equal(t, 2, w.s.Fleet.Card("older.w1").Int("gen"))
				assert.Equal(t, before, after)
				assert.Equal(t, beforeHad, afterHad)
			} else {
				assert.Equal(t, "c", w.s.Fleet.Card("older.w1").Row)
				assert.Equal(t, 3, w.s.Fleet.Card("older.w1").Int("gen"))
				assert.True(t, afterHad)
				assert.Equal(t, "c", indexPast(w.s.Fleet.Rows(), after))
				assert.Equal(t, map[string]int{"a": 3, "b": 3, "c": 2}, perMember(w.s))
			}
		})
	}
}
