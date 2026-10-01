package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/config"
)

const storeAddr = "127.0.0.1:6379"

// inventoryHarness is a harness whose fake Redis holds n applied machines
// named bench-01 ... bench-nn, and NOVA_SPRINT_REDIS naming it.
func inventoryHarness(t *testing.T, n int) *harness {
	t.Helper()
	h := newHarness()
	h.env["NOVA_SPRINT_REDIS"] = storeAddr
	h.redis.views[config.KindMachine] = map[string]config.View{}
	for i := 1; i <= n; i++ {
		h.redis.views[config.KindMachine][fmt.Sprintf("bench-%02d", i)] = config.View{"user": "user-a", "seat": "seat-a", "slots": "8", "runners": "0"}
	}
	h.redis.revs[config.KindMachine] = 1
	return h
}

// localMachines is the machines of an inventory that carry
// ansible_connection=local.
func localMachines(t *testing.T, out string) []string {
	t.Helper()
	var inv config.AnsibleInventory
	require.NoError(t, json.Unmarshal([]byte(out), &inv), out)
	var local []string
	for _, n := range inv.All.Hosts {
		if inv.Meta.Hostvars[n]["ansible_connection"] == "local" {
			local = append(local, n)
		}
	}
	return local
}

// The verb prints the applied state of the store: the machines, the fleet
// row's groups, the loops once applied; --list is the default and --host
// one machine's variables. Postgres is never opened.
func TestInventoryPrintsTheAppliedState(t *testing.T) {
	t.Parallel()
	h := inventoryHarness(t, 2)
	h.env["NOVA_MACHINE"] = "bench-01"
	h.redis.views[config.KindFleet] = map[string]config.View{config.KindFleet: {"store": "bench-02", "coordinator": "bench-01"}}
	h.redis.views["loop"] = map[string]config.View{"member-02": {
		"name": "member-02", "machine": "bench-02", "argv": `["nova-swarm","member"]`, "seat": "seat-a", "keys": "API_KEY",
		"every": "0", "keepalive": "true", "width": "2", "enabled": "true", "log": "~/nova-bench/loops/member-02.log",
	}}
	h.redis.revs["loop"] = 1
	h.redis.beats["bench-02"] = &config.Beat{OS: "linux", Arch: "amd64"}

	code, out, errs := h.run(t, "inventory")
	require.Equal(t, 0, code, errs)
	var inv config.AnsibleInventory
	require.NoError(t, json.Unmarshal([]byte(out), &inv))
	assert.Equal(t, []string{"bench-01", "bench-02"}, inv.All.Hosts)
	assert.Equal(t, []string{"bench-01"}, inv.StoreDeployer.Hosts)
	assert.Equal(t, []string{"bench-02"}, inv.Store.Hosts)
	assert.Equal(t, "local", inv.Meta.Hostvars["bench-01"]["ansible_connection"])
	assert.Equal(t, "linux", inv.Meta.Hostvars["bench-02"]["nova_os"])
	assert.Len(t, inv.Meta.Hostvars["bench-02"]["nova_loops"], 1)
	assert.Equal(t, 0, h.opens, "inventory opened Postgres")
	assert.Equal(t, 1, h.redis.snapshots)

	_, listed, _ := h.run(t, "inventory", "--list")
	assert.Equal(t, out, listed)
	code, host, _ := h.run(t, "inventory", "--host", "bench-02")
	require.Equal(t, 0, code)
	var hv map[string]any
	require.NoError(t, json.Unmarshal([]byte(host), &hv))
	assert.Equal(t, "bench-02", hv["ansible_host"])
}

