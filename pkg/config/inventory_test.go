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

// snapshot is two machines with complete applied rows and beats, the fleet
// row naming both and the loops the case gives (nil: the loop kind never applied).
func snapshot(loops map[string]View) *Snapshot {
	s := &Snapshot{
		Machines: map[string]View{
			"bench-alpha": {"user": "user-a", "seat": "seat-a", "slots": "64", "width": "8", "runners": "1"},
			"bench-beta":  {"user": "user-b", "seat": "seat-b", "slots": "40", "width": "3", "runners": "0"},
		},
		Fleet: View{"store": "bench-beta", "coordinator": "bench-alpha", "redis_port": "6380", "pg_dsn": "postgres://nova_config@localhost:5432/nova", "loops_dir": "~/nova-bench/loops"},
		Loops: loops,
		Beats: map[string]*Beat{
			"bench-alpha": {OS: "linux", Arch: "amd64", Cores: "16"},
			"bench-beta":  {OS: "linux", Arch: "amd64", Cores: "8"},
		},
		Revs: map[string]int64{KindMachine: 3, KindFleet: 2},
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
		"ansible_host": "bench-alpha", "ansible_user": "user-a", "nova_seat": "seat-a", "registry_seat": "seat-a", "slots": 64, "slots_capacity": 64, "slots_share": 8, "runners": 1,
		"kind": "machine", "nova_tla": false, "ansible_connection": "local", "bench_os": "linux", "cores": 16, "nova_os": "linux", "nova_arch": "amd64", "nova_redis_port": 6380, "nova_redis_addr": "bench-beta:6380", "nova_pg_dsn": "postgres://nova_config@localhost:5432/nova",
		"nova_loops": []InventoryLoop{{Name: "tick", Argv: []string{"nova-sprint", "run"}, Keys: []string{}, Every: 5, Log: "~/nova-bench/loops/tick.log"}},
	}, alpha)
	assert.Equal(t, 40, beta["slots_capacity"])
	assert.Equal(t, 3, beta["slots_share"])
	assert.Equal(t, "seat-b", beta["registry_seat"])
	assert.Equal(t, 8, beta["cores"])
	assert.Equal(t, "linux", beta["nova_os"])
	assert.Equal(t, "amd64", beta["nova_arch"])
	assert.Equal(t, "bench-beta:6380", beta["nova_redis_addr"])
	assert.Equal(t, "postgres://nova_config@localhost:5432/nova", beta["nova_pg_dsn"], "the explicit localhost DSN is preserved")
	assert.NotContains(t, beta, "ansible_connection")
	assert.Equal(t, "linux", alpha["bench_os"])
	assert.Equal(t, []InventoryLoop{{
		Name: "member-beta", Argv: []string{"nova-swarm", "member", "--as", "bench-beta"}, Seat: "seat-a",
		Keys: []string{"API_KEY", "BENCH_PASSWORD"}, Keepalive: true, Enabled: true,
		Log: "~/nova-bench/loops/member-beta.log",
	}}, beta["nova_loops"])
}

