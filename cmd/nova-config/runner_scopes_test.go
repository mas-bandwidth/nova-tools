package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// amdScopes is a Linux bench's runners as the record carries them (cmd/nova-config/README.md, runner_scopes).
const amdScopes = "https://github.com/mas-bandwidth=2,https://github.com/mas-bandwidth/nova-sprint=1:tlc," +
	"https://github.com/mas-bandwidth/schema=2:schema,https://github.com/robotgamedevelopers=2"

// TestRunnerScopesRoundTrip: a value is stored in its canonical form, shows and lists as that, writes nothing when
// set again, puts the machine in the runner group, and the inventory carries it; cleared is none.
func TestRunnerScopesRoundTrip(t *testing.T) {
	t.Parallel()
	h := fleet(t)
	h.ok(t, "machine", "set", "bench-a", "--runner_scopes",
		"https://github.com/robotgamedevelopers/=2, https://github.com/mas-bandwidth/schema=2:schema+schema,"+
			"https://github.com/mas-bandwidth/nova-sprint=1:tlc,https://github.com/mas-bandwidth=2", "--actor", "t")
	assert.Contains(t, h.ok(t, "machine", "show", "bench-a"), " runner_scopes="+amdScopes+" ")
	assert.Contains(t, h.ok(t, "machine", "list"), " runner_scopes="+amdScopes+" ")
	assert.Contains(t, h.ok(t, "machine", "show", "bench-a", "--json"), `"runner_scopes":"`+amdScopes+`"`)
	assert.Contains(t, h.ok(t, "machine", "set", "bench-a", "--runner_scopes", amdScopes, "--actor", "t"), "changed=none")

	_, inv, _ := h.run(t, "inventory", "--host", "bench-a")
	assert.Contains(t, strings.Join(strings.Fields(inv), ""), `"nova_runner_scopes":[`+
		`{"url":"https://github.com/mas-bandwidth","count":2,"labels":[]},`+
		`{"url":"https://github.com/mas-bandwidth/nova-sprint","count":1,"labels":["tlc"]},`+
		`{"url":"https://github.com/mas-bandwidth/schema","count":2,"labels":["schema"]},`+
		`{"url":"https://github.com/robotgamedevelopers","count":2,"labels":[]}]`)
	_, list, _ := h.run(t, "inventory", "--list")
	var all struct {
		Runner struct{ Hosts []string } `json:"runner"`
	}
	require.NoError(t, json.Unmarshal([]byte(list), &all))
	assert.Contains(t, all.Runner.Hosts, "bench-a", "a machine with runner scopes is in the runner group, whatever its roles and runners")

	h.ok(t, "machine", "set", "bench-a", "--runner_scopes", "", "--actor", "t")
	assert.Contains(t, h.ok(t, "machine", "show", "bench-a"), " runner_scopes=- ")
	_, inv, _ = h.run(t, "inventory", "--host", "bench-a")
	assert.NotContains(t, inv, "nova_runner_scopes", "cleared is none")
}

// runner_scopes takes the same values as windows_runners and refuses the same ones (one parser).
func TestRunnerScopesCanonical(t *testing.T) {
	t.Parallel()
	f := kinds["machine"].findField("runner_scopes")
	for in, want := range map[string]string{
		"":        "",
		amdScopes: amdScopes,
		"https://github.com/o=1:b+a+b,https://github.com/a/b=99": "https://github.com/a/b=99,https://github.com/o=1:a+b",
	} {
		got, err := f.canonical(in)
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}
	for _, bad := range []string{"https://github.com/o/r", "https://github.com/o/r=0", "https://gitlab.com/o/r=1",
		"https://github.com/o/r=1:a;b", "https://github.com/o/r=1,https://github.com/O/R=2"} {
		_, err := f.canonical(bad)
		assert.ErrorContains(t, err, "--runner_scopes", bad)
	}
}

// A corrupt value in the store (written by hand) refuses the inventory and names the fix.
func TestRunnerScopesCorruptRefusesTheInventory(t *testing.T) {
	t.Parallel()
	_, err := build([]Row{{Name: "m1", F: map[string]string{"runner_scopes": "nope"}}}, nil, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nova-config machine set m1 --runner_scopes")
}

// A second set of runners on a url (@<set>[/<group>]) is stored after the url's plain one, reaches the inventory with its
// set and group, and is refused where two entries would share runner directories and on windows_runners.
func TestRunnerScopesSets(t *testing.T) {
	t.Parallel()
	f := kinds["machine"].findField("runner_scopes")
	got, err := f.canonical("https://github.com/o=1:x@nightly/public-group, https://github.com/o=2:b+a, https://github.com/o=1@aa")
	require.NoError(t, err)
	assert.Equal(t, "https://github.com/o=2:a+b,https://github.com/o=1@aa,https://github.com/o=1:x@nightly/public-group", got)
	for _, bad := range []string{
		"https://github.com/o=1@nn,https://github.com/O=2@nn",       // the url and set twice
		"https://github.com/o=1@nexus,https://github.com/o/nexus=1", // both would be o-nexus-<i>
		"https://github.com/o=1@", "https://github.com/o=1@N", "https://github.com/o=1@1a", "https://github.com/o=1@ab/",
		"https://github.com/o=1@ab/g!g", "https://github.com/o=1@ab/g/h", "https://github.com/o=1@ab-",
		"https://github.com/o=1@win-x", "https://github.com/o/r=1@ab/g", // a Windows-side name; a repository has no groups
	} {
		_, err := f.canonical(bad)
		assert.ErrorContains(t, err, "--runner_scopes", bad)
	}
	_, err = kinds["machine"].findField("windows_runners").canonical("https://github.com/o=1@ab")
	assert.ErrorContains(t, err, "--windows_runners", "the windows-runners play names one set per url")

	ws, err := parseScopes("https://github.com/o=1:x@nightly/public-group,https://github.com/o=1")
	require.NoError(t, err)
	b, err := json.Marshal(ws)
	require.NoError(t, err)
	assert.JSONEq(t, `[{"url":"https://github.com/o","count":1,"labels":[]},`+
		`{"url":"https://github.com/o","count":1,"labels":["x"],"set":"nightly","group":"public-group"}]`, string(b),
		"a plain scope's json is as before: no set or group")
}
