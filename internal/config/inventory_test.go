package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// loopView is a loop's Redis view as the loop kind's apply writes it, with
// the fields a case overrides.
func loopView(name, machine string, over map[string]string) View {
	v := View{
		"name": name, "machine": machine, "argv": `["nova-swarm","member","--width","2"]`,
		"seat": "seat-a", "keys": "API_KEY,BENCH_PASSWORD", "every": "0", "keepalive": "true",
		"width": "2", "enabled": "true", "log": "~/nova-bench/loops/" + name + ".log",
	}
	for k, x := range over {
		v[k] = x
	}
	return v
}

// snapshot is two machines, the fleet row naming both, a beat for one and
// the loops the case gives (nil: the loop kind never applied).
func snapshot(loops map[string]View) *Snapshot {
	s := &Snapshot{
		Machines: map[string]View{
			"bench-alpha": {"user": "user-a", "seat": "seat-a", "slots": "64", "runners": "1"},
			"bench-beta":  {"user": "user-b", "seat": "seat-b", "slots": "40", "runners": "0"},
		},
		Fleet: View{"store": "bench-beta", "coordinator": "bench-alpha", "redis_port": "6380", "pg_dsn": "postgres://nova_config@localhost:5432/nova"},
		Loops: loops,
		Beats: map[string]*Beat{"bench-beta": {OS: "linux", Arch: "amd64"}},
		Revs:  map[string]int64{KindMachine: 3, KindFleet: 2},
	}
	if loops != nil {
		s.Revs[KindLoop] = 4
	}
	return s
}

// The inventory's groups, host variables and all.vars from one snapshot: the
// fleet row decides coordinator, store and store_deployer, the beat the
// platform, and the loop views each machine's nova_loops, typed.
func TestBuildInventoryFromTheAppliedState(t *testing.T) {
	t.Parallel()
	inv, err := BuildInventory(snapshot(map[string]View{
		"member-beta": loopView("member-beta", "bench-beta", nil),
		"tick":        loopView("tick", "bench-alpha", map[string]string{"argv": `["nova-sprint","run"]`, "seat": "", "keys": "", "every": "5", "keepalive": "false", "width": "0", "enabled": "false"}),
	}), "bench-alpha")
	require.NoError(t, err)

	assert.Equal(t, []string{"bench-alpha", "bench-beta"}, inv.All.Hosts)
	assert.Equal(t, []string{"bench-alpha", "bench-beta"}, inv.Benches.Hosts)
	assert.Equal(t, []string{"bench-alpha"}, inv.Coordinator.Hosts)
	assert.Equal(t, []string{"bench-alpha"}, inv.StoreDeployer.Hosts)
	assert.Equal(t, []string{"bench-beta"}, inv.Store.Hosts)
	assert.Equal(t, []string{"bench-alpha"}, inv.Runners.Hosts)
	assert.Equal(t, "bench-beta", inv.All.Vars["nova_store"])
	assert.Equal(t, map[string]int64{KindMachine: 3, KindFleet: 2, KindLoop: 4}, inv.All.Vars["nova_config_rev"])

	alpha, beta := inv.Meta.Hostvars["bench-alpha"], inv.Meta.Hostvars["bench-beta"]
	assert.Equal(t, map[string]any{
		"ansible_host": "bench-alpha", "ansible_user": "user-a", "nova_seat": "seat-a", "slots": 64, "runners": 1,
		"kind": "machine", "ansible_connection": "local", "nova_redis_port": 6380, "nova_redis_addr": "bench-beta:6380", "nova_pg_dsn": "postgres://nova_config@localhost:5432/nova",
		"nova_loops": []InventoryLoop{{Name: "tick", Argv: []string{"nova-sprint", "run"}, Keys: []string{}, Every: 5, Log: "~/nova-bench/loops/tick.log"}},
	}, alpha)
	assert.Equal(t, "linux", beta["nova_os"])
	assert.Equal(t, "amd64", beta["nova_arch"])
	assert.Equal(t, "bench-beta:6380", beta["nova_redis_addr"])
	assert.Equal(t, "postgres://nova_config@localhost:5432/nova", beta["nova_pg_dsn"], "the explicit localhost DSN is preserved")
	assert.NotContains(t, beta, "ansible_connection")
	assert.NotContains(t, alpha, "nova_os", "a machine with no beat carries no platform: the plays gather it")
	assert.Equal(t, []InventoryLoop{{
		Name: "member-beta", Argv: []string{"nova-swarm", "member", "--width", "2"}, Seat: "seat-a",
		Keys: []string{"API_KEY", "BENCH_PASSWORD"}, Keepalive: true, Width: 2, Enabled: true,
		Log: "~/nova-bench/loops/member-beta.log",
	}}, beta["nova_loops"])
}

