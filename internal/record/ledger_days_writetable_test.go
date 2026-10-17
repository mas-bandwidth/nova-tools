package record_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/record"
	_ "github.com/mas-bandwidth/nova-tools/internal/record/ledgerread"
	"github.com/stretchr/testify/require"
)

// TestLedgerDaysWriteTable is the writer switch of the ledger record (v1.5
// aspect 13; the owner's S2 and S4, writers second): ReplaceLedgerDays writes
// each ledger entry's field key and value in the table form, the padded
// standard base64 of the LedgerKey and LedgerValue wires of schemas/ledger.
// What it writes reads back equal through the dual reader, the legacy fixtures
// still read (the reader keeps its legacy form), and a value the table cannot
// hold is refused by name before a byte is written, never written in the
// legacy form as a fallback. The day's fields are written to and read from a
// t.TempDir().
//
// Unit tier: no clock, no socket, no store; every value is in memory.
func TestLedgerDaysWriteTable(t *testing.T) {
	t.Parallel()

	store := record.NewRedisLedger(nil)
	row := ledgerRow
	days := []record.LedgerDay{{Day: day, Entries: []record.LedgerEntry{row}}}
	written, err := store.PrepareLedgerDays(days)
	require.NoError(t, err, "the switched writer prepares the day")

	// Write the day's field/value pairs to a file in a t.TempDir(), one pair
	// per line, and read the file back.
	dir := t.TempDir()
	path := filepath.Join(dir, day+".ledger")
	f, err := os.Create(path)
	require.NoError(t, err)
	for k, v := range written[record.LedgerKey(day)] {
		_, err := fmt.Fprintf(f, "%s\t%s\n", k, v)
		require.NoError(t, err)
	}
	require.NoError(t, f.Close())
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	fields := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		parts := strings.SplitN(line, "\t", 2)
		require.Len(t, parts, 2, "a written pair %q", line)
		fields[parts[0]] = parts[1]
	}

	// Everything the switched writer wrote reads back equal through the dual
	// reader.
	got, err := record.ReadLedgerDay(day, fields)
	require.NoError(t, err, "the table-written day reads")
	require.Equal(t, []record.LedgerEntry{row}, got)

	// The legacy fixtures still read: the dual reader keeps its legacy form
	// until the retire card.
	gotLegacy, err := record.ReadLedgerDay(day, map[string]string{legacyField: legacyValue})
	require.NoError(t, err, "the legacy fixture reads")
	require.Equal(t, []record.LedgerEntry{row}, gotLegacy)

	// A value the table cannot hold is refused by name before a byte is
	// written, never written in the legacy form as a fallback.
	bad := row
	bad.Sources = "bad\x00source"
	_, err = store.PrepareLedgerDays([]record.LedgerDay{{Day: day, Entries: []record.LedgerEntry{bad}}})
	require.ErrorContains(t, err, "LEDGER_VALUE REFUSED: ", "a NUL source is refused by name")
	require.ErrorContains(t, err, "run: "+record.LedgerHelp)
}
