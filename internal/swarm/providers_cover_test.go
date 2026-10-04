package swarm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Unit coverage for providers.go's LaunchRow, untagged and store-free: its main
// path and its one fallback, over the embedded providers table. LaunchRow has no
// error return, so the fallback for a provider the table does not name is the one
// outcome that stands in for a refusal; the read-error branch answers the provider
// itself and is reachable only through a malformed embedded table, which no unit
// test can produce without a seam for the table's bytes.

// TestProvidersCoverLaunchRowNamesTheTablesRow: LaunchRow answers a provider the
// table names with that provider's own row, and answers a provider the table does
// not name with DefaultLaunchRow rather than guessing a row.
func TestProvidersCoverLaunchRowNamesTheTablesRow(t *testing.T) {
	t.Parallel()

	table, err := readProvidersTable()
	require.NoError(t, err, "readProvidersTable: %v", err)
	require.NotEmpty(t, table, "the embedded providers table has no rows")

	t.Run("table row", func(t *testing.T) {
		t.Parallel()
		for name := range table {
			assert.Equal(t, name, LaunchRow(name), "LaunchRow(%q) names the table's own row", name)
		}
	})

	t.Run("unknown provider falls back", func(t *testing.T) {
		t.Parallel()
		for _, unknown := range []string{"nonexistent-provider-xyz", "", "DeepSeek"} {
			assert.Equal(t, DefaultLaunchRow, LaunchRow(unknown), "LaunchRow(%q) falls back to DefaultLaunchRow", unknown)
		}
	})
}

// TestProvidersCoverDefaultLaunchRowIsDeclared: the row LaunchRow falls back to is
// itself declared in the table, so a route the table does not name still launches
// through the one launcher and its one argv shape.
func TestProvidersCoverDefaultLaunchRowIsDeclared(t *testing.T) {
	t.Parallel()

	table, err := readProvidersTable()
	require.NoError(t, err, "readProvidersTable: %v", err)
	_, ok := table[DefaultLaunchRow]
	assert.True(t, ok, "DefaultLaunchRow %q is not declared in the providers table", DefaultLaunchRow)
}