// TestInventoryEmitsEveryHostVarThePlaysRead covers the host variables whose
// source already exists in the applied machine row or measured beat. The
// legacy registry_roles source has no machine-row field in this schema and is
// deliberately not guessed here; its role-derived groups remain the current
// inventory's fleet projection until a schema card supplies that source.
func TestInventoryEmitsEveryHostVarThePlaysRead(t *testing.T) {
	t.Parallel()
	s := &Snapshot{
		Machines: map[string]View{
			"linux-bench":  {"user": "linux-user", "seat": "seat-linux", "slots": "20", "width": "4", "runners": "1"},
			"darwin-bench": {"user": "mac-user", "seat": "seat-mac", "slots": "12", "width": "2", "runners": "0"},
			"store-only":   {"user": "store-user", "seat": "seat-store", "slots": "48", "width": "0", "runners": "0"},
			"coordinator":  {"user": "coord-user", "seat": "seat-coord", "slots": "36", "runners": "0"},
		},
		Fleet: View{"store": "store-only", "coordinator": "coordinator", "redis_port": "6380", "pg_dsn": "postgres://config@store:5432/nova"},
		Beats: map[string]*Beat{
			"linux-bench":  {OS: "linux", Arch: "amd64", Cores: "8"},
			"darwin-bench": {OS: "darwin", Arch: "arm64", Cores: "8"},
			"store-only":   {OS: "linux", Arch: "amd64", Cores: "16"},
			"coordinator":  {OS: "linux", Arch: "amd64", Cores: "12"},
		},
	}
	inv, err := BuildInventory(s, "coordinator")
	require.NoError(t, err)

	for host, want := range map[string]map[string]any{
		"linux-bench": {
			"ansible_host": "linux-bench", "bench_os": "linux", "cores": 8,
			"registry_seat": "seat-linux", "slots_capacity": 20, "slots_share": 4,
		},
		"darwin-bench": {
			"ansible_host": "darwin-bench", "bench_os": "darwin", "cores": 8,
			"registry_seat": "seat-mac", "slots_capacity": 12, "slots_share": 2,
		},
		"store-only": {
			"ansible_host": "store-only", "bench_os": "linux", "cores": 16,
			"registry_seat": "seat-store", "slots_capacity": 48, "slots_share": 0,
		},
		"coordinator": {
			"ansible_host": "coordinator", "bench_os": "linux", "cores": 12,
			"registry_seat": "seat-coord", "slots_capacity": 36, "slots_share": 6,
			"ansible_connection": "local",
		},
	} {
		for field, value := range want {
			assert.Equal(t, value, inv.Meta.Hostvars[host][field], "%s.%s", host, field)
		}
	}
	assert.Equal(t, []string{"coordinator", "darwin-bench", "linux-bench", "store-only"}, inv.Benches.Hosts)
	assert.Equal(t, []string{"store-only"}, inv.Store.Hosts)
	assert.Equal(t, []string{"coordinator"}, inv.Coordinator.Hosts)
	assert.NotContains(t, inv.Meta.Hostvars["linux-bench"], "registry_roles", "the machine schema has no role source to project")

	t.Run("missing required beat fact refuses with repair", func(t *testing.T) {
		t.Parallel()
		missing := *s
		missing.Beats = map[string]*Beat{}
		for host, beat := range s.Beats {
			copy := *beat
			missing.Beats[host] = &copy
		}
		beat := *missing.Beats["coordinator"]
		beat.Cores = ""
		missing.Beats["coordinator"] = &beat
		inv, err := BuildInventory(&missing, "")
		require.Error(t, err)
		assert.Nil(t, inv)
		assert.Contains(t, err.Error(), "machine coordinator: cores is unset in beat field ncpu")
		assert.Contains(t, err.Error(), "restore or start the machine's configured beat publisher")
	})
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

// An empty optional login is left out, never emitted as ""; the required seat
// remains explicit for the plays that check the registry seat.
func TestBuildInventoryOmitsEmptyValues(t *testing.T) {
	t.Parallel()
	s := snapshot(nil)
	s.Machines["bench-empty"] = View{"user": "", "seat": "seat-empty", "slots": "2", "width": "0", "runners": "0"}
	s.Beats["bench-empty"] = &Beat{OS: "linux", Arch: "amd64", Cores: "2"}
	inv, err := BuildInventory(s, "")
	require.NoError(t, err)
	hv := inv.Meta.Hostvars["bench-empty"]
	for _, k := range []string{"ansible_user", "user"} {
		assert.NotContains(t, hv, k)
	}
	assert.Equal(t, "seat-empty", hv["registry_seat"])
	assert.Equal(t, "seat-empty", hv["nova_seat"])
	assert.Equal(t, 2, hv["slots"])
}

// A fleet with declared endpoints and no machines has every group present.
func TestBuildInventoryOfAnEmptyStore(t *testing.T) {
	t.Parallel()
	inv, err := BuildInventory(&Snapshot{Fleet: View{"redis_port": "6380", "pg_dsn": "postgres://user@localhost:5432/nova", "loops_dir": "~/nova-bench/loops"}}, "")
	require.NoError(t, err)
	raw, err := inv.JSON()
	require.NoError(t, err)
	var parsed map[string]map[string]any
	require.NoError(t, json.Unmarshal(raw, &parsed))
	for _, g := range []string{"all", "benches", "coordinator", "store", "store_deployer", "runners", "tla"} {
		assert.Equal(t, []any{}, parsed[g]["hosts"], g)
	}
	// A store no kind was applied to has no revisions: an empty object, never null.
	assert.Equal(t, map[string]any{"nova_config_rev": map[string]any{}}, parsed["all"]["vars"], "all.vars of %s", raw)
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
  bench-alpha: {user: user-a, seat: seat-a, slots: 4, width: 3, runners: 1, os: darwin, arch: arm64, cores: 8}
  bench-beta: {user: user-b, seat: seat-b, slots: 2, width: 1, runners: 0, os: linux, arch: amd64, cores: 4}
fleet: {store: bench-beta, coordinator: bench-alpha, redis_port: 6380, pg_dsn: postgres://nova_config@localhost:5432/nova, loops_dir: "~/nova-bench/loops"}
loops:
  member-beta: {machine: bench-beta, argv: [nova-swarm, member], seat: seat-b, keys: [Z_KEY, A_KEY], keepalive: true}
  tick: {machine: bench-alpha, argv: ["~/bin/tick", "--once"], every: 30, enabled: false}
`)
	snap, err := LoadFixture(full)
	require.NoError(t, err)
	assert.Equal(t, View{"user": "user-a", "seat": "seat-a", "slots": "4", "width": "3", "runners": "1", "tla": "false"}, snap.Machines["bench-alpha"])
	assert.Equal(t, &Beat{OS: "darwin", Arch: "arm64", Cores: "8"}, snap.Beats["bench-alpha"])
	assert.Equal(t, &Beat{OS: "linux", Arch: "amd64", Cores: "4"}, snap.Beats["bench-beta"])
	assert.Equal(t, View{"store": "bench-beta", "coordinator": "bench-alpha", "redis_port": "6380", "pg_dsn": "postgres://nova_config@localhost:5432/nova", "loops_dir": "~/nova-bench/loops"}, snap.Fleet)
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

// A fixture that is not the fixture's shape is refused in one line naming
// what it wants, with the YAML reader's line and never its Go type.
func TestAFixtureOfTheWrongShapeIsRefusedInOneLine(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		doc  string
		want string
	}{
		{"root is a list", "- one\n- two\n", "line 1: the document is a map with machines, fleet and loops, got a list"},
		{"machines is a list", "machines:\n  - one\n  - two\n", "line 2: machines is a map of name to machine, got a list"},
		{"machine is text", "machines:\n  m1: five\n", "line 2: a machine is a map of user, seat, slots, runners, tla, os, arch, cores and width, got text"},
		{"machine field is text", "machines:\n  m1: {slots: soon}\n", "line 2: a number is wanted, got text"},
		{"fleet is text", "fleet: five\n", "line 1: fleet is a map of store, coordinator, redis_port, pg_dsn and loops_dir, got text"},
		{"loops is a list", "loops:\n  - one\n", "line 2: loops is a map of name to loop, got a list"},
		{"loop is text", "loops:\n  l1: five\n", "line 2: a loop is a map of machine, argv, seat, keys, every, keepalive and enabled, got text"},
		{"unknown field", "machines: {}\nfleet: {}\nbogus: 1\n", "line 3: bogus is not a fixture field; want machines, fleet or loops"},
		{"machine unknown field", "machines:\n  m1: {bogus: 1}\n", "line 2: bogus is not a fixture field; want user, seat, slots, runners, tla, os, arch, cores or width"},
		{"two problems at once", "machines:\n  - one\nfleet: five\n", "line 2: machines is a map of name to machine, got a list; line 3: fleet is a map of store, coordinator, redis_port, pg_dsn and loops_dir, got text"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "fx.yml")
			require.NoError(t, os.WriteFile(path, []byte(tc.doc), 0o600))
			_, err := LoadFixture(path)
			require.Error(t, err)
			msg := err.Error()
			assert.NotContains(t, msg, "\n")
			assert.Contains(t, msg, "is not the fixture's shape: "+tc.want)
			assert.NotContains(t, msg, "map[string]")
			assert.NotContains(t, msg, "struct {")
			assert.NotContains(t, msg, "yaml:")
		})
	}
	_, err := LoadFixture(filepath.Join(t.TempDir(), "absent.yml"))
	assert.ErrorContains(t, err, "--fixture")
}

// A machine row's tla fact is the inventory's tla group and its nova_tla
// host variable: the tools play installs the pinned TLC jar on exactly those
// machines (fleet/tools.yml, the tla play). A view written before the field
// existed (no tla key) is no record machine.
func TestTLAMachinesAreTheTLAGroup(t *testing.T) {
	t.Parallel()
	s := snapshot(nil)
	s.Machines["bench-beta"]["tla"] = "true"
	s.Machines["bench-gamma"] = View{"user": "user-c", "seat": "seat-c", "slots": "8", "runners": "0", "tla": "false"}
	s.Beats["bench-gamma"] = &Beat{OS: "linux", Arch: "amd64", Cores: "4"}
	inv, err := BuildInventory(s, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"bench-beta"}, inv.TLA.Hosts)
	assert.Equal(t, true, inv.Meta.Hostvars["bench-beta"]["nova_tla"])
	assert.Equal(t, false, inv.Meta.Hostvars["bench-gamma"]["nova_tla"])
	assert.Equal(t, false, inv.Meta.Hostvars["bench-alpha"]["nova_tla"], "a view with no tla key")

	path := filepath.Join(t.TempDir(), "fx.yml")
	require.NoError(t, os.WriteFile(path, []byte("machines:\n  bench-alpha: {user: u, seat: s, slots: 1, tla: true, os: linux, arch: amd64, cores: 2}\n  bench-beta: {user: u, seat: s, slots: 1, os: linux, arch: amd64, cores: 2}\nfleet: {redis_port: 6380, pg_dsn: postgres://u@localhost:5432/nova, loops_dir: \"~/nova-bench/loops\"}\n"), 0o600))
	snap, err := LoadFixture(path)
	require.NoError(t, err)
	assert.Equal(t, "true", snap.Machines["bench-alpha"]["tla"])
	assert.Equal(t, "false", snap.Machines["bench-beta"]["tla"])
	inv, err = BuildInventory(snap, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"bench-alpha"}, inv.TLA.Hosts)
	empty, err := BuildInventory(&Snapshot{Fleet: View{"redis_port": "6380", "pg_dsn": "postgres://user@localhost:5432/nova", "loops_dir": "~/nova-bench/loops"}}, "")
	require.NoError(t, err)
	assert.Equal(t, []string{}, empty.TLA.Hosts, "an empty group is [], never null")
}

// A machine key that is not a row name is refused before the inventory is built.
func TestBuildInventoryRefusesAMachineNameThatIsNotARowName(t *testing.T) {
	t.Parallel()
	s := snapshot(nil)
	s.Machines["../x"] = View{"user": "user-a", "seat": "seat-a", "slots": "64", "runners": "1"}
	inv, err := BuildInventory(s, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "../x")
	assert.Nil(t, inv)

	valid, err := BuildInventory(snapshot(nil), "")
	require.NoError(t, err)
	assert.NotNil(t, valid)
}

// TestTheInventoryCarriesTheBusAddress: the applied fleet row's bus is
// nova_bus_addr on every host when it is set, and the variable is absent
// when the bus is empty.
func TestTheInventoryCarriesTheBusAddress(t *testing.T) {
	t.Parallel()
	const bus = "127.0.0.1:6381"
	with := snapshot(nil)
	with.Fleet["bus"] = bus
	inv, err := BuildInventory(with, "")
	require.NoError(t, err)
	require.NotEmpty(t, inv.All.Hosts)
	for _, host := range inv.All.Hosts {
		assert.Equal(t, bus, inv.Meta.Hostvars[host]["nova_bus_addr"], host)
	}

	for _, fleetBus := range []string{"", "absent"} {
		empty := snapshot(nil)
		if fleetBus == "" {
			empty.Fleet["bus"] = ""
		}
		got, err := BuildInventory(empty, "")
		require.NoError(t, err)
		for _, host := range got.All.Hosts {
			assert.NotContains(t, got.Meta.Hostvars[host], "nova_bus_addr", "%s %s", fleetBus, host)
		}
	}
}