// The fixture stands in for the store: no store is opened, and the fixture
// the help names prints an inventory with loops on every machine.
func TestInventoryFromTheFixtureOpensNoStore(t *testing.T) {
	t.Parallel()
	h := newHarness()
	code, out, errs := h.run(t, "inventory", "--fixture", filepath.Join("..", "..", "fleet", "testdata", "inventory-fixture.yml"))
	require.Equal(t, 0, code, errs)
	var inv config.AnsibleInventory
	require.NoError(t, json.Unmarshal([]byte(out), &inv))
	assert.NotEmpty(t, inv.All.Hosts)
	for _, m := range inv.All.Hosts {
		assert.Contains(t, inv.Meta.Hostvars[m], "nova_loops", m)
	}
	assert.Equal(t, 0, h.redis.opens+h.opens)
}

// Every flag problem is refused in one line before any store is opened.
func TestInventoryBadFlagsAreRefusedBeforeTheStoreIsOpened(t *testing.T) {
	t.Parallel()
	cases := []struct {
		args []string
		want []string
	}{
		{[]string{"--list", "--host", "bench-01"}, []string{"--list", "--host", "exclusive"}},
		{[]string{"--host="}, []string{"--host", "empty"}},
		{[]string{"--host"}, []string{"host"}},
		{[]string{"bench-01"}, []string{"no arguments"}},
		{[]string{"--fixture", "f.yml", "--redis", storeAddr}, []string{"--fixture and --redis are exclusive"}},
		{[]string{"--fixture="}, []string{"--fixture wants a file"}},
		{[]string{"--timeout", "0s"}, []string{"--timeout wants a Go duration above 0"}},
		{[]string{"--timeout", "soon"}, []string{"timeout"}},
		{[]string{"--host=", "--timeout", "-1s"}, []string{"--host", "--timeout"}},
	}
	for _, tc := range cases {
		t.Run(strings.Join(tc.args, " "), func(t *testing.T) {
			t.Parallel()
			h := inventoryHarness(t, 1)
			code, out, errs := h.run(t, append([]string{"inventory"}, tc.args...)...)
			assert.Equal(t, 2, code)
			assert.Empty(t, out)
			assert.Equal(t, 1, strings.Count(errs, "\n"), errs)
			for _, w := range tc.want {
				assert.Contains(t, errs, w)
			}
			assert.Equal(t, 0, h.redis.opens+h.opens)
		})
	}
	h := newHarness()
	code, _, errs := h.run(t, "inventory")
	assert.Equal(t, 2, code)
	assert.Contains(t, errs, "--redis is required")
}

// --host and NOVA_MACHINE name a machine exactly; a name with no row is
// refused with the known names, at most twenty, quoted so a stray space
// shows.
func TestInventoryUnknownNamesAreRefusedWithTheKnownOnes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, host, self, want string
		n                      int
	}{
		{"host", "nosuch", "", `--host "nosuch" names no machine row; known machines: bench-01, bench-02, bench-03; run: nova-config machine list`, 3},
		{"host with a space", "bench-01 ", "", `--host "bench-01 " names no machine row`, 1},
		{"no machines", "nosuch", "", "known machines: none;", 0},
		{"bounded", "nosuch", "", "bench-20 and 5 more;", 25},
		{"self", "", "BENCH-01", `NOVA_MACHINE="BENCH-01" names no machine row (the name is matched exactly); known machines: bench-01, bench-02; run: nova-config machine list`, 2},
		{"self with a space", "", " bench-01", `NOVA_MACHINE=" bench-01" names no machine row`, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := inventoryHarness(t, tc.n)
			args := []string{"inventory"}
			if tc.host != "" {
				args = append(args, "--host", tc.host)
			}
			if tc.self != "" {
				h.env["NOVA_MACHINE"] = tc.self
			}
			code, out, errs := h.run(t, args...)
			assert.Equal(t, 1, code)
			assert.Empty(t, out)
			assert.Contains(t, errs, tc.want)
			assert.NotContains(t, errs, "bench-21")
		})
	}
}

