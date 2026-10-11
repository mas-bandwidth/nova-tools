package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/config"
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
// the ledger one behind the newest migration, that one pending, the role, the
// rule, the owner with its tables, and one ALTER per table.
var (
	mixedWhy = "role nova_config cannot apply migration " + fmt.Sprint(currentSchema()) + " and applied none: " +
		"the role that runs migrate must own every table in schema config and be able to create in it, " +
		"and nova_admin owns config.fleet, config.friends, config.history, config.machines, config.schema_migrations, config.sprint, " +
		"so a role with the owners' rights runs this once, in psql"
	mixedRemedy = `ALTER TABLE config."fleet" OWNER TO "nova_config"; ALTER TABLE config."friends" OWNER TO "nova_config"; ` +
		`ALTER TABLE config."history" OWNER TO "nova_config"; ALTER TABLE config."machines" OWNER TO "nova_config"; ` +
		`ALTER TABLE config."schema_migrations" OWNER TO "nova_config"; ALTER TABLE config."sprint" OWNER TO "nova_config";`
)

// mixedHarness is the measured store (found at the ledger at 13 and 0014
// pending): the ledger one behind the newest migration, the mixed owners.
func mixedHarness(t *testing.T) *harness {
	t.Helper()
	h := newHarness()
	h.env["NOVA_PG_DSN"] = dsn
	h.store.version = currentSchema() - 1
	h.store.Catalog = mixedCatalog()
	return h
}

// migrate --print prints each migration's SQL under its MIGRATION line,
// bounded by --max with a MORE line when a migration is cut, so a reader
// reads the schema instead of a line count (docs/STANDARD.md, section 2,
// "Output is bounded and keeps its totals").
func TestMigratePrintPrintsEachMigrationsSQLBoundedByMax(t *testing.T) {
	t.Parallel()

	all, err := config.Migrations()
	require.NoError(t, err)
	require.NotEmpty(t, all)
	sql := strings.Split(strings.TrimSuffix(all[0].SQL, "\n"), "\n")
	require.Greater(t, len(sql), 2, "the first migration is too short to cut")

	t.Run("text", func(t *testing.T) {
		t.Parallel()
		for _, tc := range []struct {
			name  string
			max   string
			shown int
		}{
			{"a ceiling cuts and names the rest", "2", 2},
			{"zero prints the migration whole", "0", len(sql)},
		} {
			t.Run(tc.name, func(t *testing.T) {
				t.Parallel()
				h := newHarness()
				code, out, errs := h.run(t, "migrate", "--print", "--max", tc.max)
				require.Equal(t, 0, code, "stdout %q stderr %q", out, errs)
				assert.Empty(t, errs)
				lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
				require.Greater(t, len(lines), tc.shown)
				assert.True(t, strings.HasPrefix(lines[0], fmt.Sprintf("MIGRATION version=1 file=0001_schema.sql lines=%d", len(sql))), lines[0])
				for i := 0; i < tc.shown; i++ {
					assert.Equal(t, sql[i], lines[1+i], "SQL line %d", i+1)
				}
				maybeMore := lines[1+tc.shown]
				if tc.shown < len(sql) {
					assert.True(t, strings.HasPrefix(maybeMore, fmt.Sprintf("MIGRATE MORE kind=sql shown=%d total=%d", tc.shown, len(sql))), maybeMore)
				} else {
					assert.False(t, strings.HasPrefix(maybeMore, "MIGRATE MORE"), maybeMore)
				}
				assert.True(t, strings.HasSuffix(out, fmt.Sprintf("CONFIG MIGRATE print=%d pg=-\n", currentSchema())), out)
			})
		}
	})

	t.Run("json", func(t *testing.T) {
		t.Parallel()
		h := newHarness()
		code, out, errs := h.run(t, "migrate", "--print", "--max", "2", "--json")
		require.Equal(t, 0, code, "stdout %q stderr %q", out, errs)
		assert.Empty(t, errs)
		var result struct {
			Items []struct {
				Kind   string         `json:"kind"`
				Fields map[string]any `json:"fields"`
			} `json:"items"`
		}
		require.NoError(t, json.Unmarshal([]byte(out), &result))
		require.Len(t, result.Items, currentSchema())
		fields := result.Items[0].Fields
		assert.EqualValues(t, 2, fields["sql_shown"])
		assert.EqualValues(t, len(sql), fields["sql_lines"])
		assert.Equal(t, strings.Join(sql[:2], "\n"), fields["sql"])
	})
}

