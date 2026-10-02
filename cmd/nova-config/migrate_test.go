package main

import (
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mixedCatalog is schema config as the fleet's store was found on
// 2026-10-02: six tables the admin role made at setup, three the config
// role made later; the config role runs migrate.
func mixedCatalog() config.Ownership {
	o := config.Ownership{Role: "nova_config", SchemaOwner: "nova_admin", Create: true, Tables: map[string]string{}}
	for _, t := range []string{"fleet", "friends", "history", "machines", "schema_migrations", "sprint"} {
		o.Tables[t] = "nova_admin"
	}
	for _, t := range []string{"loops", "routes", "tiers"} {
		o.Tables[t] = "nova_config"
	}
	return o
}

// The refusal for the measured mixed case, exactly: the role, the
// migrations, the rule, each owner with its tables, and one ALTER per table.
const mixedRefusal = "nova-config migrate: role nova_config cannot apply migrations 10 to 11 and applied none: " +
	"the role that runs migrate must own every table in schema config and be able to create in it, " +
	"and nova_admin owns config.fleet, config.friends, config.history, config.machines, config.schema_migrations, config.sprint, " +
	"so a role with the owners' rights runs this once, in psql; run: " +
	"ALTER TABLE config.fleet OWNER TO nova_config; ALTER TABLE config.friends OWNER TO nova_config; " +
	"ALTER TABLE config.history OWNER TO nova_config; ALTER TABLE config.machines OWNER TO nova_config; " +
	"ALTER TABLE config.schema_migrations OWNER TO nova_config; ALTER TABLE config.sprint OWNER TO nova_config;\n"

func TestMigrateRefusesBeforeApplyingWhenTheRoleDoesNotOwnTheTables(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.env["NOVA_PG_DSN"] = dsn
	h.store.version = 9
	h.store.Catalog = mixedCatalog()
	code, out, errs := h.run(t, "migrate")
	require.Equal(t, 1, code, "stdout %q stderr %q", out, errs)
	assert.Equal(t, "", out)
	assert.Equal(t, mixedRefusal, errs)
	assert.Equal(t, 9, h.store.version, "the refusal applied a migration")

	code, out, errs = h.run(t, "migrate", "--dry-run")
	require.Equal(t, 1, code, "dry-run ready=no: stdout %q stderr %q", out, errs)
	assert.Equal(t, "", errs)
	assert.Equal(t, "MIGRATE PENDING version=10 file=0010_sprint_reader_tier.sql\n"+
		"MIGRATE PENDING version=11 file=0011_fleet_endpoints.sql\n"+
		"MIGRATE NOT-OWNED table=config.fleet owner=nova_admin role=nova_config\n"+
		"MIGRATE NOT-OWNED table=config.friends owner=nova_admin role=nova_config\n"+
		"MIGRATE NOT-OWNED table=config.history owner=nova_admin role=nova_config\n"+
		"MIGRATE NOT-OWNED table=config.machines owner=nova_admin role=nova_config\n"+
		"MIGRATE NOT-OWNED table=config.schema_migrations owner=nova_admin role=nova_config\n"+
		"MIGRATE NOT-OWNED table=config.sprint owner=nova_admin role=nova_config\n"+
		"MIGRATE WOULD-REFUSE "+mixedRefusal[len("nova-config migrate: "):]+
		"CONFIG MIGRATE pg=nova_config@127.0.0.1:5432/nova dry-run=true role=nova_config from=9 pending=2 ready=no\n", out)
	assert.Equal(t, 9, h.store.version, "the dry run applied a migration")

	for tb := range h.store.Catalog.Tables {
		h.store.Catalog.Tables[tb] = "nova_config"
	}
	code, out, errs = h.run(t, "migrate", "--dry-run")
	require.Equal(t, 0, code, "dry-run ready=yes: stdout %q stderr %q", out, errs)
	assert.True(t, strings.HasSuffix(out, " from=9 pending=2 ready=yes\n"), "dry-run after the ALTER lines: %q", out)
	code, out, errs = h.run(t, "migrate")
	require.Equal(t, 0, code, "after the ALTER lines: stdout %q stderr %q", out, errs)
	assert.Equal(t, "CONFIG MIGRATE pg=nova_config@127.0.0.1:5432/nova from=9 to=11 applied=2\n", out)
}

func TestMigratePreflightPassesWhatTheRuleAllows(t *testing.T) {
	t.Parallel()

	fresh := config.Ownership{Role: "nova_config", Tables: map[string]string{}}
	for _, tc := range []struct {
		name    string
		version int
		catalog config.Ownership
		want    string
		dry     string
	}{
		{"nothing pending, mixed owners", 11, mixedCatalog(), "from=11 to=11 applied=0",
			"MIGRATE NOT-OWNED table=config.sprint owner=nova_admin role=nova_config\nCONFIG MIGRATE pg=nova_config@127.0.0.1:5432/nova dry-run=true role=nova_config from=11 pending=0 ready=yes\n"},
		{"a fresh empty database", 0, fresh, "from=0 to=11 applied=11",
			"MIGRATE PENDING version=11 file=0011_fleet_endpoints.sql\nCONFIG MIGRATE pg=nova_config@127.0.0.1:5432/nova dry-run=true role=nova_config from=0 pending=11 ready=yes\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness()
			h.env["NOVA_PG_DSN"] = dsn
			h.store.version = tc.version
			h.store.Catalog = tc.catalog
			code, out, errs := h.run(t, "migrate", "--dry-run")
			require.Equal(t, 0, code, "dry-run: stdout %q stderr %q", out, errs)
			assert.Contains(t, out, tc.dry)
			assert.NotContains(t, out, "WOULD-REFUSE")
			code, out, errs = h.run(t, "migrate")
			require.Equal(t, 0, code, "stdout %q stderr %q", out, errs)
			assert.Equal(t, "CONFIG MIGRATE pg=nova_config@127.0.0.1:5432/nova "+tc.want+"\n", out)
		})
	}
}
