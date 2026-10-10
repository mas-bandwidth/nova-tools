package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// Every deal of a work card carries the sprint row's two gate bars as nova-config applied
// them (docs/SPEC-SPRINT.md section 5, the gate verdict), and its packet hands them to the
// member; a redeal takes the row's bars at the redeal, so bars turned off since are taken
// off the card. With no bars in the sprint row the card carries none, and the store's
// GateBars, the lander's read, applies the flaky bar's default (decide.DefaultFlaky), so a
// flaky red gate is rerun once without a setting.
func TestEveryWorkCardCarriesTheGateBars(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, flaky, pre string
	}{{"bars set", "0.8", "0.75"}, {"no bars", "", ""}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness(t)
			h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m1"}))
			h.m.SetRoutes([]sprint.Route{route("flash-a", "flash")})
			h.m.SetGateBars(tc.flaky, tc.pre)
			bars, err := h.st.GateBars(h.ctx)
			require.NoError(t, err)
			assert.Equal(t, [2]string{decide.GateFlakyBar(tc.flaky), tc.pre}, bars, "the lander's read applies the flaky bar's default")
			h.addReady("s1", 1, briefOf("flash", ""))
			h.must(DealStep(sprint.DealReq{}))
			w1 := h.workCards()["s1-1.w1"]
			require.NotNil(t, w1)
			assert.Equal(t, []string{tc.flaky, tc.pre}, []string{w1.F(sprint.FieldDecideGateFlaky), w1.F(sprint.FieldDecideGatePreexisting)})
			p := sprint.PacketOf("", 0, w1, h.snap().Work.Card("s1-1"), nil, nil)
			assert.Equal(t, []string{tc.flaky, tc.pre}, []string{p.DecideGateFlaky, p.DecideGatePreexisting}, "the packet hands the member the bars")
			// the member goes down holding it, the bars are turned off, and the redeal takes them off
			h.must(TakeStep(sprint.TakeReq{As: "m1", Sel: sprint.Sel{IDs: []string{w1.ID}}, Gens: map[string]int{w1.ID: w1.Int("gen")}, Who: "m1"}))
			h.run(FleetStep(sprint.FleetReq{Op: "down", Member: "m1"}))
			h.must(FleetStep(sprint.FleetReq{Op: "up", Member: "m2"}))
			h.m.SetGateBars("", "")
			h.run(DealStep(sprint.DealReq{}))
			wc := h.workCards()["s1-1.w1"]
			require.NotNil(t, wc)
			require.Equal(t, "m2", wc.Row, "dealt again")
			assert.Empty(t, wc.F(sprint.FieldDecideGateFlaky)+wc.F(sprint.FieldDecideGatePreexisting), "the redeal takes the row's bars, none")
			h.clean("gate bars")
		})
	}
}
