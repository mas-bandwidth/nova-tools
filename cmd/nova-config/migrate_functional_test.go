//go:build functional

package main

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/config"
	"github.com/stretchr/testify/require"
)

// asRole is the database of dsn as another role (trust authentication: no
// password anywhere).
func asRole(t *testing.T, dsn, role string) string {
	t.Helper()
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	u.User = url.User(role)
	return u.String()
}

// applyAs applies migrations from..to by hand as the role of dsn, each with
// its ledger row, as the fleet's store was built: some by one role, some by
// another.
func applyAs(t *testing.T, dsn string, from, to int) {
	t.Helper()
	ctx := context.Background()
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	defer db.Close()
	all, err := config.Migrations()
	require.NoError(t, err)
	for _, m := range all {
		if m.Version < from || m.Version > to {
			continue
		}
		_, err := db.ExecContext(ctx, m.SQL)
		require.NoError(t, err, "migration %d", m.Version)
		_, err = db.ExecContext(ctx, `INSERT INTO config.schema_migrations (version) VALUES ($1)`, m.Version)
		require.NoError(t, err, "ledger %d", m.Version)
	}
}

func execAll(t *testing.T, dsn string, stmts ...string) {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	defer db.Close()
	for _, s := range stmts {
		_, err := db.ExecContext(context.Background(), s)
		require.NoError(t, err, "%s", s)
	}
}

func ledger(t *testing.T, dsn string) int {
	t.Helper()
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	defer db.Close()
	var v int
	require.NoError(t, db.QueryRowContext(context.Background(), `SELECT max(version) FROM config.schema_migrations`).Scan(&v))
	return v
}

// The measured case on a real Postgres, built as the fleet's store was: an
// admin role made the early tables (1 to 5) and later altered its own (10 to
// 12, 14), a config role with full row rights on them made and changed the rest
// (6 to 9, 13), so the ledger is at 14. The config role runs migrate: 0015
// (and any after it) alters config.machines, the admin role's, and migrate
// refuses before applying any, the ledger unchanged; the ALTER lines it
// prints, run once by a role with the owners' rights, let the same migrate
// apply them.
func TestMigrateRefusesMixedOwnershipUntilTheAlterLinesAreRun(t *testing.T) {
	t.Parallel()

	super := server.Database(t)
	u, err := url.Parse(super)
	require.NoError(t, err)
	db := strings.TrimPrefix(u.Path, "/")
	admin, cfg := "adm_"+db, "cfg_"+db
	// Roles are the cluster's, named after the test's own database so parallel
	// tests never share one; the throwaway cluster goes with them.
	execAll(t, super,
		"CREATE ROLE "+admin+" LOGIN", "CREATE ROLE "+cfg+" LOGIN",
		"GRANT CREATE ON DATABASE "+db+" TO "+admin)
	adminDSN, cfgDSN := asRole(t, super, admin), asRole(t, super, cfg)
	applyAs(t, adminDSN, 1, 5)
	execAll(t, adminDSN,
		"GRANT USAGE, CREATE ON SCHEMA config TO "+cfg,
		"GRANT ALL ON ALL TABLES IN SCHEMA config TO "+cfg,
		"GRANT ALL ON ALL SEQUENCES IN SCHEMA config TO "+cfg)
	applyAs(t, cfgDSN, 6, 9)
	applyAs(t, adminDSN, 10, 12)
	applyAs(t, cfgDSN, 13, 13)
	applyAs(t, adminDSN, 14, 14)
	all, err := config.Migrations()
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(all), 15, "the measured case is the ledger at 14 and 0015 (and any after it) pending")

	r := &real{env: map[string]string{"NOVA_PG_DSN": cfgDSN}}
	_, errs := r.run(t, 1, "migrate")
	for _, tb := range []string{"fleet", "friends", "history", "machines", "schema_migrations", "sprint"} {
		require.Contains(t, errs, "config."+tb, "refusal: %q", errs)
	}
	for _, tb := range []string{"loops", "routes", "tiers"} {
		require.NotContains(t, errs, "config."+tb, "refusal: %q", errs)
	}
	pending := "migration 15"
	if len(all) > 15 {
		pending = fmt.Sprintf("migrations 15 to %d", len(all))
	}
	require.Contains(t, errs, "nova-config migrate REFUSED: role "+cfg+" cannot apply "+pending+" and applied none", "refusal: %q", errs)
	require.Contains(t, errs, admin+" owns ", "refusal: %q", errs)
	require.Equal(t, 14, ledger(t, super), "the refusal applied a migration")

	out, _ := r.run(t, 1, "migrate", "--dry-run")
	require.Contains(t, out, " ready=no\n", "dry-run: %q", out)
	require.Equal(t, 14, ledger(t, super), "the dry run applied a migration")

	_, remedy, found := strings.Cut(strings.TrimSuffix(errs, "\n"), "; run: ")
	require.True(t, found, "refusal names no remedy: %q", errs)
	execAll(t, super, remedy)
	out, _ = r.run(t, 0, "migrate", "--dry-run")
	require.Contains(t, out, " ready=yes\n", "dry-run after the remedy: %q", out)
	out, _ = r.run(t, 0, "migrate")
	require.True(t, strings.HasSuffix(out, fmt.Sprintf(" from=14 to=%d applied=%d\n", len(all), len(all)-14)), "migrate after the remedy: %q", out)
	require.Equal(t, len(all), ledger(t, super))
}