func TestMigrateRefusesBeforeApplyingWhenTheRoleDoesNotOwnTheTables(t *testing.T) {
	t.Parallel()

	h := mixedHarness(t)
	code, out, errs := h.run(t, "migrate")
	require.Equal(t, 1, code, "stdout %q stderr %q", out, errs)
	assert.Equal(t, "", out)
	assert.Equal(t, "nova-config migrate REFUSED: "+mixedWhy+"; run: "+mixedRemedy+"\n", errs)
	assert.Equal(t, currentSchema()-1, h.store.version, "the refusal applied a migration")

	for tb := range h.store.Catalog.Tables {
		h.store.Catalog.Tables[tb] = "nova_config"
	}
	code, out, errs = h.run(t, "migrate")
	require.Equal(t, 0, code, "after the ALTER lines: stdout %q stderr %q", out, errs)
	assert.Equal(t, fmt.Sprintf("CONFIG MIGRATE pg=nova_config@127.0.0.1:5432/nova from=%d to=%d applied=1\n", currentSchema()-1, currentSchema()), out)
}

func TestMigrateDryRunPrintsTheOwnershipFindingAndExitsOneWhenNotReady(t *testing.T) {
	t.Parallel()

	h := mixedHarness(t)
	code, out, errs := h.run(t, "migrate", "--dry-run")
	require.Equal(t, 1, code, "dry-run ready=no: stdout %q stderr %q", out, errs)
	assert.Equal(t, "", errs, "nothing was attempted: no refusal line")
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	n := currentSchema()
	require.Len(t, lines, n+6+2, out)
	assert.True(t, strings.HasPrefix(lines[n-2], fmt.Sprintf("MIGRATION version=%d file=%04d_", n-1, n-1)), lines[n-2])
	assert.True(t, strings.HasSuffix(lines[n-2], " state=applied"), lines[n-2])
	assert.True(t, strings.HasPrefix(lines[n-1], fmt.Sprintf("MIGRATION version=%d file=%04d_", n, n)), lines[n-1])
	assert.True(t, strings.HasSuffix(lines[n-1], " state=pending"), lines[n-1])
	assert.Equal(t, []string{
		"MIGRATE NOT-OWNED table=config.fleet owner=nova_admin role=nova_config",
		"MIGRATE NOT-OWNED table=config.friends owner=nova_admin role=nova_config",
		"MIGRATE NOT-OWNED table=config.history owner=nova_admin role=nova_config",
		"MIGRATE NOT-OWNED table=config.machines owner=nova_admin role=nova_config",
		"MIGRATE NOT-OWNED table=config.schema_migrations owner=nova_admin role=nova_config",
		"MIGRATE NOT-OWNED table=config.sprint owner=nova_admin role=nova_config",
		"MIGRATE WOULD-REFUSE " + mixedWhy + "; run: " + mixedRemedy,
		fmt.Sprintf("CONFIG MIGRATE pg=nova_config@127.0.0.1:5432/nova from=%d to=%d applied=0 dry_run=true pending=1 missing=0 role=nova_config ready=no owner=mixed sessions=0", n-1, n),
	}, lines[n:])
	assert.Equal(t, n-1, h.store.version, "the dry run applied a migration")

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
	assert.True(t, strings.HasSuffix(out, fmt.Sprintf(" from=%d to=%d applied=0 dry_run=true pending=1 missing=0 role=nova_config ready=yes owner=mixed sessions=0\n", n-1, n)), out)
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
				fmt.Sprintf("CONFIG MIGRATE pg=nova_config@127.0.0.1:5432/nova from=%d to=%d applied=0 dry_run=true pending=0 missing=0 role=nova_config ready=yes owner=mixed sessions=0\n", n, n)},
		{"a fresh empty database", 0, config.Ownership{Role: "nova_config", Tables: map[string]string{}},
			fmt.Sprintf("CONFIG MIGRATE pg=nova_config@127.0.0.1:5432/nova from=0 to=%d applied=0 dry_run=true pending=%d missing=0 role=nova_config ready=yes owner=none sessions=0\n", n, n)},
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

