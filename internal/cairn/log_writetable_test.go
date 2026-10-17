package cairn

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/ledgerwire"
	"github.com/stretchr/testify/require"
)

// readCairnLogLine reads one log.jsonl line as the CairnLogRecord table: the
// padded standard base64 of the wire (S2). It returns the record as its JSON
// text, the shape ReadOpen reads the legacy open line in.
func readCairnLogLine(t *testing.T, line string) string {
	t.Helper()
	wire, err := base64.StdEncoding.DecodeString(strings.TrimSpace(line))
	require.NoError(t, err, "the line is padded standard base64 of the wire")
	var b ledgerwire.CairnLogRecordBuilder
	require.True(t, b.Init())
	defer b.Shutdown()
	var rep ledgerwire.TableReport
	require.True(t, ledgerwire.CairnLogRecordLoadBuilder(&b, wire, &rep), "the wire loads as a CairnLogRecord")
	require.True(t, b.Lock())
	v := b.AsConst()
	n := ledgerwire.CairnLogRecordToJsonMeasure(v)
	require.True(t, n >= 0)
	buf := make([]byte, n)
	require.Equal(t, n, ledgerwire.CairnLogRecordToJson(v, buf))
	return string(buf)
}

// TestCairnLogWriteTable pins the writer's switch to the table form (S4,
// writers second; S2, a text line carries the padded standard base64 of the
// wire). appendLog writes the CairnLogRecord table of schemas/ledger, and what
// it writes reads back equal through the dual reader; a value the table cannot
// hold is refused by name before a byte is written, and a legacy fixture of the
// same record still reads.
func TestCairnLogWriteTable(t *testing.T) {
	t.Parallel()

	// The writer writes the table form into a t.TempDir(), and the line it
	// appends reads back equal as the CairnLogRecord.
	store := t.TempDir()
	stamp := "2026-10-17T12:00:00Z"
	require.NoError(t, appendLog(store, "open", "session-1", "", stamp, "manual", "fixture"))
	raw, err := os.ReadFile(filepath.Join(store, "log.jsonl"))
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	require.Len(t, lines, 1, "one append writes one line")
	want := `{"event":"open","session":"session-1","entry":"","stamp":"2026-10-17T12:00:00Z","publish":"manual","source":"fixture"}`
	require.JSONEq(t, want, readCairnLogLine(t, lines[0]), "what appendLog writes reads back equal through the cair reader")

	// A value the table cannot hold is refused by name before a byte is
	// written: an event that is not an enum variant never lands a line.
	require.ErrorContains(t, appendLog(store, "bogus", "session-1", "", stamp, "manual", "fixture"), "REFUSED")
	rawAfter, err := os.ReadFile(filepath.Join(store, "log.jsonl"))
	require.NoError(t, err)
	require.Equal(t, string(raw), string(rawAfter), "a refused value writes no line")

	// A legacy fixture of the same record still reads: a store whose log.jsonl
	// was written by the legacy JSON writer still answers ReadOpen.
	legacyStore := t.TempDir()
	require.NoError(t, os.MkdirAll(legacyStore, 0o755))
	legacy := `{"event":"open","session":"session-9","entry":"","stamp":"2026-10-17T12:00:00Z","publish":"manual","source":"fixture"}`
	require.NoError(t, os.WriteFile(filepath.Join(legacyStore, "log.jsonl"), []byte(legacy+"\n"), 0o644))
	rec, err := ReadOpen(legacyStore, "session-9")
	require.NoError(t, err)
	require.True(t, rec.Found, "the legacy fixture still reads")
	require.Equal(t, "fixture", rec.Source)
	require.Equal(t, "manual", rec.Publish)
}
