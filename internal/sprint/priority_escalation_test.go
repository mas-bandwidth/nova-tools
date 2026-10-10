package sprint

import (
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A direct tier escalation opens a repair attempt under the same priority policy
// as Rework (docs/SPEC-SPRINT.md, Priority), including older normal attempts.
func TestIdenticalFailureEscalationUsesReworkPriority(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, level, policy, want string }{
		{"default normal", "", "", PriorityFix},
		{"default low", PriorityLow, "", PriorityFix},
		{"high policy", "", PriorityHigh, PriorityHigh},
		{"keep normal", "", ReworkKeep, PriorityNormal},
		{"keep low", PriorityLow, ReworkKeep, PriorityLow},
		{"high stays", PriorityHigh, "", PriorityHigh},
		{"fix stays", PriorityFix, PriorityHigh, PriorityFix},
		{"critical stays", PriorityCritical, "", PriorityCritical},
		{"blocker stays", PriorityBlocker, "", PriorityBlocker},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			w := setup(t, 1)
			w.s.Routes = []Route{
				{Name: "small", Tier: cardhdr.RouteFlash, Provider: "p", Model: "small", Enabled: true},
				{Name: "next", Tier: cardhdr.RoutePro, Provider: "p", Model: "next", Enabled: true},
			}
			pr := w.s.Work.Card("s1-1")
			pr.Fields["brief"] = "c: repair output (s1) tier: heavy\nREPO: mas-bandwidth/nova-tools\n\nThe task.\n"
			pr.Fields[FieldPriority] = tc.level
			// Keep the first retry's level to reproduce a pre-policy held attempt.
			w.s.Work.SetProps(map[string]string{PropReworkPriority: ReworkKeep})
			finished(w, pr.ID, true)
			w.must(Rework(w.s, ReworkReq{Sel: Sel{IDs: []string{pr.ID}}, Fix: "repair the output"}))
			pr = w.s.Work.Card(pr.ID)
			require.Equal(t, 2, pr.Int("attempt"))
			wc := w.s.Fleet.Card(pr.F("work"))
			require.NotNil(t, wc)
			takeCard(w, wc.ID)
			w.s.Work.SetProps(map[string]string{PropReworkPriority: tc.policy})
			w.must(Finish(w.s, FinishReq{Sel: Sel{IDs: []string{wc.ID}}, Gens: gensOf(w.s, wc.ID), Failed: true, Report: "r; second attempt"}))
			pr = w.s.Work.Card(pr.ID)
			require.Equal(t, Ready, pr.Col, "the direct escalation, not the review path")
			assert.Equal(t, cardhdr.RoutePro, pr.F(FieldTierNow))
			level, _ := CardPriority(pr)
			assert.Equal(t, tc.want, level)
			assert.Empty(t, pr.F("result"))
		})
	}
}
