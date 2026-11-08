package main

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// postgres_play_test.go pins the postgres role's role list as data (nova_pg_roles): each entry a role, the schema it
// owns (or none), the nova-secrets name its password is sealed as (or none) and the seats that name is granted to.
// The default is the three nova-config has always had, so a run without -e behaves as before; another tool (nova-sprint)
// adds its own role with -e and no change to the play. The run through ansible is postgres_play_functional_test.go.

type pgRole struct {
	Role   string   `yaml:"role"`
	Schema string   `yaml:"schema"`
	Secret string   `yaml:"secret"`
	Seats  []string `yaml:"seats"`
	Flags  string   `yaml:"flags"`
	Want   string   `yaml:"want"`
}

// The default list is exactly the three roles the play made before the list was data: nova_admin owns the database,
// nova_config owns schema config and its password is NOVA_PG_PASSWORD, nova_read reads; nothing is granted to a seat.
func TestPostgresRolesDefaultIsNovaConfigsThree(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("roles/postgres/defaults/main.yml")
	require.NoError(t, err)
	var d struct {
		Roles []pgRole `yaml:"nova_pg_roles"`
	}
	require.NoError(t, yaml.Unmarshal(b, &d))
	assert.Equal(t, []pgRole{
		{Role: "nova_admin", Seats: []string{}, Flags: "LOGIN CREATEDB CREATEROLE", Want: "t|t|t"},
		{Role: "nova_config", Schema: "config", Secret: "NOVA_PG_PASSWORD", Seats: []string{}},
		{Role: "nova_read", Seats: []string{}},
	}, d.Roles)
	assert.NotContains(t, string(b), "nova_pg_seal_", "the sealed role and its name are the list's secret field, not two variables beside it")
}

// No task names a role of the list where it makes passwords, roles, schemas or seals: those loop over the list. The
// grants of schema config stay nova-config's (the tables it loads need them) and the database is nova_admin's.
func TestPostgresRoleTasksLoopOverTheList(t *testing.T) {
	t.Parallel()
	b, err := os.ReadFile("roles/postgres/tasks/main.yml")
	require.NoError(t, err)
	var raw []any
	require.NoError(t, yaml.Unmarshal(b, &raw))
	looped := 0
	for _, task := range flattenTasks(t, mapsOf(t, raw)) {
		name := asText(task["name"])
		if strings.Contains(asText(task["loop"]), "nova_pg_roles") {
			t.Errorf("task %q loops over nova_pg_roles itself; the role loops over pg_roles, the list checked by roles.yml", name)
		}
		if strings.Contains(asText(task["loop"]), "pg_roles") {
			looped++
		}
		if strings.Contains(name, "grants") || strings.Contains(name, "database") || strings.Contains(name, "tables") {
			continue
		}
		body := asText(task)
		for _, lit := range []string{"nova_config", "nova_read", "'config'", "schema config"} {
			assert.NotContains(t, body, lit, "task %q names %s: it must come from the list", name, lit)
		}
	}
	assert.GreaterOrEqual(t, looped, 6, "passwords, roles, schemas and the proofs loop over pg_roles")
	assert.Contains(t, string(b), "include_tasks: roles.yml", "the list is checked before anything changes")

	play, err := os.ReadFile("postgres.yml")
	require.NoError(t, err)
	assert.Contains(t, string(play), "pg_roles | selectattr('secret')", "postgres.yml seals each role that names a secret")
	assert.Contains(t, string(play), "'grant'", "postgres.yml grants each sealed name to the role's seats")
}
