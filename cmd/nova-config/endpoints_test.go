package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Partial rows remain saveable in the authoritative file store, but full
// apply checks the endpoint pair before opening Redis or applying a kind
// (docs/SPEC-CONFIG.md, "Apply").
func TestFileFleetEndpointsStayPartialUntilApply(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		fields  []string
		missing string
	}{
		{"neither endpoint", nil, "redis_port, pg_dsn"},
		{"only Redis port", []string{"--redis_port", "6380"}, "pg_dsn"},
		{"only Postgres DSN", []string{"--pg_dsn", dsn}, "redis_port"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness()
			h.dir = t.TempDir()
			h.env["NOVA_FRIEND"], h.env["NOVA_SPRINT_REDIS"] = "operator", "bench-beta:6380"
			step := func(args ...string) {
				t.Helper()
				code, _, errs := h.run(t, append(args, "--file", "try.json")...)
				require.Zero(t, code, errs)
			}
			step("migrate")
			step("machine", "add", "bench-beta", "--user", "u", "--seat", "s", "--slots", "4")
			step(append([]string{"fleet", "set", "--store", "bench-beta"}, tc.fields...)...)
			path := filepath.Join(h.dir, "try.json")
			before, err := os.ReadFile(path)
			require.NoError(t, err)
			for _, args := range [][]string{{"apply"}, {"apply", "--dry-run"}, {"apply", "--kind", "fleet"}} {
				code, out, errs := h.run(t, append(args, "--file", "try.json")...)
				require.Equal(t, 1, code, errs)
				assert.Empty(t, out)
				assert.Contains(t, errs, "endpoints are unset: "+tc.missing)
				assert.Zero(t, h.redis.opens)
				assert.Empty(t, h.redis.log)
			}
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, before, after, "a refused apply preserves the partial file")
			step("apply", "--kind", "machine", "--dry-run")
			assert.Equal(t, 1, h.redis.opens, "another kind may apply while endpoints are partial")
			step("fleet", "set", "--redis_port", "6380", "--pg_dsn", dsn)
			step("apply", "--dry-run")
			assert.Equal(t, 2, h.redis.opens, "the complete endpoint pair permits full apply")
			assert.Empty(t, h.redis.log)
		})
	}
}

// A fixture is inventory's entire source, so missing endpoints refuse
// without connecting to either store (docs/SPEC-CONFIG.md, "Inventory").
func TestInventoryFixtureChecksEndpointsWithoutConnections(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		fleet   string
		missing string
	}{
		{"neither endpoint", "{}", "redis_port, pg_dsn"},
		{"only Redis port", "{redis_port: 6380}", "pg_dsn"},
		{"only Postgres DSN", "{pg_dsn: postgres://user@localhost:5432/nova}", "redis_port"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness()
			path := filepath.Join(t.TempDir(), "inventory.yml")
			require.NoError(t, os.WriteFile(path, []byte("fleet: "+tc.fleet+"\n"), 0o600))
			code, out, errs := h.run(t, "inventory", "--fixture", path)
			require.Equal(t, 1, code, errs)
			assert.Empty(t, out)
			assert.Contains(t, errs, "endpoints are unset: "+tc.missing)
			assert.Zero(t, h.opens+h.redis.opens)
		})
	}
}
