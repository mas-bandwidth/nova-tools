//go:build functional

package config

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// An adopt runs this release's inventory over the store the previous release
// left, before this release's migrations and apply have run: machine rows
// applied before migration 0042 (no roles field in the hash), no bench
// publisher's beat hash, and only the beat record every nova-sprint member
// writes. The inventory lists every host in the groups the previous release
// gave them (roles derived as 0042 derives them, each said in a NOTE), reads
// cores from the member's record, and lists a host with no beat at all. Once
// 0042 and apply have stored the field, the field wins.
func TestInventoryAdoptsOverAPreviousReleaseStore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ap, c := redisApplier(t)
	st := previousReleaseSeed(t)
	applyKinds(t, st, ap, "operator")

	// The previous release's store: no roles field on any applied machine
	// row, no beat hash; bench-b's member beats, bench-a is down.
	for _, m := range []string{"bench-a", "bench-b"} {
		require.NoError(t, c.HDel(ctx, MachineKey(m), "roles").Err())
		require.Zero(t, c.Exists(ctx, BeatKey(m)).Val())
	}
	require.NoError(t, c.Set(ctx, MemberBeatKey("bench-b"), `{"at":"2026-10-13T12:00:00Z","load":3.5,"cores":16,"how":"procstat","meter":{}}`, 0).Err())

	snap, err := ap.Snapshot(ctx)
	require.NoError(t, err)
	for _, m := range []string{"bench-a", "bench-b"} {
		require.NotContains(t, snap.Machines[m], "roles", "%s: the store must be the previous release's shape", m)
	}
	inv, err := BuildInventory(snap, "")
	require.NoError(t, err, "the inventory refused the previous release's store: the adopt cannot start")

	// The groups the previous release's inventory gave this store.
	assert.Equal(t, []string{"bench-a", "bench-b"}, inv.All.Hosts)
	assert.ElementsMatch(t, []string{"bench-a", "bench-b"}, inv.Adopt.Hosts)
	assert.Equal(t, []string{"bench-a", "bench-b"}, inv.Benches.Hosts)
	assert.Equal(t, []string{"bench-a"}, inv.Coordinator.Hosts)
	assert.Equal(t, []string{"bench-a"}, inv.StoreDeployer.Hosts)
	assert.Empty(t, inv.Store.Hosts, "the fleet names no store")
	assert.Equal(t, []string{"bench-a"}, inv.Runners.Hosts)
	assert.Equal(t, "bench,coordination", inv.Meta.Hostvars["bench-a"]["registry_roles"])
	assert.Equal(t, "bench", inv.Meta.Hostvars["bench-b"]["registry_roles"])

	// cores from the member's beat record; no os or arch anywhere, so the
	// play's facts give them.
	assert.Equal(t, 16, inv.Meta.Hostvars["bench-b"]["cores"])
	assert.Equal(t, 8, inv.Meta.Hostvars["bench-b"]["slots_share"], "an unset width is half the beat cores")
	assert.NotContains(t, inv.Meta.Hostvars["bench-b"], "nova_os")
	assert.NotContains(t, inv.Meta.Hostvars["bench-a"], "cores")
	assert.Equal(t, []string{
		"machine bench-a: the applied row has no roles field (applied before migration 0042); derived bench,coordination as 0042 does (bench; coordination from the fleet coordinator; services from the fleet store); to store it: nova-config apply --kind machine",
		"machine bench-a: no beat (the machine is down, or its member has never beaten); listed from its row; the play gathers os, arch and cores from its facts",
		"machine bench-b: the applied row has no roles field (applied before migration 0042); derived bench as 0042 does (bench; coordination from the fleet coordinator; services from the fleet store); to store it: nova-config apply --kind machine",
		"machine bench-b: its beat has no os, arch; listed with the facts it has; the play gathers the rest from its facts",
	}, inv.Notes)

	// After 0042 and apply the field is stored, and it wins over the
	// derivation. A configured publisher's hash gives os and arch, but its
	// ncpu never overrides the member's live cores.
	_, _, err = st.Update(ctx, KindMachine, "bench-a", map[string]string{"roles": "bench,coordination,runner"}, "operator")
	require.NoError(t, err)
	applyKinds(t, st, ap, "operator")
	require.NoError(t, c.HSet(ctx, BeatKey("bench-b"), "os", "linux", "arch", "amd64", "ncpu", "12").Err())
	snap, err = ap.Snapshot(ctx)
	require.NoError(t, err)
	assert.Equal(t, "bench,coordination,runner", snap.Machines["bench-a"]["roles"])
	assert.Equal(t, "bench", snap.Machines["bench-b"]["roles"], "apply wrote back the field the previous release's row lacked")
	inv, err = BuildInventory(snap, "")
	require.NoError(t, err)
	assert.Equal(t, "bench,coordination,runner", inv.Meta.Hostvars["bench-a"]["registry_roles"])
	assert.Equal(t, []string{"bench-a", "bench-b"}, inv.Benches.Hosts)
	assert.Equal(t, []string{"bench-a"}, inv.Coordinator.Hosts)
	assert.Equal(t, 16, inv.Meta.Hostvars["bench-b"]["cores"], "the member's record is the source of truth; the hash's ncpu is only a fallback")
	assert.Equal(t, "linux", inv.Meta.Hostvars["bench-b"]["nova_os"])
	assert.Equal(t, []string{
		"machine bench-a: no beat (the machine is down, or its member has never beaten); listed from its row; the play gathers os, arch and cores from its facts",
	}, inv.Notes)
}

