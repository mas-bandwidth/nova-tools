package config

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestARouteRunningLoopLoadsEveryProvidersKeyEnv is tla/ProviderKey.tla's
// UnitsLoadEveryEnabledKey (AddLoop): a loop whose argv runs a nova-swarm
// member or reader loads the key_env of every enabled provider row, and a loop
// that runs no provider route loads none of them. The keys the loops play
// renders into nova-secrets exec --only are the row's applied keys, so the
// derivation is what the unit's --only holds (docs/SPEC-CONFIG.md, "loop").
func TestARouteRunningLoopLoadsEveryProvidersKeyEnv(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	st := NewMem()

	_, _, err := st.Update(ctx, KindFleet, KindFleet, map[string]string{"loops_dir": "~/nova-bench/loops"}, "t")
	require.NoError(t, err)

	providerKind, ok := Lookup(KindProvider)
	require.True(t, ok)
	minimax, err := providerKind.NewRow("minimax", map[string]string{
		"billing": "metered", "funded": "true", "key_env": "MINIMAX_API_KEY",
	})
	require.NoError(t, err)
	_, err = st.Insert(ctx, KindProvider, minimax, "t")
	require.NoError(t, err)

	loopKind, ok := Lookup(KindLoop)
	require.True(t, ok)
	member, err := loopKind.NewRow("member-a", map[string]string{
		"machine": "m1",
		"argv":    `["/usr/bin/env","NOVA_SPRINT_REDIS_USER=bench","nova-swarm","member","--as","member-a"]`,
		"seat":    "bench", "keepalive": "true",
	})
	require.NoError(t, err)
	other, err := loopKind.NewRow("tick", map[string]string{
		"machine": "m1", "argv": `["nova-sprint","run"]`, "every": "5",
	})
	require.NoError(t, err)

	derived, err := loopKind.Derive(ctx, st, []Row{member, other})
	require.NoError(t, err)
	require.Len(t, derived, 2)

	// parseLoop is the inventory's reader: what the loops play hands the
	// template as item['keys'], which loop_secrets joins into --only.
	memberView, err := parseLoop("member-a", View(derived[0].Fields))
	require.NoError(t, err)
	only := "--only " + strings.Join(memberView.Keys, ",")
	assert.Contains(t, only, "MINIMAX_API_KEY",
		"a member loop's rendered --only does not load the provider row's key_env")
	assert.Contains(t, only, "--only MINIMAX_API_KEY", "the --only the play renders")

	otherView, err := parseLoop("tick", View(derived[1].Fields))
	require.NoError(t, err)
	assert.NotContains(t, strings.Join(otherView.Keys, ","), "MINIMAX_API_KEY",
		"a loop that runs no provider route loads a provider key")

	assert.Equal(t, []string{"MINIMAX_API_KEY"}, EffectiveKeys(member, []Row{minimax}))
	assert.Empty(t, EffectiveKeys(other, []Row{minimax}))
}