// The machine marked local: NOVA_MACHINE exactly, else the lower-cased
// first label of the hostname, else none; an empty NOVA_MACHINE is unset and
// the old FLEET_SELF is not read.
func TestInventoryLocalMachine(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, hostname string
		env            map[string]string
		want           []string
	}{
		{"NOVA_MACHINE", "elsewhere.example", map[string]string{"NOVA_MACHINE": "bench-02"}, []string{"bench-02"}},
		{"hostname", "bench-03.tailnet.example", nil, []string{"bench-03"}},
		{"NOVA_MACHINE outranks the hostname", "bench-03.local", map[string]string{"NOVA_MACHINE": "bench-01"}, []string{"bench-01"}},
		{"hostname lower-cased", "Bench-02.local", nil, []string{"bench-02"}},
		{"empty NOVA_MACHINE is unset", "bench-01.local", map[string]string{"NOVA_MACHINE": ""}, []string{"bench-01"}},
		{"FLEET_SELF is not read", "elsewhere.example", map[string]string{"FLEET_SELF": "bench-01"}, nil},
		{"no match marks nothing", "bench-011.local", nil, nil},
		{"no hostname", "", nil, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := inventoryHarness(t, 3)
			h.hostname = tc.hostname
			for k, v := range tc.env {
				h.env[k] = v
			}
			code, out, errs := h.run(t, "inventory")
			require.Equal(t, 0, code, errs)
			assert.Equal(t, tc.want, localMachines(t, out))
		})
	}
}

// A store that never answers is given up at --timeout, with the stage, the
// address and the command to run again with a longer bound; the remedy runs.
func TestInventoryTimesOutWaitingForTheStore(t *testing.T) {
	t.Parallel()
	h := inventoryHarness(t, 2)
	h.redis.hang = true
	code, out, errs := h.run(t, "inventory", "--redis", storeAddr, "--host", "bench-01", "--timeout", "50ms")
	want := "nova-config inventory: timed out after 50ms waiting for the store at " + storeAddr + " while reading the applied state; check that Redis answers there; run: nova-config inventory --redis " + storeAddr + " --host bench-01 --timeout 150ms\n"
	assert.Equal(t, 2, code)
	assert.Empty(t, out)
	require.Equal(t, want, errs)

	h.redis.hang = false
	_, remedy, _ := strings.Cut(strings.TrimSuffix(errs, "\n"), "; run: ")
	code, out, errs = h.run(t, strings.Fields(remedy)[1:]...)
	assert.Equal(t, 0, code, errs)
	assert.Contains(t, out, `"ansible_host": "bench-01"`)
}

// A loop view the plays cannot render is refused (exit 1) with the
// command that repeats the run.
func TestInventoryRefusesALoopItCannotRender(t *testing.T) {
	t.Parallel()
	h := inventoryHarness(t, 1)
	h.redis.views["loop"] = map[string]config.View{"bad": {"machine": "bench-01", "argv": "not json", "every": "5", "log": "~/nova-bench/loops/bad.log"}}
	h.redis.revs["loop"] = 1
	code, out, errs := h.run(t, "inventory")
	assert.Equal(t, 1, code)
	assert.Empty(t, out)
	assert.Contains(t, errs, "loop bad: argv")
	assert.True(t, strings.HasSuffix(errs, "; run: nova-config inventory\n"), errs)
}

// helpCommands returns the printf and chmod commands the inventory help
// prints, each as printed (the help left-trims its lines).
func helpCommands(t *testing.T, help string) (printf, chmod string) {
	t.Helper()
	for _, l := range strings.Split(help, "\n") {
		l = strings.TrimSpace(l)
		switch {
		case strings.HasPrefix(l, "printf "):
			printf = l
		case strings.HasPrefix(l, "chmod +x "):
			chmod = l
		}
	}
	require.NotEmpty(t, printf, help)
	require.NotEmpty(t, chmod, help)
	return printf, chmod
}

