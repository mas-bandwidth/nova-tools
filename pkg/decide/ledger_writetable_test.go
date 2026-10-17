package decide_test

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/decide"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
)

// TestDecideLedgerWriteTable is the writer half of the decide records' table
// adoption (v1.5 aspect 13; the owner's rules S2 and S4): the writer appends
// each record line in the table's form, the padded standard base64 of the
// binary image; what it writes reads back equal through the dual reader; a
// legacy fixture of the same record still reads; and a value the table cannot
// hold is refused by name before a byte is written.
func TestDecideLedgerWriteTable(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "decisions.jsonl")

	d := decisionSample()
	_, err := decide.Append(path, d)
	require.NoError(t, err)
	loaded, err := decide.Load(path)
	require.NoError(t, err)
	require.Len(t, loaded, 1)
	assert.Equal(t, d, loaded[0])

	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	require.Len(t, lines, 1)

	// the line is the padded standard base64 of the binary image (S2), opening
	// with the variable table's form byte.
	dec, err := base64.StdEncoding.DecodeString(lines[0])
	require.NoError(t, err)
	require.NotEmpty(t, dec)
	assert.Equal(t, byte(1), dec[0])

	// the dual reader reads what the writer wrote back equal, as a base64 read.
	rd := decide.NewParser()
	l, err := rd.RecordLine([]byte(lines[0]))
	require.NoError(t, err)
	require.NotNil(t, l.Decision)
	assert.Equal(t, d, *l.Decision)
	assert.Equal(t, uint64(1), rd.RecordLineCounts().Reads(schemaread.FormBase64))

	// a legacy fixture of the same record still reads (the reader keeps the
	// legacy form until the retire card).
	legacy, err := json.Marshal(map[string]any{"decision": d})
	require.NoError(t, err)
	legacyPath := filepath.Join(dir, "legacy.jsonl")
	require.NoError(t, os.WriteFile(legacyPath, append(legacy, '\n'), 0o600))
	ds, err := decide.Load(legacyPath)
	require.NoError(t, err)
	require.Len(t, ds, 1)
	assert.Equal(t, d, ds[0])

	// a value the table cannot hold (a probability past the declared bound) is
	// refused by name before a byte is written.
	refusalPath := filepath.Join(dir, "refused.jsonl")
	over := decisionSample()
	over.ID = "a13@1.0deadbeef"
	a := over.Answers["A"]
	a.P = map[string]float64{"A": 1.5}
	over.Answers["A"] = a
	before := fileSize(t, refusalPath)
	_, err = decide.Append(refusalPath, over)
	require.Error(t, err)
	var ref *schemaread.Refusal
	require.ErrorAs(t, err, &ref)
	assert.Equal(t, "RECORD_LINE", ref.Name)
	assert.Equal(t, fileSize(t, refusalPath), before)

	// an outcome and an act line are written in the table form too, and read
	// back equal through the dual reader.
	o := decide.Outcome{ID: d.ID, Label: "pass", Note: "n", At: "2026-10-17T05:00:00Z"}
	got, changed, err := decide.Attach(path, o)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, d.ID, got.ID)
	act := decide.Act{ID: d.ID, Act: "applied", Op: "op-1", At: "2026-10-17T05:01:00Z"}
	require.NoError(t, decide.RecordAct(path, act))

	raw, err = os.ReadFile(path)
	require.NoError(t, err)
	lines = strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	require.Len(t, lines, 3)
	rd = decide.NewParser()
	for i, want := range []struct {
		kind int
	}{
		{kind: 1}, // decision
		{kind: 2}, // outcome
		{kind: 3}, // act
	} {
		gotLine, err := rd.RecordLine([]byte(lines[i]))
		require.NoError(t, err, "line %d", i)
		switch want.kind {
		case 1:
			require.NotNil(t, gotLine.Decision)
			assert.Equal(t, d, *gotLine.Decision)
		case 2:
			require.NotNil(t, gotLine.Outcome)
			assert.Equal(t, o, *gotLine.Outcome)
		case 3:
			require.NotNil(t, gotLine.Act)
			assert.Equal(t, act, *gotLine.Act)
		}
	}
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	fi, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return 0
	}
	require.NoError(t, err)
	return fi.Size()
}
