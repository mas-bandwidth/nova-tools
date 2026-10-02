package main

import (
	"context"
	"encoding/json"
	"fmt"
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

// mixedWhy and mixedRemedy are the refusal for the measured case, exactly:
// the ledger at 15, migration 16 pending, the role, the rule, the owner with
// its tables, and one ALTER per table.
const (
	mixedWhy = "role nova_config cannot apply migration 16 and applied none: " +
		"the role that runs migrate must own every table in schema config and be able to create in it, " +
		"and nova_admin owns config.fleet, config.friends, config.history, config.machines, config.schema_migrations, config.sprint, " +
		"so a role with the owners' rights runs this once, in psql"
	mixedRemedy = `ALTER TABLE config."fleet" OWNER TO "nova_config"; ALTER TABLE config."friends" OWNER TO "nova_config"; ` +
		`ALTER TABLE config."history" OWNER TO "nova_config"; ALTER TABLE config."machines" OWNER TO "nova_config"; ` +
		`ALTER TABLE config."schema_migrations" OWNER TO "nova_config"; ALTER TABLE config."sprint" OWNER TO "nova_config";`
)

// mixedHarness is the measured store: ledger at 15, the mixed owners.
func mixedHarness(t *testing.T) *harness {
	t.Helper()
	require.Equal(t, 16, currentSchema(), "the measured case is the ledger at 15 and 0016 pending")
	h := newHarness()
	h.env["NOVA_PG_DSN"] = dsn
	h.store.version = 15
	h.store.Catalog = mixedCatalog()
	return h
}

func TestMigrateRefusesBeforeApplyingWhenTheRoleDoesNotOwnTheTables(t *testing.T) {
	t.Parallel()

	h := mixedHarness(t)
	code, out, errs := h.run(t, "migrate")
	require.Equal(t, 1, code, "stdout %q stderr %q", out, errs)
	assert.Equal(t, "", out)
	assert.Equal(t, "nova-config migrate REFUSED: "+mixedWhy+"; run: "+mixedRemedy+"\n", errs)
	assert.Equal(t, 15, h.store.version, "the refusal applied a migration")

	for tb := range h.store.Catalog.Tables {
		h.store.Catalog.Tables[tb] = "nova_config"
	}
	code, out, errs = h.run(t, "migrate")
	require.Equal(t, 0, code, "after the ALTER lines: stdout %q stderr %q", out, errs)
	assert.Equal(t, "CONFIG MIGRATE pg=nova_config@127.0.0.1:5432/nova from=15 to=16 applied=1\n", out)
}

func TestMigrateDryRunPrintsTheOwnershipFindingAndExitsOneWhenNotReady(t *testing.T) {
	t.Parallel()

	h := mixedHarness(t)
	code, out, errs := h.run(t, "migrate", "--dry-run")
	require.Equal(t, 1, code, "dry-run ready=no: stdout %q stderr %q", out, errs)
	assert.Equal(t, "", errs, "nothing was attempted: no refusal line")
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	require.Len(t, lines, 16+6+2, out)
	assert.Equal(t, "MIGRATION version=15 file=0015_row_notes.sql lines=", lines[14][:len("MIGRATION version=15 file=0015_row_notes.sql lines=")])
	assert.True(t, strings.HasSuffix(lines[14], " state=applied"), lines[14])
	assert.True(t, strings.HasPrefix(lines[15], "MIGRATION version=16 file=0016_machine_width_default.sql lines="), lines[15])
	assert.True(t, strings.HasSuffix(lines[15], " state=pending"), lines[15])
	assert.Equal(t, []string{
		"MIGRATE NOT-OWNED table=config.fleet owner=nova_admin role=nova_config",
		"MIGRATE NOT-OWNED table=config.friends owner=nova_admin role=nova_config",
		"MIGRATE NOT-OWNED table=config.history owner=nova_admin role=nova_config",
		"MIGRATE NOT-OWNED table=config.machines owner=nova_admin role=nova_config",
		"MIGRATE NOT-OWNED table=config.schema_migrations owner=nova_admin role=nova_config",
		"MIGRATE NOT-OWNED table=config.sprint owner=nova_admin role=nova_config",
		"MIGRATE WOULD-REFUSE " + mixedWhy + "; run: " + mixedRemedy,
		"CONFIG MIGRATE pg=nova_config@127.0.0.1:5432/nova from=15 to=16 applied=0 dry_run=true pending=1 missing=0 role=nova_config ready=no",
	}, lines[16:])
	assert.Equal(t, 15, h.store.version, "the dry run applied a migration")

	code, out, errs = h.run(t, "migrate", "--dry-run", "--json")
	require.Equal(t, 1, code, "dry-run --json ready=no: stdout %q stderr %q", out, errs)
	assert.Contains(t, out, `"status":"failed"`)
	assert.Contains(t, out, `"ready":"no"`)
	assert.Contains(t, out, `"kind":"not_owned"`)

	for tb := range h.store.Catalog.Tables {
		h.store.Catalog.Tables[tb] = "nova_config"
	}
	code, out, errs = h.run(t, "migrate", "--dry-run")
	require.Equal(t, 0, code, "dry-run ready=yes: stdout %q stderr %q", out, errs)
	assert.True(t, strings.HasSuffix(out, " from=15 to=16 applied=0 dry_run=true pending=1 missing=0 role=nova_config ready=yes\n"), out)
	assert.NotContains(t, out, "MIGRATE NOT-OWNED")
	assert.NotContains(t, out, "WOULD-REFUSE")
}

func TestMigratePreflightPassesWhatTheRuleAllows(t *testing.T) {
	t.Parallel()

	n := currentSchema()
	for _, tc := range []struct {
		name    string
		version int
		catalog config.Ownership
		dry     string
	}{
		{"nothing pending, mixed owners", n, mixedCatalog(),
			"MIGRATE NOT-OWNED table=config.sprint owner=nova_admin role=nova_config\n" +
				fmt.Sprintf("CONFIG MIGRATE pg=nova_config@127.0.0.1:5432/nova from=%d to=%d applied=0 dry_run=true pending=0 missing=0 role=nova_config ready=yes\n", n, n)},
		{"a fresh empty database", 0, config.Ownership{Role: "nova_config", Tables: map[string]string{}},
			fmt.Sprintf("CONFIG MIGRATE pg=nova_config@127.0.0.1:5432/nova from=0 to=%d applied=0 dry_run=true pending=%d missing=0 role=nova_config ready=yes\n", n, n)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness()
			h.env["NOVA_PG_DSN"] = dsn
			h.store.version = tc.version
			h.store.Catalog = tc.catalog
			code, out, errs := h.run(t, "migrate", "--dry-run")
			require.Equal(t, 0, code, "dry-run: stdout %q stderr %q", out, errs)
			assert.True(t, strings.HasSuffix(out, tc.dry), out)
			assert.NotContains(t, out, "WOULD-REFUSE")
			code, out, errs = h.run(t, "migrate")
			require.Equal(t, 0, code, "stdout %q stderr %q", out, errs)
			assert.Equal(t, fmt.Sprintf("CONFIG MIGRATE pg=nova_config@127.0.0.1:5432/nova from=%d to=%d applied=%d\n", tc.version, n, n-tc.version), out)
		})
	}
}

