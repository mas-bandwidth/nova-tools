//go:build functional

package config

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestApplyFriendRowWithNoMachineAgreesWithFriendSync is the guard for the
// 2026-10-04 refusal "redis: friend rowan-mas slots: INVALID studio 0 0": a
// friend row whose tiers include heavy, with no beat naming a machine (so
// apply charges her to the fleet's coordinator machine), is accepted by a full
// apply of every kind and lands the desired hash friend sync carries (slots
// and tiers), the same list capacity.lua's filter_ok accepts (config.Tiers).
// A tier outside that list is still refused, and the refusal names the check.
func TestApplyFriendRowWithNoMachineAgreesWithFriendSync(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	ap, c := redisApplier(t)
	st := seed(t)
	// seed's two friends already desire 64 slots on studio; raise its ceiling
	// so the third friend's 8 slots fit and the test is about her tiers.
	_, _, setupErr := st.Update(ctx, KindMachine, "studio", map[string]string{"slots": "128"}, "rowan")
	require.NoError(t, setupErr)

	friend, _ := Lookup(KindFriend)
	row, err := friend.NewRow("nova", map[string]string{"slots": "8", "tiers": "flash,heavy,pro", "roles": "builder", "streams": "security* , infra", "kinds": "fix-red,review"})
	require.NoError(t, err)
	_, err = st.Insert(ctx, KindFriend, row, "rowan")
	require.NoError(t, err)

	res := applyKinds(t, st, ap, "rowan")
	assertionMsg := []any{"full apply refused a friend whose tiers include heavy: %+v", res}
	require.Equal(t, 3, res[KindFriend].Add, assertionMsg...)

	got := c.HGetAll(ctx, "friend:nova:desired").Val()
	require.Equal(t, "8", got["slots"], "friend:nova:desired %v", got)
	require.Equal(t, "studio", got["machine"], "friend:nova:desired %v (no beat: charged to the coordinator machine)", got)
	require.Equal(t, "flash,heavy,pro", got["tiers"], "friend:nova:desired %v: friend sync carries the row's tiers", got)
	require.Equal(t, "security* , infra", got["streams"], "friend:nova:desired %v: apply carries the row's stream restriction", got)
	require.Equal(t, "fix-red,review", got["kinds"], "friend:nova:desired %v: apply carries the row's kind restriction", got)
	stored, found, err := st.Get(ctx, KindFriend, "nova")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "security* , infra", stored.Fields["streams"], "migration and Postgres round-trip keep the configured value")
	require.Equal(t, "fix-red,review", stored.Fields["kinds"], "migration and Postgres round-trip keep the configured value")

	// A row the config layer would never store still names the check that
	// refused it, never a bare "INVALID <machine> 0 0".
	bad := Row{Name: "nova", Fields: map[string]string{"slots": "8", "tiers": "ultra", "roles": "builder"}}
	err = ap.Write(ctx, KindFriend, bad, nil, "rowan", "test")
	require.Error(t, err, "an unknown tier was accepted")
	require.ErrorContains(t, err, "INVALID tiers", "the refusal does not name the check: %v", err)
}