// advancingMigrationStore is a strict dry-run seam: Version reads one behind
// the newest, then another migrator records the newest before Applied reads
// the ledger. Any DDL call
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
	s.version = currentSchema()
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
				assert.Contains(t, out, fmt.Sprintf("MIGRATION version=%d file=%04d_", currentSchema(), currentSchema()))
				assert.NotContains(t, out, "state=pending")
				assert.NotContains(t, out, "WOULD-REFUSE")
				assert.Contains(t, out, "MIGRATE NOT-OWNED table=config.fleet")
				assert.Contains(t, out, fmt.Sprintf("from=%d to=%d applied=0 dry_run=true pending=0 missing=0 role=nova_config ready=yes", currentSchema(), currentSchema()))
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
			assert.EqualValues(t, currentSchema(), result.Facts["from"])
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
			assert.Equal(t, currentSchema(), migrations)
			assert.Equal(t, 6, notOwned)
		})
	}
}

// ownedWholeByAnother is the store as the fleet's was found on 2026-10-08:
// nova_config owns schema config and every table in it, and migrate is run
// as nova_admin (the seat's DSN). The refusal names nova_config as the role
// that runs it and gives that command, never an ALTER OWNER: the store is
// not misowned, the run is as the wrong role.
func TestMigrateRefusalNamesTheOwningRoleAndItsCommand(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.env["NOVA_PG_DSN"] = "postgres://nova_admin@127.0.0.1:5432/nova"
	h.store.version = currentSchema() - 1
	o := config.Ownership{Role: "nova_admin", SchemaOwner: "nova_config", Create: false, Tables: map[string]string{}}
	for tb := range mixedCatalog().Tables {
		o.Tables[tb] = "nova_config"
	}
	h.store.Catalog = o

	why := fmt.Sprintf("role nova_admin cannot apply migration %d and applied none: role nova_config owns schema config and every table in it, "+
		"and only the owner alters and fills them, so nova_config runs this once", currentSchema())
	remedy := "NOVA_PG_PASSWORD_ENV=NOVA_PG_CONFIG_PASSWORD nova-config migrate --pg postgres://nova_config@127.0.0.1:5432/nova " +
		"(as nova_config, the owner, with its password in the variable NOVA_PG_CONFIG_PASSWORD names)"

	code, out, errs := h.run(t, "migrate")
	require.Equal(t, 1, code, "stdout %q stderr %q", out, errs)
	assert.Equal(t, "", out)
	assert.Equal(t, "nova-config migrate REFUSED: "+why+"; run: "+remedy+"\n", errs)
	assert.NotContains(t, errs, "ALTER", "the owner runs migrate; nothing is re-owned")
	assert.Equal(t, currentSchema()-1, h.store.version, "the refusal applied a migration")

	code, out, errs = h.run(t, "migrate", "--dry-run")
	require.Equal(t, 1, code, "dry-run ready=no: stdout %q stderr %q", out, errs)
	assert.Contains(t, out, "MIGRATE WOULD-REFUSE "+why+"; run: "+remedy+"\n")
	assert.Contains(t, out, "role=nova_admin ready=no")

	// as the owner, the same store migrates
	h.env["NOVA_PG_DSN"] = dsn
	h.store.Catalog.Role = "nova_config"
	h.store.Catalog.Create = true
	code, out, errs = h.run(t, "migrate")
	require.Equal(t, 0, code, "as the owner: stdout %q stderr %q", out, errs)
	assert.Equal(t, fmt.Sprintf("CONFIG MIGRATE pg=nova_config@127.0.0.1:5432/nova from=%d to=%d applied=1\n", currentSchema()-1, currentSchema()), out)
}

