package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRunnerEnvRoundTrip: a machine's runner_env (the environment its GitHub Actions runners' services start with,
// which the github-runners play reads from the inventory) is stored as one sorted JSON object, shows and lists as
// that, writes nothing when set again, and reaches the inventory as nova_runner_env with every value unsafe; an empty
// one is left out, as the service env is.
func TestRunnerEnvRoundTrip(t *testing.T) {
	t.Parallel()
	h := fleet(t)
	env := `{"ImageOS":"ubuntu24","PATH":"/home/alice/sdk/bin:/usr/bin:/bin"}`
	h.ok(t, "machine", "set", "bench-b", "--runner_env", `{"PATH": "/home/alice/sdk/bin:/usr/bin:/bin", "ImageOS": "ubuntu24"}`, "--actor", "t")
	assert.Contains(t, h.ok(t, "machine", "show", "bench-b"), " runner_env="+env+" ")
	assert.Contains(t, h.ok(t, "machine", "list"), " runner_env="+env+" ")
	assert.Contains(t, h.ok(t, "machine", "show", "bench-b", "--json"), `"runner_env":`+env)
	assert.Contains(t, h.ok(t, "machine", "set", "bench-b", "--runner_env", env, "--actor", "t"), "changed=none")

	_, inv, _ := h.run(t, "inventory", "--host", "bench-b")
	assert.Contains(t, strings.Join(strings.Fields(inv), ""),
		`"nova_runner_env":{"ImageOS":{"__ansible_unsafe":"ubuntu24"},"PATH":{"__ansible_unsafe":"/home/alice/sdk/bin:/usr/bin:/bin"}}`)
	_, inv, _ = h.run(t, "inventory", "--host", "bench-a")
	assert.NotContains(t, inv, "nova_runner_env", "an empty environment is left out of the inventory")

	h.ok(t, "machine", "set", "bench-b", "--runner_env", "", "--actor", "t")
	assert.Contains(t, h.ok(t, "machine", "show", "bench-b"), " runner_env={} ")
	_, inv, _ = h.run(t, "inventory", "--host", "bench-b")
	assert.NotContains(t, inv, "nova_runner_env", "cleared is none")

	for _, bad := range []string{`["PATH"]`, `{"1X":"a"}`, `{"A B":"a"}`, `{"PATH":1}`, `not json`} {
		code, _, errs := h.run(t, "machine", "set", "bench-b", "--runner_env", bad, "--actor", "t")
		assert.NotEqual(t, 0, code, bad)
		assert.Contains(t, errs, "--runner_env", bad)
	}
}

// An environment name is a POSIX one (letters, digits and underscores, not starting with a digit), in either case:
// ImageOS is what actions/setup-* read, UseSharedCompilation what msbuild reads.
func TestEnvNamesAreMixedCase(t *testing.T) {
	t.Parallel()
	f := kinds["service"].findField("env")
	got, err := f.canonical(`{"UseSharedCompilation": "false", "ImageOS": "ubuntu24", "_X": "1"}`)
	require.NoError(t, err)
	assert.Equal(t, `{"ImageOS":"ubuntu24","UseSharedCompilation":"false","_X":"1"}`, got)
	_, err = f.canonical(`{"9X": "1"}`)
	assert.Error(t, err)
	_, err = f.canonical(`{"A-B": "1"}`)
	assert.Error(t, err)
}

// A corrupt value in the store (written by hand) refuses the inventory and names the fix.
func TestRunnerEnvCorruptRefusesTheInventory(t *testing.T) {
	t.Parallel()
	_, err := build([]Row{{Name: "m1", F: map[string]string{"runner_env": "nope"}}}, nil, "")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "nova-config machine set m1 --runner_env")
}
