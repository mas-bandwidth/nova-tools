package refmodel_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

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

func TestAbstractSetsPrimaryTier(t *testing.T) {
	t.Parallel()
	w := newWorld("reader-a")

	// 1. Card with FieldTierNow: "pro"
	c1 := &sprint.Card{ID: "c1", Row: "s1", Col: sprint.Review, Fields: map[string]string{
		"kind": refmodel.KindPrimary, "stream": "s1", sprint.FieldTierNow: "pro", "brief": "c: work\n",
	}}
	w.s.Work.Put(c1)

	// 2. Card with brief specifying tier: pro (no FieldTierNow)
	c2 := &sprint.Card{ID: "c2", Row: "s1", Col: sprint.Review, Fields: map[string]string{
		"kind": refmodel.KindPrimary, "stream": "s1", "brief": "c: work tier: pro\n",
	}}
	w.s.Work.Put(c2)

	// 3. Card with brief specifying tier: flash (no FieldTierNow)
	c3 := &sprint.Card{ID: "c3", Row: "s1", Col: sprint.Review, Fields: map[string]string{
		"kind": refmodel.KindPrimary, "stream": "s1", "brief": "c: work tier: flash\n",
	}}
	w.s.Work.Put(c3)

	// 4. Card with no tier in brief (default flash)
	c4 := &sprint.Card{ID: "c4", Row: "s1", Col: sprint.Review, Fields: map[string]string{
		"kind": refmodel.KindPrimary, "stream": "s1", "brief": "c: work\n",
	}}
	w.s.Work.Put(c4)

	// 5. Card with FieldTier: "pro" (pinned tier)
	c5 := &sprint.Card{ID: "c5", Row: "s1", Col: sprint.Review, Fields: map[string]string{
		"kind": refmodel.KindPrimary, "stream": "s1", sprint.FieldTier: "pro", "brief": "c: work\n",
	}}
	w.s.Work.Put(c5)

	s := refmodel.Abstract(refmodel.Observed{Snap: w.s, Machine: refmodel.Running})
	assert.Equal(t, "pro", s.Primaries["c1"].Tier)
	assert.Equal(t, 2, s.ReadsNeeded("c1"))

	assert.Equal(t, "pro", s.Primaries["c2"].Tier)
	assert.Equal(t, 2, s.ReadsNeeded("c2"))

	assert.Equal(t, "flash", s.Primaries["c3"].Tier)
	assert.Equal(t, 1, s.ReadsNeeded("c3"))

	assert.Equal(t, "flash", s.Primaries["c4"].Tier)
	assert.Equal(t, 1, s.ReadsNeeded("c4"))

	assert.Equal(t, "pro", s.Primaries["c5"].Tier)
	assert.Equal(t, 2, s.ReadsNeeded("c5"))
}