// The seat play migrates inside its stopped window, as the owner, and asks
// migrate to refuse while anything else holds the database (--window): a
// server or member that ps missed must never run under a migration it does
// not tolerate. Without the flag migrate is as before (the bootstrap, the
// store_deployer path), and the dry run names the sessions without refusing.
func TestMigrateWindowRefusesWhileAnotherNovaSessionHoldsTheDatabase(t *testing.T) {
	t.Parallel()

	h := newHarness()
	h.env["NOVA_PG_DSN"] = dsn
	h.store.version = currentSchema() - 1
	h.store.Held = []config.Session{{Role: "nova_admin", PID: 4242, Application: "nova-sprint"}, {Role: "nova_admin", PID: 4243}}

	code, out, errs := h.run(t, "migrate", "--window")
	require.Equal(t, 1, code, "stdout %q stderr %q", out, errs)
	assert.Equal(t, "", out)
	assert.Equal(t, "nova-config migrate REFUSED: role nova_config applied none: --window says the old server and member are stopped, "+
		"and 2 other nova session(s) hold the database: nova_admin pid=4242 app=nova-sprint, nova_admin pid=4243; run: "+windowRemedy+"\n", errs)
	assert.Equal(t, currentSchema()-1, h.store.version, "the refusal applied a migration")

	// the dry run says who holds the database and refuses nothing
	code, out, errs = h.run(t, "migrate", "--dry-run", "--window")
	require.Equal(t, 0, code, "dry-run: stdout %q stderr %q", out, errs)
	assert.Contains(t, out, "MIGRATE SESSION role=nova_admin pid=4242 app=nova-sprint\n")
	assert.Contains(t, out, "MIGRATE SESSION role=nova_admin pid=4243 app=-\n")
	assert.True(t, strings.HasSuffix(out, fmt.Sprintf(" from=%d to=%d applied=0 dry_run=true pending=1 missing=0 role=nova_config ready=yes owner=nova_config sessions=2\n", currentSchema()-1, currentSchema())), out)
	assert.NotContains(t, out, "WOULD-REFUSE")
	code, out, errs = h.run(t, "migrate", "--dry-run", "--json")
	require.Equal(t, 0, code, "dry-run --json: stdout %q stderr %q", out, errs)
	assert.Contains(t, out, `"kind":"session"`)
	assert.Contains(t, out, `"sessions":2`)
	assert.Equal(t, currentSchema()-1, h.store.version, "the dry run applied a migration")

	// the window is open: the same migrate applies
	h.store.Held = nil
	code, out, errs = h.run(t, "migrate", "--window")
	require.Equal(t, 0, code, "window open: stdout %q stderr %q", out, errs)
	assert.Equal(t, fmt.Sprintf("CONFIG MIGRATE pg=nova_config@127.0.0.1:5432/nova from=%d to=%d applied=1\n", currentSchema()-1, currentSchema()), out)

	// without --window a held database does not gate (the bootstrap path is as before)
	h2 := newHarness()
	h2.env["NOVA_PG_DSN"] = dsn
	h2.store.version = currentSchema() - 1
	h2.store.Held = []config.Session{{Role: "nova_admin", PID: 7}}
	code, out, errs = h2.run(t, "migrate")
	require.Equal(t, 0, code, "no --window: stdout %q stderr %q", out, errs)
	assert.Equal(t, currentSchema(), h2.store.version)

	// nothing pending under --window with a held database: nothing to refuse, applied=0
	h3 := newHarness()
	h3.env["NOVA_PG_DSN"] = dsn
	h3.store.Held = []config.Session{{Role: "nova_admin", PID: 7}}
	code, out, errs = h3.run(t, "migrate", "--window")
	require.Equal(t, 0, code, "nothing pending: stdout %q stderr %q", out, errs)
	assert.Contains(t, out, " applied=0\n")
}

