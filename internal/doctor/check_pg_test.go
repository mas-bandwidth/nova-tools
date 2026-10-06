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

// pgFake is a fake Env whose exec answers `nova-config migrate --dry-run
// --json` with the store's ledger read at schema version from. Nothing runs
// and no socket opens: the JSON is the whole outside world.
func pgFake(t *testing.T, from int, role string) Env {
	t.Helper()
	body := pgLedgerJSON(t, from, role)
	return fakeEnv{
		env: map[string]string{config.EnvPG: "postgres://user@db.test:5432/nova"},
		exec: func(name string, args ...string) (string, error) {
			require.Equal(t, "nova-config", name)
			require.Equal(t, []string{"migrate", "--dry-run", "--json"}, args)
			return body, nil
		},
	}
}

// pgLedgerJSON is what `nova-config migrate --dry-run --json` prints for a
// store whose greatest applied migration is from: the facts, and one migration
// item each, applied at or below from and pending above it.
func pgLedgerJSON(t *testing.T, from int, role string) string {
	t.Helper()
	all, err := config.Migrations()
	require.NoError(t, err)
	require.NotEmpty(t, all)
	var items []map[string]any
	for _, m := range all {
		state := "applied"
		if m.Version > from {
			state = "pending"
		}
		items = append(items, map[string]any{
			"kind":   "migration",
			"fields": map[string]any{"version": m.Version, "file": m.Name, "lines": 1, "state": state},
		})
	}
	report := map[string]any{
		"result": map[string]any{"verb": "migrate", "status": "ok", "exit": 0},
		"facts": map[string]any{
			"pg": "user@db.test:5432/nova", "from": from, "to": len(all), "applied": 0,
			"dry_run": true, "pending": len(all) - from, "missing": 0, "role": role, "ready": "yes",
		},
		"items": items,
	}
	b, err := json.Marshal(report)
	require.NoError(t, err)
	return string(b)
}

// TestDoctorPostgresCheckNamesTheMissingMigration pins the pg check on a fake
// Env: the store answers at the configured address as the configured user, and
// when its schema is behind the newest migration this binary carries the check
// fails and names the migration, with the nova-config verb that applies it as
// the fix; at the newest migration it is ok.
func TestDoctorPostgresCheckNamesTheMissingMigration(t *testing.T) {
	t.Parallel()
	all, err := config.Migrations()
	require.NoError(t, err)
	require.NotEmpty(t, all)
	newest := all[len(all)-1]

	t.Run("one migration behind names it and the verb that applies it", func(t *testing.T) {
		t.Parallel()
		r := checkPG(context.Background(), pgFake(t, newest.Version-1, "config"))
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, fmt.Sprintf("%d %s", newest.Version, newest.Name))
		assert.Contains(t, r.Evidence, "schema=")
		assert.Contains(t, r.Fix, "nova-config migrate")
	})
	t.Run("at the newest migration is ok", func(t *testing.T) {
		t.Parallel()
		r := checkPG(context.Background(), pgFake(t, newest.Version, "config"))
		assert.Equal(t, OK, r.Status, r)
		assert.Contains(t, r.Evidence, "schema="+fmt.Sprint(newest.Version))
		assert.Empty(t, r.Fix)
	})
	t.Run("the check is registered", func(t *testing.T) {
		t.Parallel()
		assert.Contains(t, Default.Names(), "pg")
	})
}

// TestDoctorPostgresCheckSaysTheLocalEquivalent: with no NOVA_PG_DSN the check
// is ok and names what nova-up --local provides in the fleet store's place.
func TestDoctorPostgresCheckSaysTheLocalEquivalent(t *testing.T) {
	t.Parallel()
	r := checkPG(context.Background(), fakeEnv{env: map[string]string{}})
	assert.Equal(t, OK, r.Status, r)
	assert.Contains(t, r.Evidence, "twin store")
	assert.Contains(t, r.Evidence, "nova-up --local")
	assert.Empty(t, r.Fix)
}

// TestDoctorPostgresCheckFailsWhenTheStoreDoesNotAnswer: a store that does not
// answer at the configured address is a fail whose fix names the verb that
// shows the store's own refusal.
func TestDoctorPostgresCheckFailsWhenTheStoreDoesNotAnswer(t *testing.T) {
	t.Parallel()
	env := fakeEnv{
		env: map[string]string{config.EnvPG: "postgres://user@db.test:5432/nova"},
		exec: func(string, ...string) (string, error) {
			return "", errors.New("dial tcp 127.0.0.1:1: connection refused")
		},
	}
	r := checkPG(context.Background(), env)
	assert.Equal(t, Fail, r.Status, r)
	assert.Contains(t, r.Evidence, "did not answer")
	assert.Contains(t, r.Fix, "nova-config status")
}