// advancingMigrationStore is a strict dry-run seam: Version reads 15, then
// another migrator records 16 before Applied reads the ledger. Any DDL call
// is a defect; the read order is part of the regression's witness.
type advancingMigrationStore struct {
	*memStore
	reads    []string
	ddlCalls int
}

func (s *advancingMigrationStore) Version(ctx context.Context) (int, error) {
	s.reads = append(s.reads, "version")
	return s.memStore.Version(ctx)
}

func (s *advancingMigrationStore) Ownership(ctx context.Context) (config.Ownership, error) {
	s.reads = append(s.reads, "ownership")
	return s.memStore.Ownership(ctx)
}

func (s *advancingMigrationStore) Applied(ctx context.Context) ([]int, error) {
	s.reads = append(s.reads, "applied")
	s.version = 16
	return s.memStore.Applied(ctx)
}

func (s *advancingMigrationStore) Migrate(context.Context) (int, int, []int, error) {
	s.ddlCalls++
	return 0, 0, nil, fmt.Errorf("dry-run attempted DDL")
}

// Readiness and the rendered ledger share one snapshot (docs/SPEC-CONFIG.md,
// "The schema"): mixed owners cannot block a dry run with nothing pending, even
// when the preliminary Version read finds a migration pending.
func TestMigrateDryRunUsesTheAdvancingLedgerForReadiness(t *testing.T) {
	t.Parallel()
	for _, format := range []string{"text", "json"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			h := mixedHarness(t)
			st := &advancingMigrationStore{memStore: h.store}
			h.override = st
			args := []string{"migrate", "--dry-run"}
			if format == "json" {
				args = append(args, "--json")
			}
			code, out, errs := h.run(t, args...)
			require.Equal(t, 0, code, "stdout: %s\nstderr: %s", out, errs)
			assert.Empty(t, errs)
			assert.Equal(t, []string{"version", "ownership", "applied"}, st.reads)
			assert.Zero(t, st.ddlCalls)
			assert.Zero(t, h.redis.opens)
			if format == "text" {
				assert.Contains(t, out, "MIGRATION version=16 file=0016_machine_width_default.sql")
				assert.NotContains(t, out, "state=pending")
				assert.NotContains(t, out, "WOULD-REFUSE")
				assert.Contains(t, out, "MIGRATE NOT-OWNED table=config.fleet")
				assert.Contains(t, out, "from=16 to=16 applied=0 dry_run=true pending=0 missing=0 role=nova_config ready=yes")
				return
			}
			var result struct {
				Result struct {
					Status string `json:"status"`
					Exit   int    `json:"exit"`
					Remedy string `json:"remedy"`
				} `json:"result"`
				Facts map[string]any `json:"facts"`
				Items []struct {
					Kind   string         `json:"kind"`
					Fields map[string]any `json:"fields"`
				} `json:"items"`
			}
			require.NoError(t, json.Unmarshal([]byte(out), &result))
			assert.Equal(t, "ok", result.Result.Status)
			assert.Zero(t, result.Result.Exit)
			assert.Empty(t, result.Result.Remedy)
			assert.EqualValues(t, 16, result.Facts["from"])
			assert.EqualValues(t, 0, result.Facts["pending"])
			assert.EqualValues(t, 0, result.Facts["applied"])
			assert.Equal(t, "yes", result.Facts["ready"])
			migrations, notOwned := 0, 0
			for _, item := range result.Items {
				switch item.Kind {
				case "migration":
					migrations++
					assert.Equal(t, "applied", item.Fields["state"])
				case "not_owned":
					notOwned++
				}
			}
			assert.Equal(t, 16, migrations)
			assert.Equal(t, 6, notOwned)
		})
	}
}
