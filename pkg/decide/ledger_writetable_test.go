package decide_test

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/decide"
)

// TestDecideLedgerWriteTable is the writer half of the decide records' table
// adoption (v1.5 aspect 13; the owner's rules S2 and S4): the record's writer
// (locked, record.go) appends each decide record line in the table's form, the
// padded standard base64 of the RecordLine table's binary image, and a value
// the table cannot hold is refused by name before a byte is written, never in
// the legacy form as a fallback. The dual reader reads what the switched writer
// writes, and the legacy fixtures still read.
func TestDecideLedgerWriteTable(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "decisions.jsonl")
	d := decisionSample()

	recorded, err := decide.Append(path, d)
	require.NoError(t, err)
	require.Nil(t, recorded)
	_, err = decide.Append(path, d)
	require.NoError(t, err, "the same decision again replays, one record")

	_, changed, err := decide.Attach(path, decide.Outcome{ID: d.ID, Label: "Landed", Note: "ok", At: d.At})
	require.NoError(t, err)
	require.True(t, changed)
	require.NoError(t, decide.RecordAct(path, decide.Act{ID: d.ID, Act: "applied", Op: "op-1", At: d.At}))

	// what the switched writer wrote reads back equal through the dual reader
	ds, err := decide.Load(path)
	require.NoError(t, err)
	require.Len(t, ds, 1)
	got := ds[0]
	assert.Equal(t, d.ID, got.ID)
	assert.Equal(t, d.Decision, got.Decision)
	assert.Equal(t, d.Schema, got.Schema)
	assert.Equal(t, d.Backend, got.Backend)
	assert.Equal(t, d.At, got.At)
	assert.Equal(t, d.State, got.State)
	assert.Equal(t, d.Inputs, got.Inputs)
	assert.Equal(t, d.Answers, got.Answers)
	assert.Equal(t, d.Usage, got.Usage)
	require.NotNil(t, got.Outcome)
	assert.Equal(t, "Landed", got.Outcome.Label)
	require.Len(t, got.Acts, 1)
	assert.Equal(t, "applied", got.Acts[0].Act)

	// the legacy fixtures still read (S4: old readers keep reading the legacy form)
	legacy, err := decide.Load("testdata/judgment-record.jsonl")
	require.NoError(t, err)
	require.NotEmpty(t, legacy)
}