// The seat play's preflight runs as the owner and reads owner= back: a store
// whose tables have more than one owner is owner=mixed, the dry run names each
// table with its owner and would refuse with one ALTER OWNER per table, and
// migrate as the owner refuses before applying any; a store owned whole by the
// role says owner=<role>; a database before its first migration says owner=none.
func TestMigrateNamesTheWholeOwnerAndRefusesMixedOwnership(t *testing.T) {
	t.Parallel()

	n := currentSchema()
	h := newHarness()
	h.env["NOVA_PG_DSN"] = dsn
	h.store.version = n - 1
	o := config.Ownership{Role: "nova_config", SchemaOwner: "nova_config", Create: true, Tables: map[string]string{}}
	for tb := range mixedCatalog().Tables {
		o.Tables[tb] = "nova_config"
	}
	o.Tables["sprint"] = "nova_admin"
	h.store.Catalog = o

	why := fmt.Sprintf("role nova_config cannot apply migration %d and applied none: the role that runs migrate must own every table in schema config "+
		"and be able to create in it, and nova_admin owns config.sprint, so a role with the owners' rights runs this once, in psql", n)
	remedy := `ALTER TABLE config."sprint" OWNER TO "nova_config";`

	code, out, errs := h.run(t, "migrate", "--dry-run", "--json", "--window")
	require.Equal(t, 1, code, "dry-run --json mixed: stdout %q stderr %q", out, errs)
	assert.Contains(t, out, `"owner":"mixed"`)
	assert.Contains(t, out, `"ready":"no"`)
	assert.Contains(t, out, `"kind":"not_owned","fields":{"table":"config.sprint","owner":"nova_admin","role":"nova_config"}`)
	code, out, errs = h.run(t, "migrate", "--dry-run")
	require.Equal(t, 1, code, "dry-run mixed: stdout %q stderr %q", out, errs)
	assert.Contains(t, out, "MIGRATE NOT-OWNED table=config.sprint owner=nova_admin role=nova_config\n")
	assert.Contains(t, out, "MIGRATE WOULD-REFUSE "+why+"; run: "+remedy+"\n")
	assert.True(t, strings.HasSuffix(out, " pending=1 missing=0 role=nova_config ready=no owner=mixed sessions=0\n"), out)

	code, out, errs = h.run(t, "migrate", "--window")
	require.Equal(t, 1, code, "mixed: stdout %q stderr %q", out, errs)
	assert.Equal(t, "nova-config migrate REFUSED: "+why+"; run: "+remedy+"\n", errs)
	assert.Equal(t, n-1, h.store.version, "the refusal applied a migration")

	// owned whole by the role: owner=<role>, and the migrate applies
	h.store.Catalog.Tables["sprint"] = "nova_config"
	code, out, errs = h.run(t, "migrate", "--dry-run", "--window")
	require.Equal(t, 0, code, "dry-run whole: stdout %q stderr %q", out, errs)
	assert.True(t, strings.HasSuffix(out, " pending=1 missing=0 role=nova_config ready=yes owner=nova_config sessions=0\n"), out)
	code, out, errs = h.run(t, "migrate", "--window")
	require.Equal(t, 0, code, "whole: stdout %q stderr %q", out, errs)
	assert.Equal(t, n, h.store.version)

	// before the first migration there is no schema and no owner yet
	h4 := newHarness()
	h4.env["NOVA_PG_DSN"] = dsn
	h4.store.version = 0
	h4.store.Catalog = config.Ownership{Role: "nova_config", Tables: map[string]string{}}
	code, out, errs = h4.run(t, "migrate", "--dry-run", "--window")
	require.Equal(t, 0, code, "fresh: stdout %q stderr %q", out, errs)
	assert.Contains(t, out, " ready=yes owner=none sessions=0\n")
}
