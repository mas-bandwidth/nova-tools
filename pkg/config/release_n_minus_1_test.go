package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestReleaseNInventoryOverReleaseNMinus1Store is the release gate the
// v1.2.19 adopt lacked: release N's inventory is built from the applied state
// release N-1 leaves (fleet/testdata/inventory-fixture-n-1.yml: machine rows
// applied before migration 0042, so no roles field; no beat carrying os, arch
// or ncpu; one machine down), and every host and every group of the inventory
// N-1 itself printed for that state (fleet/testdata/inventory-n-1-golden.json,
// written by v1.2.18's nova-config inventory --fixture --list) is present. An
// adopt runs release N's inventory over exactly that store before release N's
// migrations and apply have run, so anything this refuses or drops is an
// adopt that cannot start. certification.yml runs it in release-checks.
//
// When a release changes what an applied row or a beat carries, the fixture
// and the golden move to the shape the release before it leaves.
func TestReleaseNInventoryOverReleaseNMinus1Store(t *testing.T) {
	t.Parallel()
	testdata := filepath.Join("..", "..", "fleet", "testdata")
	snap, err := LoadFixture(filepath.Join(testdata, "inventory-fixture-n-1.yml"))
	require.NoError(t, err)
	for m, v := range snap.Machines {
		require.NotContains(t, v, "roles", "the N-1 fixture's machine %s must stand for a row applied before 0042", m)
	}

	inv, err := BuildInventory(snap, "")
	require.NoError(t, err, "release N's inventory refused release N-1's store: an adopt over it cannot start")

	raw, err := os.ReadFile(filepath.Join(testdata, "inventory-n-1-golden.json"))
	require.NoError(t, err)
	var golden map[string]struct {
		Hosts []string `json:"hosts"`
	}
	require.NoError(t, json.Unmarshal(raw, &golden))
	require.NotEmpty(t, golden)

	data, err := inv.JSON()
	require.NoError(t, err)
	var got map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(data, &got))
	for group, want := range golden {
		require.Contains(t, got, group, "group %s of release N-1's inventory is missing from release N's", group)
		var g struct {
			Hosts []string `json:"hosts"`
		}
		require.NoError(t, json.Unmarshal(got[group], &g), group)
		for _, host := range want.Hosts {
			assert.True(t, slices.Contains(g.Hosts, host), "group %s: host %s of release N-1's inventory is missing from release N's (got %v)", group, host, g.Hosts)
		}
	}
	for _, host := range golden["all"].Hosts {
		assert.Contains(t, inv.Meta.Hostvars, host, "host %s has no hostvars", host)
	}

	// Everything derived or left to the play is said, one NOTE per host and
	// fact, and nothing of it reaches the JSON ansible reads.
	for m := range snap.Machines {
		prefix := "machine " + m + ": the applied row has no roles field"
		assert.True(t, slices.ContainsFunc(inv.Notes, func(n string) bool { return strings.HasPrefix(n, prefix) }), "no roles NOTE for %s: %v", m, inv.Notes)
	}
	assert.Contains(t, inv.Notes, "machine bench-e: no beat (the machine is down, or its member has never beaten); listed from its row; the play gathers os, arch and cores from its facts")
	assert.NotContains(t, string(data), "NOTE")
}
