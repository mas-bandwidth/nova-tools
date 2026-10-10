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
			delete(h.env, "NOVA_SPRINT_REDIS") // partial rows are stored before Redis can apply them
			step("machine", "add", "bench-beta", "--user", "u", "--seat", "s", "--slots", "4")
			step(append([]string{"fleet", "set", "--store", "bench-beta"}, tc.fields...)...)
			h.env["NOVA_SPRINT_REDIS"] = "bench-beta:6380"
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
			delete(h.env, "NOVA_SPRINT_REDIS")
			step("fleet", "set", "--redis_port", "6380", "--pg_dsn", dsn)
			h.env["NOVA_SPRINT_REDIS"] = "bench-beta:6380"
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

// Known endpoint problems refuse before opening the authoritative store;
// malformed query errors never print the URI or its values (docs/SPEC-CONFIG.md,
// "fleet"), and the file's bytes and history stay unchanged.
func TestFleetEndpointProblemsRefuseBeforePersistenceOrConnections(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		field string
		value string
		want  string
	}{
		{"semicolon query", "pg_dsn", dsn + "?password=synthetic;sslmode=disable", "valid URI query"},
		{"invalid escape", "pg_dsn", dsn + "?password=%zz", "valid URI query"},
		{"invalid escape after secret", "pg_dsn", dsn + "?password=synthetic%zz", "valid URI query"},
		{"invalid key escape", "pg_dsn", dsn + "?pass%zzword=synthetic", "valid URI query"},
		{"encoded mixed-case password", "pg_dsn", dsn + "?%70aSsWoRd=synthetic", "carries a password"},
		{"zero Redis port", "redis_port", "0", "1 through 65535"},
		{"large Redis port", "redis_port", "65536", "1 through 65535"},
		{"zero Postgres port", "pg_dsn", "postgres://user@localhost:0/nova", "TCP port from 1 through 65535"},
		{"large Postgres port", "pg_dsn", "postgres://user@localhost:65536/nova", "TCP port from 1 through 65535"},
		{"empty Postgres port", "pg_dsn", "postgres://user@localhost:/nova", "TCP port from 1 through 65535"},
		{"nonnumeric Postgres port", "pg_dsn", "postgres://user@localhost:synthetic/nova", "password-free postgres://"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			h := newHarness()
			h.dir = t.TempDir()
			code, _, errs := h.run(t, "migrate", "--file", "try.json")
			require.Zero(t, code, errs)
			path := filepath.Join(h.dir, "try.json")
			before, err := os.ReadFile(path)
			require.NoError(t, err)
			h.opens = 0
			for _, store := range [][]string{{"--file", "try.json"}, {"--pg", dsn}} {
				args := []string{"fleet", "set", "--" + tc.field, tc.value, "--as", "operator"}
				code, out, errs := h.run(t, append(args, store...)...)
				require.Equal(t, 2, code, errs)
				assert.Empty(t, out)
				assert.Contains(t, errs, tc.want)
				assert.Contains(t, errs, "run: nova-config fleet set -h")
				assert.NotContains(t, errs, "synthetic")
				assert.NotContains(t, errs, "%zz")
				if tc.field == "pg_dsn" {
					assert.NotContains(t, errs, tc.value)
				}
				assert.Zero(t, h.opens+h.redis.opens)
			}
			after, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, before, after)
		})
	}
}
