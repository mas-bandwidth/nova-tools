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
		"name": name, "machine": machine, "argv": `["nova-swarm","member","--as","` + machine + `"]`,
		"seat": "seat-a", "keys": "API_KEY,BENCH_PASSWORD", "every": "0", "keepalive": "true",
		"enabled": "true", "log": "~/nova-bench/loops/" + name + ".log",
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
		Fleet: View{"store": "bench-beta", "coordinator": "bench-alpha"},
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
		"tick":        loopView("tick", "bench-alpha", map[string]string{"argv": `["nova-sprint","run"]`, "seat": "", "keys": "", "every": "5", "keepalive": "false", "enabled": "false"}),
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
		"kind": "machine", "ansible_connection": "local",
		"nova_loops": []InventoryLoop{{Name: "tick", Argv: []string{"nova-sprint", "run"}, Keys: []string{}, Every: 5, Log: "~/nova-bench/loops/tick.log"}},
	}, alpha)
	assert.Equal(t, "linux", beta["nova_os"])
	assert.Equal(t, "amd64", beta["nova_arch"])
	assert.NotContains(t, beta, "ansible_connection")
	assert.NotContains(t, alpha, "nova_os", "a machine with no beat carries no platform: the plays gather it")
	assert.Equal(t, []InventoryLoop{{
		Name: "member-beta", Argv: []string{"nova-swarm", "member", "--as", "bench-beta"}, Seat: "seat-a",
		Keys: []string{"API_KEY", "BENCH_PASSWORD"}, Keepalive: true, Enabled: true,
		Log: "~/nova-bench/loops/member-beta.log",
	}}, beta["nova_loops"])
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

// An empty store is an inventory with no hosts and every group present.
func TestBuildInventoryOfAnEmptyStore(t *testing.T) {
	t.Parallel()
	inv, err := BuildInventory(&Snapshot{Fleet: View{}}, "")
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
fleet: {store: bench-beta, coordinator: bench-alpha}
loops:
  member-beta: {machine: bench-beta, argv: [nova-swarm, member], seat: seat-b, keys: [Z_KEY, A_KEY], keepalive: true}
  tick: {machine: bench-alpha, argv: ["~/bin/tick", "--once"], every: 30, enabled: false}
`)
	snap, err := LoadFixture(full)
	require.NoError(t, err)
	assert.Equal(t, View{"user": "user-a", "seat": "seat-a", "slots": "4", "runners": "1"}, snap.Machines["bench-alpha"])
	assert.Equal(t, &Beat{OS: "darwin", Arch: "arm64"}, snap.Beats["bench-alpha"])
	assert.NotContains(t, snap.Beats, "bench-beta")
	assert.Equal(t, View{
		"name": "member-beta", "machine": "bench-beta", "argv": `["nova-swarm","member"]`, "seat": "seat-b",
		"keys": "A_KEY,Z_KEY", "every": "0", "keepalive": "true", "enabled": "true",
		"log": "~/nova-bench/loops/member-beta.log",
	}, snap.Loops["member-beta"])
	assert.Equal(t, "false", snap.Loops["tick"]["enabled"])
	inv, err := BuildInventory(snap, "")
	require.NoError(t, err)
	assert.Len(t, inv.Meta.Hostvars["bench-beta"]["nova_loops"], 1)

	bare, err := LoadFixture(write("bare.yml", "machines:\n  bench-alpha: {user: u, seat: s, slots: 1}\n"))
	require.NoError(t, err)
	assert.Nil(t, bare.Loops)

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

// A fixture that is not the fixture's shape is refused in one line naming
// what it wants, with the YAML reader's words quoted, never its newlines.
func TestAFixtureOfTheWrongShapeIsRefusedInOneLine(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "fx.yml")
	require.NoError(t, os.WriteFile(path, []byte("- one\n- two\n"), 0o600))
	_, err := LoadFixture(path)
	require.Error(t, err)
	msg := err.Error()
	assert.NotContains(t, msg, "\n")
	assert.Contains(t, msg, "is not the fixture's shape: \"yaml: unmarshal errors: line 1: cannot unmarshal !!seq into config.fixture\"")
	assert.Contains(t, msg, "want a mapping with machines")
}
