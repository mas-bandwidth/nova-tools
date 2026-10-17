package config

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestConfigRowsWriteFixedPgFileInventory is the writer half of the nova-config
// rows' table adoption (v1.5 aspect 9, part 1: the pg, file and inventory
// stores). Every writer of a row the dual reader reads (the history side, the
// file twin, the loop view) writes the table form, and what it writes reads
// back equal through the new reader; the legacy fixtures still read. A value a
// table cannot hold is refused by name before a byte is written, never written
// in the legacy form as a fallback (S4, writers second).
//
// Unit tier: no clock, no socket, no program, no store; the file twin is
// written and read through the house's store in a tempdir.
func TestConfigRowsWriteFixedPgFileInventory(t *testing.T) {
	t.Parallel()

	t.Run("file twin", func(t *testing.T) {
		t.Parallel()
		path := migratedFile(t)
		f, err := OpenFile(path)
		require.NoError(t, err)
		row := Row{Name: "m1", Fields: map[string]string{"user": "nova", "seat": "m1", "slots": "4"}, CreatedAt: "2026-10-01T00:00:00Z", UpdatedAt: "2026-10-02T00:00:00Z"}
		_, err = f.Insert(t.Context(), KindMachine, row, "actor1")
		require.NoError(t, err)
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		// the writer writes the fixed batch image as padded standard base64,
		// never the legacy JSON object
		require.NotContains(t, string(raw), `"rows":`)
		require.Contains(t, string(raw), "=")
		_, err = base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
		require.NoError(t, err, "the file is a base64 image")
		again, err := OpenFile(path)
		require.NoError(t, err)
		got, found, err := again.Get(t.Context(), KindMachine, "m1")
		require.NoError(t, err)
		require.True(t, found)
		require.Equal(t, row, got)
	})
}
