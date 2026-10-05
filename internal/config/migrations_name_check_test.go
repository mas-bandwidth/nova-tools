package config

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMigrationsHoldEveryNamedTableToTheRowNamePattern asserts that the
// migration chain is unique and consecutive, ending with
// 0032_route_first.sql, and that migration 27 enforces the NamePattern
// ('^[a-z0-9][a-z0-9-]*$') check constraint on each named table
// (config.machines, config.friends, config.loops, config.routes)
// (docs/SPEC-CONFIG.md; security#69 finding 2).
func TestMigrationsHoldEveryNamedTableToTheRowNamePattern(t *testing.T) {
	t.Parallel()

	all, err := Migrations()
	require.NoError(t, err)
	require.NotEmpty(t, all)

	// Migrations() numbers stay unique and consecutive from 1 to len(all).
	for i, m := range all {
		wantVersion := i + 1
		assert.Equal(t, wantVersion, m.Version, "migration at index %d has version %d, want %d", i, m.Version, wantVersion)
		prefix := fmt.Sprintf("%04d_", wantVersion)
		assert.True(t, strings.HasPrefix(m.Name, prefix), "migration %s should have prefix %s", m.Name, prefix)
	}

	require.GreaterOrEqual(t, len(all), 27, "the row name checks are migration 27")
	last := all[26] // not the last: later migrations come after it
	require.Equal(t, 27, last.Version, "row name checks migration version")
	require.Equal(t, "0027_row_name_checks.sql", last.Name, "row name checks migration name")
	require.GreaterOrEqual(t, len(all), 32, "the route first column is migration 32")
	first := all[31]
	require.Equal(t, 32, first.Version, "route first migration version")
	require.Equal(t, "0032_route_first.sql", first.Name, "route first migration name")

	// Migration 27 is the name-pattern check. 0032 adds the route's first
	// column and does not replace that check (docs/SPEC-CONFIG.md, route).
	var sql string
	for _, m := range all {
		if m.Version == 27 {
			require.Equal(t, "0027_row_name_checks.sql", m.Name)
			sql = m.SQL
		}
	}
	require.NotEmpty(t, sql, "migration 27 is the name-pattern check")
	// Asserts its SQL names each of the four tables and the pattern string.
	for _, table := range []string{"config.machines", "config.friends", "config.loops", "config.routes"} {
		assert.Contains(t, sql, table, "migration 27 SQL must reference table %s", table)
	}

	const pattern = `^[a-z0-9][a-z0-9-]*$`
	assert.Contains(t, sql, pattern, "migration 27 SQL must contain the name pattern")
}