func TestMemberEndpointComesFromTheFleetAndNotItsPersistedArgv(t *testing.T) {
	t.Parallel()
	s := snapshot(map[string]View{
		"member-beta": loopView("member-beta", "bench-beta", map[string]string{"argv": `["/usr/bin/env","NOVA_SPRINT_REDIS=old-store:6379","NOVA_SPRINT_REDIS_USER=bench","NOVA_SPRINT_REDIS_PASSWORD_ENV=NOVA_REDIS_BENCH_PASSWORD","~/.local/bin/nova-swarm","member","--reader"]`}),
		"other":       loopView("other", "bench-alpha", map[string]string{"argv": `["/usr/bin/env","NOVA_SPRINT_REDIS=keep-me:6379","~/bin/tick"]`}),
	})
	inv, err := BuildInventory(s, "")
	require.NoError(t, err)
	member := inv.Meta.Hostvars["bench-beta"]["nova_loops"].([]InventoryLoop)[0]
	assert.Equal(t, []string{"/usr/bin/env", "NOVA_SPRINT_REDIS_USER=bench", "NOVA_SPRINT_REDIS_PASSWORD_ENV=NOVA_REDIS_BENCH_PASSWORD", "~/.local/bin/nova-swarm", "member", "--reader"}, member.Argv)
	other := inv.Meta.Hostvars["bench-alpha"]["nova_loops"].([]InventoryLoop)[0]
	assert.Contains(t, other.Argv, "NOVA_SPRINT_REDIS=keep-me:6379", "a non-member command stays word-for-word")
	for _, host := range inv.All.Hosts {
		assert.Equal(t, "bench-beta:6380", inv.Meta.Hostvars[host]["nova_redis_addr"], host)
	}
}

func TestInventoryRefusesMissingEndpointsBeforeRewritingALegacyMember(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"redis_port", "pg_dsn"} {
		t.Run(field, func(t *testing.T) {
			t.Parallel()
			const argv = `["/usr/bin/env","NOVA_SPRINT_REDIS=bench-beta:6380","nova-swarm","member"]`
			s := snapshot(map[string]View{"member-beta": loopView("member-beta", "bench-beta", map[string]string{"argv": argv})})
			delete(s.Fleet, field)
			inv, err := BuildInventory(s, "")
			require.ErrorContains(t, err, "endpoints are unset: "+field)
			assert.Contains(t, err.Error(), "nova-config fleet set --redis_port <port> --pg_dsn <dsn>")
			assert.Nil(t, inv)
			assert.Equal(t, argv, s.Loops["member-beta"]["argv"])
		})
	}
}

