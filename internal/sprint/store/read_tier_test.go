package store

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// A read tier raises a card's reads and never lowers them (nova-tools#5096 item 27;
// route.go readTierOf, settings.go): `stream set <s> --read-tier pro` puts every read
// of the stream on pro, `set --read-tier pro` every read of the sprint, a stream's
// setting over the sprint's, and a pro card's reads stay on pro under a flash
// setting. The read card records the tier its route was drawn from and its packet
// hands it on, so the reader's JOB.md names it; with no setting a read is on its
// card's tier, as before. A setting raises the route and never the count: a flash
// card is read once, on whatever tier, and a pro card twice (ReadsNeeded; the owner,
// 2026-10-02, cost rule 4: "one cold read per flash card on a flash route; two per
// pro card").
func TestAReadTierSettingRaisesTheReadsAndNeverLowersThem(t *testing.T) {
	t.Parallel()
	routes := []sprint.Route{route("pro-a", "pro"), route("pro-b", "pro"), route("flash-a", "flash"), route("flash-b", "flash")}
	tierOf := func(name string) string {
		for _, r := range routes {
			if r.Name == name {
				return r.Tier
			}
		}
		return "none"
	}
	// readsOf is the tiers of the routes the primary's reads drew, the tiers they
	// record, and the tiers their packets carry
	readsOf := func(h *harness, id string) (drawn, recorded, packed []string) {
		h.t.Helper()
		s := h.snap()
		reads := s.Readers.Of(id)
		require.Len(h.t, reads, sprint.ReadsNeeded(s.Work.Card(id)), "%s asked of as many readers as its tier needs", id)
		for _, rc := range reads {
			drawn = append(drawn, tierOf(rc.F(sprint.FieldRoute)))
			recorded = append(recorded, rc.F(sprint.FieldTier))
			packed = append(packed, sprint.PacketOf("", 0, rc, s.Work.Card(id), nil, nil).Tier)
		}
		return drawn, recorded, packed
	}
	set := func(h *harness, r sprint.SetReq) {
		h.t.Helper()
		r.Who = h.st.Actor
		h.must(SetStep(r))
	}
	// read runs the cards of three streams to review and asks their readers:
	// s1 and s2 flash cards, s3 a pro card working on pro
	read := func(h *harness) {
		h.t.Helper()
		h.addReady("s1", 1, briefOf("flash", ""))
		h.addReady("s2", 1, briefOf("flash", ""))
		h.addReady("s3", 1, briefOf("pro", ""))
		h.setPrimary("s3-1", map[string]string{sprint.FieldTierNow: "pro"}) // a pro card on pro: escalated (flash first)
		h.startMachine()
		h.machine()
		h.work("m1")
		h.work("m2")
		h.machine()
	}
	// reads is the tier once for each read the card's own tier needs: s3, the pro
	// card, is read twice, the flash cards once, whatever the setting
	reads := func(id, tier string) []string {
		if id == "s3-1" {
			return []string{tier, tier}
		}
		return []string{tier}
	}
	t.Run("a stream's read tier", func(t *testing.T) {
		t.Parallel()
		h := routeHarness(t, routes...)
		h.addReady("s1", 1, briefOf("flash", ""))
		h.addReady("s3", 1, briefOf("pro", ""))
		h.setPrimary("s3-1", map[string]string{sprint.FieldTierNow: "pro"}) // a pro card on pro: escalated (flash first)
		set(h, sprint.SetReq{Streams: []string{"s1"}, ReadTier: "pro"})
		set(h, sprint.SetReq{Streams: []string{"s3"}, ReadTier: "flash"})
		h.addReady("s2", 1, briefOf("flash", ""))
		h.startMachine()
		h.machine()
		h.work("m1")
		h.work("m2")
		h.machine()
		for id, want := range map[string]string{"s1-1": "pro", "s2-1": "flash", "s3-1": "pro"} {
			drawn, recorded, packed := readsOf(h, id)
			assert.Equal(t, reads(id, want), drawn, "%s's reads drew %s routes", id, want)
			assert.Equal(t, reads(id, want), recorded, "%s's read cards record their tier", id)
			assert.Equal(t, reads(id, want), packed, "%s's read packets hand the reader its tier", id)
		}
		h.clean("a stream's read tier")
	})
	t.Run("the sprint's read tier, a stream's over it", func(t *testing.T) {
		t.Parallel()
		h := routeHarness(t, routes...)
		h.addReady("s1", 1, briefOf("flash", ""))
		set(h, sprint.SetReq{ReadTier: "pro"})
		set(h, sprint.SetReq{Streams: []string{"s1"}, ReadTier: "flash"})
		read(h)
		for id, want := range map[string]string{"s1-1": "flash", "s2-1": "pro", "s3-1": "pro"} {
			drawn, recorded, _ := readsOf(h, id)
			assert.Equal(t, reads(id, want), drawn, "%s's reads drew %s routes", id, want)
			assert.Equal(t, reads(id, want), recorded, "%s's read cards record their tier", id)
		}
		h.clean("the sprint's read tier")
	})
	t.Run("default takes a setting off", func(t *testing.T) {
		t.Parallel()
		h := routeHarness(t, routes...)
		h.addReady("s1", 1, briefOf("flash", ""))
		set(h, sprint.SetReq{ReadTier: "pro"})
		set(h, sprint.SetReq{Streams: []string{"s1"}, ReadTier: "pro"})
		set(h, sprint.SetReq{ReadTier: sprint.ReadTierDefault})
		set(h, sprint.SetReq{Streams: []string{"s1"}, ReadTier: sprint.ReadTierDefault})
		read(h)
		for id, want := range map[string]string{"s1-1": "flash", "s2-1": "flash", "s3-1": "pro"} {
			drawn, _, _ := readsOf(h, id)
			assert.Equal(t, reads(id, want), drawn, "%s's reads drew %s routes", id, want)
		}
	})
	t.Run("refused, writing nothing", func(t *testing.T) {
		t.Parallel()
		h := routeHarness(t, routes...)
		h.addReady("s1", 1, briefOf("flash", ""))
		for _, r := range []sprint.SetReq{
			{ReadTier: "frontier", Who: h.st.Actor},
			{ReadTier: "fast", Who: h.st.Actor},
			{Streams: []string{"nope"}, ReadTier: "pro", Who: h.st.Actor},
			{Streams: []string{"s1"}, DealtMax: "1h", Who: h.st.Actor},
			{DealtMax: "-1h", Who: h.st.Actor},
			{Who: h.st.Actor},
			{ReadTier: "pro", Who: "m1"},
		} {
			res := h.run(SetStep(r))
			require.Len(t, res.Refused, 1, "%+v is refused", r)
		}
		s := h.snap()
		_, had := s.Work.Prop(sprint.PropReadTier)
		assert.False(t, had, "no read tier written")
		assert.Empty(t, s.StreamCtl("s1").F(sprint.FieldReadTier), "no stream read tier written")
		assert.True(t, strings.Contains(h.run(SetStep(sprint.SetReq{ReadTier: "pro", Who: "m1"})).Refused[0].Why, "coordinator"), "a worker is refused as not the coordinator")
	})
}
