package sprint

import (
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestRouteDrawAndSyncRespectMachineHarnesses(t *testing.T) {
	t.Parallel()
	for _, h := range []string{"claude", "codex", "grok"} {
		t.Run(h, func(t *testing.T) {
			w := fleetWorld(t, 1, 2, "m1", "m2")
			w.s.Routes = []Route{{Name: "sub", Tier: "flash", Harness: h, Provider: "subscription-" + h, Model: "m", Enabled: true, First: true}, {Name: "api", Tier: "flash", Provider: "p", Model: "m", Enabled: true}}
			w.must(FleetStep(w.s, FleetReq{Op: "sync", Sync: []SyncMember{{"m1", 2}, {"m2", 2}}, Harnesses: map[string]string{"m1": "opencode", "m2": "opencode," + h}, Machines: []string{"m1", "m2"}}))
			assert.Equal(t, "opencode,"+h, w.s.MemberCtl("m2").F(FieldHarnesses))
			assert.Empty(t, HarnessDrift(w.s, map[string]string{"m1": "opencode", "m2": "opencode," + h}))
			c := w.s.Work.Card("s1-1")
			for _, m := range []string{"m1", "m2"} {
				set, _, why, _ := w.s.routeOf(c, nil, nil, m)
				require.Empty(t, why)
				want := "api"
				if m == "m2" {
					want = "sub"
				}
				assert.Equal(t, want, set[FieldRoute])
			}
			w.s.Routes = w.s.Routes[:1]
			assert.Equal(t, []string{"m2"}, w.s.membersForRoute(c, []string{"m1", "m2"}))
			assert.False(t, w.s.readerCanLaunch("reader-m1", "flash"))
			assert.True(t, w.s.readerCanLaunch("reader-m2", "flash"))
			set := w.s.readRouteOf(routeIndexesOf(w.s), c, nil, "reader-m2")
			assert.Equal(t, "sub", set[FieldRoute])
			w.s.Readers.SetRows([]string{"reader-m1", "reader-m2"})
			w.place(w.s.Work, c.ID, "s1", Review)
			c.Fields["attempt"] = "1"
			c.Fields["head"] = "0123456789012345678901234567890123456789"
			p := Ask(w.s, AskReq{Sel: Sel{IDs: []string{c.ID}}})
			require.Empty(t, p.Refused)
			var rd string
			for _, u := range p.Units {
				for _, ch := range u.Changes {
					if ch.Table == Readers && ch.Entry.Create != nil {
						rd = ch.Entry.Create.Row
						assert.Equal(t, "sub", ch.Entry.Set[FieldRoute])
					}
				}
			}
			assert.Equal(t, "reader-m2", rd, "ask skips the incapable reader")
		})
	}
}

func TestQueuedSubscriptionRoutesStayOnCapableMachines(t *testing.T) {
	t.Parallel()
	t.Run("down skips an incapable destination", func(t *testing.T) {
		w := fleetWorld(t, 1, 2, "m1", "m2", "m3")
		w.s.MemberCtl("m1").Fields[FieldHarnesses] = "opencode,claude"
		w.s.MemberCtl("m3").Fields[FieldHarnesses] = "opencode,claude"
		putWorkCard(w, "s1-1", "m1", Ready, 0, map[string]string{FieldRoute: "sub", FieldHarness: "claude"})
		w.must(FleetStep(w.s, FleetReq{Op: "down", Member: "m1", Live: []string{"m2", "m3"}}))
		assert.Equal(t, "m3", w.s.Fleet.Card("s1-1.w1").Row)
	})
	t.Run("sync removal uses the incoming capabilities", func(t *testing.T) {
		w := fleetWorld(t, 1, 2, "m1", "m2")
		for _, m := range []string{"m1", "m2"} {
			w.s.MemberCtl(m).Fields[FieldHarnesses] = "opencode,claude"
		}
		putWorkCard(w, "s1-1", "m1", Ready, 0, map[string]string{FieldRoute: "sub", FieldHarness: "claude"})
		w.must(FleetStep(w.s, FleetReq{Op: "sync", Sync: []SyncMember{{"m2", 2}}, Machines: []string{"m2"}, Harnesses: map[string]string{"m2": "opencode"}}))
		assert.Equal(t, Withdrawn, w.s.Fleet.Card("s1-1.w1").Col)
		assert.Equal(t, "opencode", w.s.MemberCtl("m2").F(FieldHarnesses))
	})
}
