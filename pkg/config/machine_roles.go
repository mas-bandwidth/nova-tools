package config

import "strings"

// DeriveMachineRoles is migration 0042's derivation of a machine's inventory
// roles from the rows a store held before 0042 existed: every machine is a
// bench, the fleet row's coordinator also carries coordination, and the
// fleet row's store also carries services. A runner is never derived (the
// runners count is not a role). The result is canonical (sorted, the order
// the roles field's Canonical gives).
//
// 0042 (migrations/0042_machine_roles.sql) runs this rule in SQL over the
// configuration store; the inventory runs it here over an applied machine row
// written before 0042, which has no roles field, so a release's adopt reads
// the store the previous release leaves (docs/SPEC-RELEASE.md). The two are one
// rule held by one table of cases, machineRoleDerivationCases: the SQL is
// proven against it by TestMachineRolesMigrationPreservesExistingGroups and
// this by TestDeriveMachineRolesIsMigration0042, so a change to either that
// the other does not make fails a test.
func DeriveMachineRoles(machine string, fleet View) string {
	roles := []string{DefaultMachineRoles}
	if fleet["coordinator"] != "" && fleet["coordinator"] == machine {
		roles = append(roles, "coordination")
	}
	if fleet["store"] != "" && fleet["store"] == machine {
		roles = append(roles, "services")
	}
	return strings.Join(roles, ",")
}
