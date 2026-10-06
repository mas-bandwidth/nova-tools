package swarm

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/config"
)

// The defect of 2026-10-06: a heavy route under the claude harness (provider
// subscription-claude) was added, and four fleet members (m1 to m4 here), which hold no
// claude login, drew it for work and for reads; 73 attempts ended in one second "launch
// refused". A member draws only routes whose harness its machine row lists (opencode only
// by default), the deal and the ask skip a member that serves none of the tier's routes,
// and a route no member up can launch is one judgment naming the route and the machines.
func TestAMemberNeverDrawsARouteWhoseHarnessItLacks(t *testing.T) {
	t.Parallel()
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

	heavy := []LaunchRoute{{Name: "heavy-claude", Harness: "claude"}, {Name: "heavy-or", Harness: ""}}
	claudeOnly := heavy[:1]
	for _, m := range []string{"m1", "m2", "m3", "m4"} {
		require.False(t, CanLaunch(rows[m], "claude"), "%s holds no claude login and must not launch a claude route", m)
		require.Equal(t, []LaunchRoute{{Name: "heavy-or"}}, Launchable(heavy, rows[m]), "%s draws only the opencode route of the tier", m)
		require.False(t, Serves(rows[m], claudeOnly), "%s serves no tier whose only route is claude's", m)
	}
	require.Equal(t, heavy, Launchable(heavy, rows["keeper"]), "keeper lists claude and draws both")

	// the deal and the ask: only keeper serves a tier whose only route is claude's
	require.Equal(t, []string{"keeper"}, MembersServing(rows, claudeOnly))
	require.Len(t, MembersServing(rows, heavy), 5, "every member serves a tier with an opencode route")
	require.Len(t, MembersServing(rows, nil), 5, "a tier with no route: every member runs its own model")
	require.Empty(t, Unserved(heavy, rows), "keeper is up: every route has a member")

	// keeper down: the claude route is one judgment naming it and the machines, not one failure per card
	delete(rows, "keeper")
	require.Empty(t, MembersServing(rows, claudeOnly))
	un := Unserved(heavy, rows)
	require.Equal(t, []UnservedRoute{{Route: "heavy-claude", Harness: "claude", Machines: []string{"m1", "m2", "m3", "m4"}}}, un)
	j := un[0].Judgment()
	for _, want := range []string{"heavy-claude", "claude", "m1, m2, m3, m4", "--harnesses opencode,claude"} {
		require.True(t, strings.Contains(j, want), "judgment %q names %q", j, want)
	}
	require.Contains(t, Unserved(heavy, nil)[0].Judgment(), "no member is up")
}