// nova_loops says what the store says: absent when the loop kind was never
// applied (a play must not read that as "run nothing" and retire every
// unit), an empty list on every machine when it was applied with no rows.
func TestNovaLoopsIsAbsentUntilTheLoopKindIsApplied(t *testing.T) {
	t.Parallel()
	never, err := BuildInventory(snapshot(nil), "")
	require.NoError(t, err)
	none, err := BuildInventory(snapshot(map[string]View{}), "")
	require.NoError(t, err)
	for _, m := range []string{"bench-alpha", "bench-beta"} {
		assert.NotContains(t, never.Meta.Hostvars[m], "nova_loops", m)
		assert.Equal(t, []InventoryLoop{}, none.Meta.Hostvars[m]["nova_loops"], m)
	}
	raw, err := none.JSON()
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"nova_loops": []`)
}

// A loop view the plays cannot render a unit from is refused by name, with
// the command that shows it; nothing is guessed.
func TestALoopViewThatDoesNotParseIsRefused(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		over map[string]string
		want string
	}{
		{"argv not JSON", map[string]string{"argv": "nova-swarm member"}, `argv "nova-swarm member" is not a JSON list`},
		{"argv empty", map[string]string{"argv": "[]"}, "is not a JSON list of at least one string"},
		{"keys without a seat", map[string]string{"seat": ""}, "names secrets and the loop has no seat"},
		{"every and keepalive", map[string]string{"every": "30"}, "a loop runs every n seconds or is kept alive, exactly one"},
		{"neither", map[string]string{"keepalive": "false"}, "exactly one"},
		{"every negative", map[string]string{"every": "-1", "keepalive": "false"}, "is not a count of seconds"},
		{"keepalive word", map[string]string{"keepalive": "yes please"}, "is not true or false"},
		{"enabled word", map[string]string{"enabled": "maybe"}, "is not true or false"},
		{"no log", map[string]string{"log": ""}, "log \"\" is empty"},
		{"no machine row", map[string]string{"machine": "bench-gone"}, `names machine "bench-gone", which has no machine row`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := BuildInventory(snapshot(map[string]View{"one": loopView("one", "bench-beta", tc.over)}), "")
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
			assert.Contains(t, err.Error(), "loop")
		})
	}
}

// An empty login or seat is left out, never emitted as "".
func TestBuildInventoryOmitsEmptyValues(t *testing.T) {
	t.Parallel()
	s := snapshot(nil)
	s.Machines["bench-empty"] = View{"user": "", "seat": "", "slots": "2", "runners": "0"}
	inv, err := BuildInventory(s, "")
	require.NoError(t, err)
	hv := inv.Meta.Hostvars["bench-empty"]
	for _, k := range []string{"ansible_user", "nova_seat", "user", "seat"} {
		assert.NotContains(t, hv, k)
	}
	assert.Equal(t, 2, hv["slots"])
}

// A fleet with declared endpoints and no machines has every group present.
func TestBuildInventoryOfAnEmptyStore(t *testing.T) {
	t.Parallel()
	inv, err := BuildInventory(&Snapshot{Fleet: View{"redis_port": "6380", "pg_dsn": "postgres://user@localhost:5432/nova"}}, "")
	require.NoError(t, err)
	raw, err := inv.JSON()
	require.NoError(t, err)
	var parsed map[string]map[string]any
	require.NoError(t, json.Unmarshal(raw, &parsed))
	for _, g := range []string{"all", "benches", "coordinator", "store", "store_deployer", "runners"} {
		assert.Equal(t, []any{}, parsed[g]["hosts"], g)
	}
	_, err = inv.HostJSON("nosuch")
	var unknown *UnknownHostError
	require.ErrorAs(t, err, &unknown)
	assert.Equal(t, []string{}, unknown.Known)
}

// A fixture file is the snapshot the Redis view gives for the same rows:
// the loop fields in their canonical text, enabled true unless it says
// false, the loops key's absence the loop kind never applied, and an
// unknown field refused.
func TestLoadFixtureIsTheAppliedStateOfTheSameRows(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(name, text string) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte(text), 0o600))
		return p
	}
	full := write("full.yml", `
machines:
  bench-alpha: {user: user-a, seat: seat-a, slots: 4, runners: 1, os: darwin, arch: arm64}
  bench-beta: {user: user-b, seat: seat-b, slots: 2}
fleet: {store: bench-beta, coordinator: bench-alpha, redis_port: 6380, pg_dsn: postgres://nova_config@localhost:5432/nova}
loops:
  member-beta: {machine: bench-beta, argv: [nova-swarm, member], seat: seat-b, keys: [Z_KEY, A_KEY], keepalive: true, width: 2}
  tick: {machine: bench-alpha, argv: ["~/bin/tick", "--once"], every: 30, enabled: false}
`)
	snap, err := LoadFixture(full)
	require.NoError(t, err)
	assert.Equal(t, View{"user": "user-a", "seat": "seat-a", "slots": "4", "runners": "1"}, snap.Machines["bench-alpha"])
	assert.Equal(t, &Beat{OS: "darwin", Arch: "arm64"}, snap.Beats["bench-alpha"])
	assert.NotContains(t, snap.Beats, "bench-beta")
	assert.Equal(t, View{"store": "bench-beta", "coordinator": "bench-alpha", "redis_port": "6380", "pg_dsn": "postgres://nova_config@localhost:5432/nova"}, snap.Fleet)
	assert.Equal(t, View{
		"name": "member-beta", "machine": "bench-beta", "argv": `["nova-swarm","member"]`, "seat": "seat-b",
		"keys": "A_KEY,Z_KEY", "every": "0", "keepalive": "true", "width": "2", "enabled": "true",
		"log": "~/nova-bench/loops/member-beta.log",
	}, snap.Loops["member-beta"])
	assert.Equal(t, "false", snap.Loops["tick"]["enabled"])
	inv, err := BuildInventory(snap, "")
	require.NoError(t, err)
	assert.Len(t, inv.Meta.Hostvars["bench-beta"]["nova_loops"], 1)

	bare, err := LoadFixture(write("bare.yml", "machines:\n  bench-alpha: {user: u, seat: s, slots: 1}\n"))
	require.NoError(t, err)
	assert.Nil(t, bare.Loops)
	assert.Empty(t, bare.Fleet["redis_port"], "a legacy fixture never guesses a port")
	_, err = BuildInventory(bare, "")
	assert.ErrorContains(t, err, "endpoints are unset: redis_port, pg_dsn")

	for name, text := range map[string]string{
		"unknown field": "machines:\n  bench-alpha: {user: u, seats: s}\n",
		"bad name":      "machines:\n  Bench_Alpha: {user: u}\n",
	} {
		_, err := LoadFixture(write(name+".yml", text))
		assert.Error(t, err, name)
	}
	_, err = LoadFixture(filepath.Join(dir, "absent.yml"))
	assert.ErrorContains(t, err, "--fixture")
}
