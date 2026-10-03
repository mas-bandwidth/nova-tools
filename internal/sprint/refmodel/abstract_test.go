package refmodel_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/refmodel"
)

// A friend's fleet row (sprint.FriendRow) is no member of the model: the model's members
// are the machines, so the differential state never counts her row as a machine down.
func TestAbstractTakesNoFriendRowForAMember(t *testing.T) {
	t.Parallel()
	w := newWorld("reader-a")
	w.must(t, sprint.FleetStep(w.s, sprint.FleetReq{Op: "up", Member: "m1"}))
	w.s.Fleet.SetRows(append(w.s.Fleet.Rows(), sprint.FriendRow("amy")))
	s := refmodel.Abstract(refmodel.Observed{Snap: w.s, Machine: refmodel.Running})
	assert.Equal(t, map[string]string{"m1": refmodel.Up}, s.Members)
}

// The model computes the tier the card is on (sprint.CardTiers, docs/SPEC-SPRINT.md
// section 5). tier_now stands only while the card is not pinned. A pin, a frontier
// card, or an explicit tier is the ceiling, and that ceiling beats the brief.
// copied is a wrong tier the row refuses. Where tier_now or the brief disagrees
// with the computed tier, copied is that other value, so a model that copies the
// engine's tier instead of computing it fails the row. reads is the count that
// computed tier asks.
func TestAbstractSetsPrimaryTier(t *testing.T) {
	t.Parallel()
	pin := "model: prov/model-a\ntokens: 1000\ndeadline: 60\n"
	grade := decide.Decided{Value: decide.GradePro, P: 0.9, Op: "g-pro"}.String()
	cases := []struct {
		id, name, brief string
		extra           map[string]string
		tier            string
		reads           int
		copied          string // a tier this row refuses
	}{
		{id: "dealt", name: "tier_now after a deal", brief: "c: work\n",
			extra: map[string]string{sprint.FieldTierNow: "pro"}, tier: "pro", reads: 2, copied: "flash"},
		{id: "ceiling", name: "a pro brief before a deal is the ceiling", brief: "c: work tier: pro\n",
			tier: "pro", reads: 2, copied: "flash"},
		{id: "flash", name: "a flash brief is flash", brief: "c: work tier: flash\n",
			tier: "flash", reads: 1, copied: "pro"},
		{id: "bare", name: "a brief that names no tier is flash", brief: "c: work\n",
			tier: "flash", reads: 1, copied: "pro"},
		{id: "explicit", name: "an explicit tier with no tier_now is that tier", brief: "c: work\n",
			extra: map[string]string{sprint.FieldTier: "pro"}, tier: "pro", reads: 2, copied: "flash"},
		{id: "pin-now", name: "a pin ignores tier_now", brief: "c: work tier: flash\n" + pin,
			extra: map[string]string{sprint.FieldTierNow: "pro"}, tier: "flash", reads: 1, copied: "pro"},
		{id: "explicit-now", name: "an explicit tier ignores tier_now", brief: "c: work tier: flash\n",
			extra: map[string]string{sprint.FieldTier: "pro", sprint.FieldTierNow: "flash"}, tier: "pro", reads: 2, copied: "flash"},
		{id: "explicit-brief", name: "an explicit tier beats the brief", brief: "c: work tier: pro\n",
			extra: map[string]string{sprint.FieldTier: "flash"}, tier: "flash", reads: 1, copied: "pro"},
		{id: "frontier", name: "a frontier card ignores tier_now", brief: "c: work tier: frontier\n",
			extra: map[string]string{sprint.FieldTierNow: "flash"}, tier: "frontier", reads: 2, copied: "flash"},
		{id: "grade", name: "a pro grade does not raise a flash ceiling", brief: "c: work tier: flash\n",
			extra: map[string]string{sprint.FieldGrade: grade}, tier: "flash", reads: 1, copied: "pro"},
	}
	w := newWorld("reader-a")
	for _, tc := range cases {
		f := map[string]string{"kind": refmodel.KindPrimary, "stream": "s1", "brief": tc.brief}
		for k, v := range tc.extra {
			f[k] = v
		}
		w.s.Work.Put(&sprint.Card{ID: tc.id, Row: "s1", Col: sprint.Review, Fields: f})
	}
	s := refmodel.Abstract(refmodel.Observed{Snap: w.s, Machine: refmodel.Running})
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.tier, s.Primaries[tc.id].Tier)
			assert.NotEqual(t, tc.copied, s.Primaries[tc.id].Tier)
			assert.Equal(t, tc.reads, s.ReadsNeeded(tc.id))
		})
	}
}
