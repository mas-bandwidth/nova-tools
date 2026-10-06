package doctor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/config"
)

// pgRig is the world the pg check reads: NOVA_PG_DSN in the environment, and
// the one answer `nova-config migrate --dry-run --json` gives. Nothing runs and
// no socket opens: the answer is the whole outside world.
type pgRig struct {
	dsn    string // NOVA_PG_DSN; empty names no store
	from   int    // the store's greatest applied migration
	role   string // the login the store answered as
	ready  string // migrate's readiness; "yes" when empty
	refuse bool   // nova-config does not answer at all
}

// pgFake is a fake Env over the rig: the exec answers only the one call the
// check makes and refuses every other, as the real tool refuses what it cannot
// run.
func pgFake(t *testing.T, r pgRig) Env {
	t.Helper()
	env := map[string]string{}
	if r.dsn != "" {
		env[config.EnvPG] = r.dsn
	}
	return fakeEnv{env: env, exec: func(name string, args ...string) (string, error) {
		require.Equal(t, "nova-config", name)
		require.Equal(t, []string{"migrate", "--dry-run", "--json"}, args)
		if r.refuse {
			return "", errors.New("dial tcp 127.0.0.1:1: connect: connection refused")
		}
		return pgLedgerJSON(t, r), nil
	}}
}

// pgLedgerJSON is what `nova-config migrate --dry-run --json` answers for the
// rig's store: the facts and one migration item each, applied at or below
// from and pending above it, and a not-owned item when migrate would refuse
// (cmd/nova-config/migrate.go, migrateDryRun).
func pgLedgerJSON(t *testing.T, r pgRig) string {
	t.Helper()
	all, err := config.Migrations()
	require.NoError(t, err)
	require.NotEmpty(t, all)
	ready := r.ready
	if ready == "" {
		ready = "yes"
	}
	var items []map[string]any
	for _, m := range all {
		state := "applied"
		if m.Version > r.from {
			state = "pending"
		}
		items = append(items, map[string]any{
			"kind":   "migration",
			"fields": map[string]any{"version": m.Version, "file": m.Name, "lines": 1, "state": state},
		})
	}
	if ready != "yes" {
		items = append(items, map[string]any{
			"kind":   "not_owned",
			"fields": map[string]any{"table": "config.machines", "owner": "other", "role": r.role},
		})
	}
	status, exit := "ok", 0
	if ready != "yes" {
		status, exit = "failed", 1
	}
	report := map[string]any{
		"result": map[string]any{"verb": "migrate", "status": status, "exit": exit},
		"facts": map[string]any{
			"pg": "user@db.test:5432/nova", "from": r.from, "to": len(all), "applied": 0,
			"dry_run": true, "pending": len(all) - r.from, "missing": 0, "role": r.role, "ready": ready,
		},
		"items": items,
	}
	b, err := json.Marshal(report)
	require.NoError(t, err)
	return string(b)
}

// TestDoctorPostgresCheckNamesTheMissingMigration pins the pg check on a fake
// Env (docs/SPEC-DOCTOR.md "The frame", docs/SETUP.md dep-postgres-b.w3): the
// store answers at the configured address as the configured user, and the
// check holds its schema to the newest migration this binary carries — a store
// behind names every migration not applied with the nova-config verb that
// applies it as the fix, a store at the newest is ok.
func TestDoctorPostgresCheckNamesTheMissingMigration(t *testing.T) {
	t.Parallel()
	const dsn = "postgres://user@db.test:5432/nova"
	all, err := config.Migrations()
	require.NoError(t, err)
	require.NotEmpty(t, all)
	newest := all[len(all)-1].Version

	t.Run("one migration behind names it and the verb that applies it", func(t *testing.T) {
		t.Parallel()
		r := checkPG(context.Background(), pgFake(t, pgRig{dsn: dsn, from: newest - 1, role: "nova_config"}))
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, fmt.Sprintf("%d %s", newest, all[len(all)-1].Name))
		assert.Contains(t, r.Evidence, "schema=")
		assert.Contains(t, r.Fix, "nova-config migrate")
	})
	t.Run("every migration behind is named", func(t *testing.T) {
		t.Parallel()
		r := checkPG(context.Background(), pgFake(t, pgRig{dsn: dsn, from: 0, role: "nova_config"}))
		assert.Equal(t, Fail, r.Status, r)
		for _, m := range all {
			assert.Contains(t, r.Evidence, fmt.Sprintf("%d %s", m.Version, m.Name))
		}
		assert.Contains(t, r.Fix, "nova-config migrate")
	})
	t.Run("at the newest migration is ok", func(t *testing.T) {
		t.Parallel()
		r := checkPG(context.Background(), pgFake(t, pgRig{dsn: dsn, from: newest, role: "nova_config"}))
		assert.Equal(t, OK, r.Status, r)
		assert.Contains(t, r.Evidence, fmt.Sprintf("schema=%d/%d", newest, newest))
		assert.Contains(t, r.Evidence, "role=nova_config")
		assert.Empty(t, r.Fix)
	})
	t.Run("a store the role cannot migrate refuses with the dry run", func(t *testing.T) {
		t.Parallel()
		r := checkPG(context.Background(), pgFake(t, pgRig{dsn: dsn, from: newest, role: "nova_config", ready: "no"}))
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "ready=no")
		assert.Contains(t, r.Fix, "nova-config migrate --dry-run")
	})
	t.Run("a store ahead of this binary names the update", func(t *testing.T) {
		t.Parallel()
		r := checkPG(context.Background(), pgFake(t, pgRig{dsn: dsn, from: newest + 1, role: "nova_config"}))
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "ahead")
		assert.Contains(t, r.Fix, "nova-update apply")
	})
	t.Run("the check is registered", func(t *testing.T) {
		t.Parallel()
		assert.Contains(t, Default.Names(), "pg")
	})
}

// TestDoctorPostgresCheckSaysTheLocalEquivalent: with no NOVA_PG_DSN the check
// is ok and names what nova-up --local provides in the fleet store's place
// (docs/SETUP.md, dep-postgres-b.w3).
func TestDoctorPostgresCheckSaysTheLocalEquivalent(t *testing.T) {
	t.Parallel()
	r := checkPG(context.Background(), pgFake(t, pgRig{}))
	assert.Equal(t, OK, r.Status, r)
	assert.Contains(t, r.Evidence, "nova-up --local")
	assert.Contains(t, r.Evidence, "twin store")
	assert.Empty(t, r.Fix)
}

// TestDoctorPostgresCheckFailsWhenTheStoreDoesNotAnswer: a store that does not
// answer at the configured address is a fail whose fix names the verb that
// shows the store's own refusal.
func TestDoctorPostgresCheckFailsWhenTheStoreDoesNotAnswer(t *testing.T) {
	t.Parallel()
	r := checkPG(context.Background(), pgFake(t, pgRig{dsn: "postgres://user@db.test:5432/nova", role: "nova_config", refuse: true}))
	assert.Equal(t, Fail, r.Status, r)
	assert.Contains(t, r.Evidence, "did not answer")
	assert.Contains(t, r.Evidence, "user@db.test:5432/nova")
	assert.Contains(t, r.Fix, "nova-config status")
}
