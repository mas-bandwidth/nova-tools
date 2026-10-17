package cairn

import (
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/record/tablejson"
	"github.com/mas-bandwidth/nova-tools/pkg/ledgerwire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/require"
)

// testCairnLogWire is the CairnLogRecord table's Wire, built from the
// generated package as internal/record/ledgerread wires it. This test installs
// it because a test in this package cannot import ledgerread (it imports this
// package, so the test build would cycle); the writer switch under test runs
// through the same slot the installer fills.
func testCairnLogWire() tablejson.Wire {
	load := func(in []byte, loadFn func(*ledgerwire.CairnLogRecordBuilder, []byte, *ledgerwire.TableReport) bool, out func(*ledgerwire.CairnLogRecord) []byte) ([]byte, schemaread.Report) {
		var b ledgerwire.CairnLogRecordBuilder
		if !b.Init() {
			return nil, schemaread.Report{Malformed: true}
		}
		defer b.Shutdown()
		var rep ledgerwire.TableReport
		ok := loadFn(&b, in, &rep)
		r := schemaread.Report{Unknown: int(rep.Unknown), KindMismatch: int(rep.KindMismatch), Clamped: int(rep.Clamped),
			Malformed: rep.Malformed || rep.Duplicate > 0 || !ok}
		if rep.Verdict == ledgerwire.TableOpenRefused || rep.Verdict == ledgerwire.TableOpenDamaged || rep.MissingRequired > 0 {
			if err := rep.Err(); err != nil {
				r.Reason = err.Error()
			}
		}
		if !ok || r.Reason != "" || r.Malformed || !b.Lock() {
			r.Malformed = r.Malformed || r.Reason == ""
			return nil, r
		}
		v := b.AsConst()
		if v == nil {
			return nil, schemaread.Report{Malformed: true}
		}
		text := out(v)
		if text == nil {
			r.Malformed = true
		}
		return text, r
	}
	asJSON := func(v *ledgerwire.CairnLogRecord) []byte {
		n := ledgerwire.CairnLogRecordToJsonMeasure(v)
		if n < 0 {
			return nil
		}
		buf := make([]byte, n)
		if ledgerwire.CairnLogRecordToJson(v, buf) != n {
			return nil
		}
		return buf
	}
	asWire := func(v *ledgerwire.CairnLogRecord) []byte {
		n := ledgerwire.CairnLogRecordMeasure(v)
		if n < 0 {
			return nil
		}
		buf := make([]byte, n)
		if ledgerwire.CairnLogRecordSave(v, buf) != n {
			return nil
		}
		return buf
	}
	return tablejson.Wire{
		Measure:  func(wire []byte) error { _, err := ledgerwire.CairnLogRecordLoadMeasureReason(wire); return err },
		WireJSON: func(wire []byte) ([]byte, schemaread.Report) { return load(wire, ledgerwire.CairnLogRecordLoadBuilder, asJSON) },
		JSON:     func(text []byte) ([]byte, schemaread.Report) { return load(text, ledgerwire.CairnLogRecordFromJson, asJSON) },
		JSONWire: func(text []byte) ([]byte, error) {
			wire, rep := load(text, ledgerwire.CairnLogRecordFromJson, asWire)
			if wire == nil || rep.Reason != "" || rep.Malformed || rep.Clamped > 0 || rep.KindMismatch > 0 {
				return nil, errors.New("malformed")
			}
			return wire, nil
		},
	}
}

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

	LogRecordWire.Install(testCairnLogWire())

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
	require.JSONEq(t, want, readCairnLogLine(t, lines[0]), "what appendLog writes reads back equal through the dual reader")

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
