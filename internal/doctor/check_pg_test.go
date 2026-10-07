package doctor

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// pgRig is the machine the pg check reads: a DSN and whether it can connect.
// Nothing real runs.
type pgRig struct {
	dsn     string // NOVA_CONFIG_DSN
	failConn bool   // connection should fail
	local   bool   // --local mode
}

// runPostgresCheck runs only the pg check over the rig.
func runPostgresCheck(t *testing.T, r pgRig) Result {
	t.Helper()
	env := map[string]string{}
	if r.dsn != "" {
		env["NOVA_CONFIG_DSN"] = r.dsn
	}
	if r.local {
		env["NOVA_LOCAL"] = "true"
	}
	fe := fakeEnv{env: env}
	fe.dial = func(addr string) error {
		if r.failConn {
			return errors.New("connection refused")
		}
		return nil
	}
	reg := NewRegistry()
	reg.Register(Default.checks["pg"])
	res, _, err := reg.Run(context.Background(), fe, Options{})
	require.NoError(t, err)
	require.Len(t, res, 1)
	return res[0]
}

// TestDoctorPostgresCheckNamesTheMissingMigration tests the pg check: it fails when
// the database is unreachable or the DSN is missing, and passes when the database
// answers. Under --local it says so.
func TestDoctorPostgresCheckNamesTheMissingMigration(t *testing.T) {
	t.Parallel()

	t.Run("no DSN is a fail", func(t *testing.T) {
		t.Parallel()
		r := runPostgresCheck(t, pgRig{dsn: ""})
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "NOVA_CONFIG_DSN")
		assert.Contains(t, r.Fix, "docs/SETUP.md, dep-postgres-b.w4")
	})

	t.Run("connection refused is a fail", func(t *testing.T) {
		t.Parallel()
		r := runPostgresCheck(t, pgRig{dsn: "postgresql://user@localhost/nova_config", failConn: true})
		assert.Equal(t, Fail, r.Status, r)
		assert.Contains(t, r.Evidence, "did not answer")
		assert.Contains(t, r.Fix, "docs/SETUP.md, dep-postgres-b.w4")
	})

	t.Run("database is reachable is ok", func(t *testing.T) {
		t.Parallel()
		r := runPostgresCheck(t, pgRig{dsn: "postgresql://user@localhost/nova_config"})
		assert.Equal(t, OK, r.Status, r)
		assert.Contains(t, r.Evidence, "migration 5")
		assert.Empty(t, r.Fix)
	})

	t.Run("--local mode is ok with local", func(t *testing.T) {
		t.Parallel()
		r := runPostgresCheck(t, pgRig{dsn: "postgresql://user@localhost/nova_config", local: true})
		assert.Equal(t, OK, r.Status, r)
		assert.Contains(t, r.Evidence, "local postgres")
		assert.Empty(t, r.Fix)
	})
}
