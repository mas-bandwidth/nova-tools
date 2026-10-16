package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// machineRoleDerivationCases is the one table migration 0042's derivation is
// held to, in both places it runs: in SQL over the configuration store
// (TestMachineRolesMigrationPreservesExistingGroups) and in Go over an applied
// row written before 0042 (TestDeriveMachineRolesIsMigration0042, through
// DeriveMachineRoles). Four machines: a plain bench with runners (a runner
// count is not a role), the fleet store, the fleet coordinator, and one the
// store and the coordinator may share.
var machineRoleDerivationCases = []struct {
	name        string
	store       string
	coordinator string
	want        map[string]string
}{
	{name: "separate references", store: "store-a", coordinator: "coord-a", want: map[string]string{
		"bench-a": "bench", "store-a": "bench,services", "coord-a": "bench,coordination", "shared": "bench",
	}},
	{name: "same reference", store: "shared", coordinator: "shared", want: map[string]string{
		"bench-a": "bench", "store-a": "bench", "coord-a": "bench", "shared": "bench,coordination,services",
	}},
	{name: "empty references", want: map[string]string{
		"bench-a": "bench", "store-a": "bench", "coord-a": "bench", "shared": "bench",
	}},
}

func TestDeriveMachineRolesIsMigration0042(t *testing.T) {
	t.Parallel()
	machine, ok := Lookup(KindMachine)
	require.True(t, ok)
	field, ok := machine.Field("roles")
	require.True(t, ok)
	for _, tc := range machineRoleDerivationCases {
		fleet := View{"store": tc.store, "coordinator": tc.coordinator}
		for name, want := range tc.want {
			got := DeriveMachineRoles(name, fleet)
			assert.Equal(t, want, got, "%s: %s", tc.name, name)
			canonical, err := field.Canonical(got)
			require.NoError(t, err)
			assert.Equal(t, canonical, got, "%s: %s: a derived list is canonical, as the inventory requires", tc.name, name)
		}
	}
}

func TestMachineRoleDefaultsAndValidation(t *testing.T) {
	t.Parallel()
	machine, ok := Lookup(KindMachine)
	require.True(t, ok)
	field, ok := machine.Field("roles")
	require.True(t, ok)
	assert.Equal(t, TypeList, field.Type)
	assert.Equal(t, MachineRoles, field.Enum)
	assert.Equal(t, DefaultMachineRoles, field.Default)
	assert.False(t, field.Required)

	row, err := machine.NewRow("m1", map[string]string{"user": "u", "seat": "s", "slots": "2"})
	require.NoError(t, err)
	assert.Equal(t, "bench", row.Fields["roles"], "an omitted role list defaults to the inventory's current bench membership")

	for _, role := range MachineRoles {
		got, err := field.Canonical(role)
		assert.NoError(t, err, "role %s", role)
		assert.Equal(t, role, got)
	}
	got, err := field.Canonical("services,bench,runner,coordination,bench")
	require.NoError(t, err)
	assert.Equal(t, "bench,coordination,runner,services", got, "roles are sorted and deduplicated")
	got, err = field.Canonical("")
	require.NoError(t, err)
	assert.Empty(t, got, "an empty list deliberately assigns no inventory role")
	_, err = field.Canonical("auditor")
	require.ErrorContains(t, err, "want a comma list of bench, coordination, runner, services")
}

func TestMachineViewPreservesMissingRolesApartFromExplicitEmpty(t *testing.T) {
	t.Parallel()
	base := map[string]string{"user": "u", "seat": "s", "slots": "1", "runners": "0"}
	missing := machineView(base, "1")
	_, hasRoles := missing["roles"]
	assert.False(t, hasRoles, "a legacy applied hash with no roles field is not silently converted to an empty list")

	withEmpty := make(map[string]string, len(base)+1)
	for key, value := range base {
		withEmpty[key] = value
	}
	withEmpty["roles"] = ""
	explicit := machineView(withEmpty, "1")
	roles, hasRoles := explicit["roles"]
	assert.True(t, hasRoles)
	assert.Empty(t, roles, "an explicit empty role list remains valid")
}
