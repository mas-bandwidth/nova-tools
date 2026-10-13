package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