// The help names every flag, variable, group and exit code, and the
// wrapper it prints, written and run in a temp dir, is a working inventory
// script; the docs carry the same commands, with no issue numbers or dates.
func TestInventoryHelpAndDocsReachAWorkingRun(t *testing.T) {
	t.Parallel()
	h := newHarness()
	code, help, errs := h.run(t, "inventory", "-h")
	require.Equal(t, 0, code, errs)
	for _, w := range []string{
		"--list", "--host", "--redis", "--fixture", "--timeout",
		"NOVA_SPRINT_REDIS", "NOVA_MACHINE",
		"first run", "nova-config inventory --fixture fleet/testdata/inventory-fixture.yml",
		"-i wants an executable", "column one", "ANSIBLE_INVENTORY_UNPARSED_FAILED=true ansible-inventory -i ./nova-inventory --list", "a failed inventory is an empty inventory", "unparsed_is_failed = True",
		"_meta.hostvars", "ansible never calls --host", "the default when neither --list nor --host is given",
		"store_deployer", "nova_loops", "nova_os", "never Postgres",
		"matched by exact machine name", "lower-cased first label", "nothing is marked local", "an empty value counts as unset",
		"this verb exits 0 when it printed, 1 when the applied state or an unknown machine refused it, 2 when it could not run (usage, connection, timeout)",
		"exit codes: 0 done, 1 refused, 2 usage\n",
	} {
		assert.Contains(t, help, w)
	}
	issue := regexp.MustCompile(`#[0-9]+|ideas#|\b20[0-9]{2}-[0-9]{2}-[0-9]{2}\b`)
	assert.Empty(t, issue.FindString(help))
	printf, chmod := helpCommands(t, help)
	for _, doc := range []string{"docs/CLI.md", "docs/nova-config/README.md", "docs/FLEET.md"} {
		raw, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(doc)))
		require.NoError(t, err)
		for _, w := range []string{printf, chmod, "ANSIBLE_INVENTORY_UNPARSED_FAILED=true ansible-inventory -i ./nova-inventory --list", "nova_loops", "store_deployer"} {
			assert.Contains(t, string(raw), w, doc)
		}
	}

	dir := t.TempDir()
	sh := func(script string) (string, error) {
		cmd := exec.Command("/bin/sh", "-c", script)
		cmd.Dir = dir
		cmd.Env = []string{"PATH=" + dir + ":/usr/bin:/bin"}
		out, err := cmd.CombinedOutput()
		return string(out), err
	}
	out, err := sh(printf + "\n" + chmod)
	require.NoError(t, err, out)
	wrapper, err := os.ReadFile(filepath.Join(dir, "nova-inventory"))
	require.NoError(t, err)
	assert.Equal(t, "#!/bin/sh\nexec nova-config inventory \"$@\"\n", string(wrapper))
	self, err := os.Executable()
	require.NoError(t, err)
	shim := "#!/bin/sh\nNOVA_CONFIG_TEST_HELPER=1 exec " + strconv.Quote(self) + " -test.run='^TestInventoryHelperProcess$' -- \"$@\"\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "nova-config"), []byte(shim), 0o755))
	for _, args := range []string{"--list", "--host bench-02"} {
		out, err := sh("./nova-inventory " + args)
		require.NoError(t, err, out)
		assert.Contains(t, out, `"ansible_host": "bench-`)
	}
}

// TestInventoryHelperProcess is the tool the wrapper test puts on the PATH:
// the test binary, run with the wrapper's arguments against a fake store.
// It does nothing in a normal test run.
func TestInventoryHelperProcess(t *testing.T) {
	t.Parallel()
	if os.Getenv("NOVA_CONFIG_TEST_HELPER") != "1" {
		return
	}
	var args []string
	for i, a := range os.Args {
		if a == "--" {
			args = os.Args[i+1:]
			break
		}
	}
	h := inventoryHarness(t, 2)
	os.Exit(run(args, os.Stdout, os.Stderr, h.deps()))
}
