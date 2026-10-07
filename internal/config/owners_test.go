package config

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// measured is schema config as the fleet's store was found on 2026-10-02:
// the early tables made by the admin role at setup, the later ones by the
// config role, which runs migrate from the install play.
func measured() Ownership {
	o := Ownership{Role: "nova_config", SchemaOwner: "nova_admin", Create: true, Tables: map[string]string{}}
	for _, t := range []string{"fleet", "friends", "history", "machines", "schema_migrations", "sprint"} {
		o.Tables[t] = "nova_admin"
	}
	for _, t := range []string{"loops", "routes", "tiers"} {
		o.Tables[t] = "nova_config"
	}
	return o
}

func TestMigrateGapsIsTheOwnersRule(t *testing.T) {
	t.Parallel()

	pending := []Migration{{Version: 13, Name: "0013_x.sql"}, {Version: 14, Name: "0014_y.sql"}}
	owned := measured()
	for tb := range owned.Tables {
		owned.Tables[tb] = "nova_config"
	}
	noCreate := Ownership{Role: "nova_config", SchemaOwner: "postgres", Tables: map[string]string{"fleet": "nova_config"}}
	admin := []Gap{{"fleet", "nova_admin"}, {"friends", "nova_admin"}, {"history", "nova_admin"}, {"machines", "nova_admin"}, {"schema_migrations", "nova_admin"}, {"sprint", "nova_admin"}}
	for _, tc := range []struct {
		name    string
		o       Ownership
		pending []Migration
		want    []Gap
	}{
		{"the role owns every table", owned, pending, nil},
		{"the measured mixed ownership", measured(), pending, admin},
		{"a fresh empty database", Ownership{Role: "nova_config", Tables: map[string]string{}}, pending, nil},
		{"nothing pending, mixed", measured(), nil, nil},
		{"nothing pending, owned", owned, nil, nil},
		{"no create on the schema", noCreate, pending, []Gap{{"", "postgres"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, MigrateGaps(tc.o, tc.pending))
		})
	}
}

func TestGapsAreFoundWhateverIsPending(t *testing.T) {
	t.Parallel()

	assert.Len(t, Gaps(measured()), 6, "the finding dry-run prints does not wait for a pending migration")
	assert.Empty(t, Gaps(Ownership{Role: "x"}), "no schema yet has nothing to own")
}

func TestGapRemedyIsOneStatementPerObject(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		gap  Gap
		role string
		want string
	}{
		{"plain identifiers", Gap{"fleet", "nova_admin"}, "nova_config", `ALTER TABLE config."fleet" OWNER TO "nova_config";`},
		{"schema owner", Gap{"", "postgres"}, "nova_config", `ALTER SCHEMA config OWNER TO "nova_config";`},
		{"spaced role", Gap{"fleet", "nova_admin"}, "Odd Role", `ALTER TABLE config."fleet" OWNER TO "Odd Role";`},
		{"reserved role", Gap{"fleet", "nova_admin"}, "current_user", `ALTER TABLE config."fleet" OWNER TO "current_user";`},
		{"reserved table", Gap{"select", "nova_admin"}, "nova_config", `ALTER TABLE config."select" OWNER TO "nova_config";`},
		{"embedded quotes", Gap{`ta"ble`, "nova_admin"}, `ro"le`, `ALTER TABLE config."ta""ble" OWNER TO "ro""le";`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.gap.Remedy(tc.role))
		})
	}
}

func TestPendingIsWhatTheLedgerLacks(t *testing.T) {
	t.Parallel()

	all := []Migration{{Version: 1}, {Version: 2}, {Version: 3}}
	assert.Equal(t, []Migration{{Version: 2}, {Version: 3}}, Pending(all, 1))
	assert.Empty(t, Pending(all, 3))
	assert.Equal(t, all, Pending(all, 0))
}

func TestMemOwnershipIsSetByATestAndHandedOutAsACopy(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	m := NewMem()
	m.Catalog = measured()
	got, err := m.Ownership(ctx)
	require.NoError(t, err)
	assert.Equal(t, measured(), got)
	got.Tables["loops"] = "someone"
	again, err := m.Ownership(ctx)
	require.NoError(t, err)
	assert.Equal(t, "nova_config", again.Tables["loops"], "the store shares its owners with the caller")
}