// mergeBeat reads each fact from one record: cores and time from the
// member's record (the source of truth), the bench hash for them only where
// the member's record has none, os and arch only from the hash; an unreadable
// member record is no record.
func TestMemberBeatWinsAndTheHashIsOnlyAFallback(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ap, c := redisApplier(t)
	require.NoError(t, c.Set(ctx, MemberBeatKey("m1"), `{"at":"2026-10-13T12:00:00Z","cores":32}`, 0).Err())
	require.NoError(t, c.Set(ctx, MemberBeatKey("m2"), `{"at":"2026-10-13T12:00:00Z","cores":32}`, 0).Err())
	require.NoError(t, c.HSet(ctx, BeatKey("m2"), "os", "darwin", "ncpu", "10", "at", "1790000000000").Err())
	require.NoError(t, c.Set(ctx, MemberBeatKey("m3"), `not json`, 0).Err())
	require.NoError(t, c.HSet(ctx, BeatKey("m5"), "os", "linux", "ncpu", "6", "at", "1790000000000").Err())
	beats, err := ap.Beats(ctx, []string{"m1", "m2", "m3", "m4", "m5"})
	require.NoError(t, err)
	assert.Equal(t, &Beat{Cores: "32", At: "2026-10-13T12:00:00Z"}, beats["m1"])
	assert.Equal(t, &Beat{OS: "darwin", Cores: "32", At: "2026-10-13T12:00:00Z"}, beats["m2"], "a stale hash never overrides the live member record")
	assert.NotContains(t, beats, "m3")
	assert.NotContains(t, beats, "m4")
	assert.Equal(t, &Beat{OS: "linux", Cores: "6", At: "2026-09-21T14:13:20Z"}, beats["m5"], "the hash is the fallback where no member record is")
}

// previousReleaseSeed is the configuration store the adopt test applies,
// with neutral names: bench-a (one runner, the fleet coordinator) and
// bench-b; the fleet names no store.
func previousReleaseSeed(t *testing.T) *Mem {
	t.Helper()
	ctx := context.Background()
	st := NewMem()
	machine, _ := Lookup(KindMachine)
	for name, raw := range map[string]map[string]string{
		"bench-a": {"user": "nova", "seat": "seat-a", "slots": "64", "runners": "1"},
		"bench-b": {"user": "nova", "seat": "seat-b", "slots": "64"},
	} {
		row, err := machine.NewRow(name, raw)
		require.NoError(t, err)
		_, err = st.Insert(ctx, KindMachine, row, "operator")
		require.NoError(t, err)
	}
	_, _, err := st.Update(ctx, KindFleet, KindFleet, map[string]string{"coordinator": "bench-a", "redis_port": "6380", "pg_dsn": "postgres://nova_config@localhost:5432/nova"}, "operator")
	require.NoError(t, err)
	return st
}
