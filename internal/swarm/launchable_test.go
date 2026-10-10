package swarm_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// The defect of 2026-10-06: a heavy route under the claude harness (provider
// subscription-claude) was added, and four fleet members (m1 to m4 here), which hold no
// claude login, drew it for work and for reads; 73 attempts ended in one second "launch
// refused". A member draws only routes whose harness its machine row lists (opencode only
// by default), the deal and the ask skip a member that serves none of the tier's routes,
// and a route no member up can launch is one judgment naming the route and the machines.
func TestAMemberNeverDrawsARouteWhoseHarnessItLacks(t *testing.T) {
	t.Parallel()
	t.Run("actual deal", func(t *testing.T) {
		s := &sprint.Snapshot{Now: time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC), Work: sprint.NewTable(sprint.Work), Fleet: sprint.NewTable(sprint.Fleet), Readers: sprint.NewTable(sprint.Readers), Merge: sprint.NewTable(sprint.Merge)}
		s.Fleet.SetRows([]string{"m1"})
		s.Fleet.Put(&sprint.Card{ID: sprint.CtlID("m1"), Row: "m1", Col: sprint.Ctl, Rev: 1, Fields: map[string]string{"kind": "member", "status": sprint.Up, "width": "2"}})
		s.Work.SetRows([]string{"s1"})
		s.Work.Put(&sprint.Card{ID: "s1-1", Row: "s1", Col: sprint.Ready, Rev: 1, Fields: map[string]string{"kind": "primary", "brief": "tier: heavy\n", sprint.FieldTier: "heavy"}})
		s.Routes = []sprint.Route{{Name: "sub", Tier: "heavy", Provider: "subscription-claude", Model: "model", Harness: "claude", Enabled: true, First: true}, {Name: "api", Tier: "heavy", Provider: "p", Model: "model", Enabled: true}}
		p := sprint.Deal(s, sprint.DealReq{Sel: sprint.Sel{IDs: []string{"s1-1"}}})
		require.Empty(t, p.Refused)
		var route string
		for _, u := range p.Units {
			for _, c := range u.Changes {
				if c.Table == sprint.Fleet && c.Entry.ID == "s1-1.w1" {
					route = c.Entry.Set[sprint.FieldRoute]
				}
			}
		}
		require.Equal(t, "api", route, "the dealer must skip the subscription route this default machine cannot launch")
	})
	ctx := context.Background()
	st := config.NewMem()
	machine := func(name, harnesses string) {
		f := map[string]string{"user": "u", "seat": "s", "slots": "16", "runners": "0"}
		if harnesses != "" {
			f["harnesses"] = harnesses
		}
		_, err := st.Insert(ctx, config.KindMachine, config.Row{Name: name, Fields: f}, "t")
		require.NoError(t, err, "machine %s", name)
	}
	for _, m := range []string{"m1", "m2", "m3", "m4"} {
		machine(m, "") // the default: no headless login
	}
	machine("keeper", "opencode,claude")
	rows, err := config.Harnesses(ctx, st)
	require.NoError(t, err)
	require.Equal(t, []string{"opencode"}, rows["m1"], "a machine row with no harnesses lists opencode only")
	require.Equal(t, []string{"opencode", "claude"}, rows["keeper"])

	heavy := []swarm.LaunchRoute{{Name: "heavy-claude", Harness: "claude"}, {Name: "heavy-or", Harness: ""}}
	claudeOnly := heavy[:1]
	for _, m := range []string{"m1", "m2", "m3", "m4"} {
		require.False(t, swarm.CanLaunch(rows[m], "claude"), "%s holds no claude login and must not launch a claude route", m)
		require.Equal(t, []swarm.LaunchRoute{{Name: "heavy-or"}}, swarm.Launchable(heavy, rows[m]), "%s draws only the opencode route of the tier", m)
		require.False(t, swarm.Serves(rows[m], claudeOnly), "%s serves no tier whose only route is claude's", m)
	}
	require.Equal(t, heavy, swarm.Launchable(heavy, rows["keeper"]), "keeper lists claude and draws both")

	// the deal and the ask: only keeper serves a tier whose only route is claude's
	require.Equal(t, []string{"keeper"}, swarm.MembersServing(rows, claudeOnly))
	require.Len(t, swarm.MembersServing(rows, heavy), 5, "every member serves a tier with an opencode route")
	require.Len(t, swarm.MembersServing(rows, nil), 5, "a tier with no route: every member runs its own model")
	require.Empty(t, swarm.Unserved(heavy, rows), "keeper is up: every route has a member")

	// keeper down: the claude route is one judgment naming it and the machines, not one failure per card
	delete(rows, "keeper")
	require.Empty(t, swarm.MembersServing(rows, claudeOnly))
	un := swarm.Unserved(heavy, rows)
	require.Equal(t, []swarm.UnservedRoute{{Route: "heavy-claude", Harness: "claude", Machines: []string{"m1", "m2", "m3", "m4"}}}, un)
	j := un[0].Judgment()
	for _, want := range []string{"heavy-claude", "claude", "m1, m2, m3, m4", "--harnesses opencode,claude"} {
		require.True(t, strings.Contains(j, want), "judgment %q names %q", j, want)
	}
	require.Contains(t, swarm.Unserved(heavy, nil)[0].Judgment(), "no member is up")

	// the tick: one persistent judgment per enabled route no member up can launch,
	// naming the route and its machines, never one per card (docs/SPEC-SWARM).
	t.Run("the tick raises one route judgment", func(t *testing.T) {
		s := &sprint.Snapshot{Now: time.Date(2030, 1, 2, 3, 4, 5, 0, time.UTC), Work: sprint.NewTable(sprint.Work), Fleet: sprint.NewTable(sprint.Fleet), Readers: sprint.NewTable(sprint.Readers), Merge: sprint.NewTable(sprint.Merge)}
		s.Work.SetRows([]string{"s1"})
		s.Fleet.SetRows([]string{"m1"})
		s.Fleet.Put(&sprint.Card{ID: sprint.CtlID("m1"), Row: "m1", Col: sprint.Ctl, Rev: 1, Fields: map[string]string{"kind": "member", "status": sprint.Up, "width": "2"}})
		s.Routes = []sprint.Route{{Name: "heavy-claude", Tier: "heavy", Provider: "subscription-claude", Model: "model", Harness: "claude", Enabled: true, First: true}, {Name: "heavy-or", Tier: "heavy", Provider: "p", Model: "model", Enabled: true}}
		p, _ := sprint.TickDeal(s, sprint.TickReq{})
		var what []string
		for _, n := range p.Notes {
			require.NotEqual(t, sprint.RouteSubject("heavy-or"), n.Stream, "the opencode route a member up can launch raises nothing: %+v", n)
			if n.Type == sprint.NNoRoute && n.Stream == sprint.RouteSubject("heavy-claude") {
				what = append(what, n.What)
			}
		}
		require.Len(t, what, 1, "one judgment for the route, not one per card: %+v", p.Notes)
		for _, want := range []string{"heavy-claude", "claude", "m1"} {
			require.Contains(t, what[0], want, "judgment %q names %q", what[0], want)
		}
	})
}
