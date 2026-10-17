package decide

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDecideLedgerWriteTable is the writer half of the decide record lines'
// table adoption (aspect 13; the owner's rules S2 and S4): after the readers
// adopted the table forms, the locked writer (record.go) appends each decide
// record line in the variable table's form, as the padded standard base64 of
// its image. What it writes reads back equal through the dual reader; a value
// the table cannot hold is refused by name before a byte is written and is
// never written in the legacy form as a fallback; a legacy fixture still
// reads; and files are written to and read from a t.TempDir().
func TestDecideLedgerWriteTable(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 17, 4, 7, 16, 0, time.UTC)
	atText := at.Format(time.RFC3339)
	record := filepath.Join(t.TempDir(), "record.jsonl")

	d := Decision{
		ID: "w1@1", Decision: "attempt", Schema: "0123456789abcdef", Backend: "jev",
		At: atText, State: "CARD ...",
		Inputs: map[string]string{"brief_sha256": "abc", "reason": "red"},
		Answers: map[string]Answer{"class": {Type: "choice", Value: "done", P: map[string]float64{"done": 0.93}}},
		Usage:   Usage{InputTokens: 1200, OutputTokens: 85},
	}
	_, err := Append(record, d)
	require.NoError(t, err)

	wantOutcome := Outcome{ID: d.ID, Label: "landed", Note: "ok", At: atText}
	_, changed, err := Attach(record, wantOutcome)
	require.NoError(t, err)
	assert.True(t, changed)

	wantAct := Act{ID: d.ID, Act: "applying", Op: "op-1", At: atText}
	require.NoError(t, RecordAct(record, wantAct))

	raw, err := os.ReadFile(record)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	require.Len(t, lines, 3, "one decision line, one outcome line, one act line")

	p := NewParser()
	for _, line := range lines {
		require.NotEmpty(t, line)
		wire, err := base64.StdEncoding.DecodeString(line)
		require.NoError(t, err, "each line the writer appends is padded standard base64")
		require.Greater(t, len(wire), 0)
		require.Equal(t, byte(1), wire[0], "the variable table's form byte leads the image")
	}

	rec, err := p.RecordLine([]byte(lines[0]))
	require.NoError(t, err)
	require.NotNil(t, rec.Decision)
	assert.Equal(t, d.ID, rec.Decision.ID)
	assert.Equal(t, d.Decision, rec.Decision.Decision)
	assert.Equal(t, d.Schema, rec.Decision.Schema)
	assert.Equal(t, d.Backend, rec.Decision.Backend)
	assert.Equal(t, d.At, rec.Decision.At)
	assert.Equal(t, d.State, rec.Decision.State)
	assert.Equal(t, d.Inputs, rec.Decision.Inputs)
	assert.Equal(t, d.Answers, rec.Decision.Answers)
	assert.Equal(t, d.Usage, rec.Decision.Usage)

	rec, err = p.RecordLine([]byte(lines[1]))
	require.NoError(t, err)
	require.NotNil(t, rec.Outcome)
	assert.Equal(t, wantOutcome, *rec.Outcome)

	rec, err = p.RecordLine([]byte(lines[2]))
	require.NoError(t, err)
	require.NotNil(t, rec.Act)
	assert.Equal(t, wantAct, *rec.Act)

	ds, err := Load(record)
	require.NoError(t, err)
	require.Len(t, ds, 1)
	assert.Equal(t, d.ID, ds[0].ID)
	require.NotNil(t, ds[0].Outcome)
	assert.Equal(t, wantOutcome, *ds[0].Outcome)
	assert.Equal(t, []Act{wantAct}, ds[0].Acts)

	// a value the table cannot hold is refused by name before a byte is
	// written, and the legacy form is never written as a fallback
	bad := d
	bad.ID = "w1-refused"
	bad.Answers = map[string]Answer{"class": {Type: "choice", Value: "done", P: map[string]float64{"done": 1.5}}}
	_, err = Append(record, bad)
	require.Error(t, err)
	assert.ErrorContains(t, err, "RECORD_LINE REFUSED")
	rawAfter, err := os.ReadFile(record)
	require.NoError(t, err)
	assert.Equal(t, 3, strings.Count(string(rawAfter), "\n"), "the refused line writes nothing")

	// a legacy fixture of the same record still reads through the dual reader
	legacy := filepath.Join(t.TempDir(), "legacy.jsonl")
	legacyBody := `{"decision":{"id":"x","decision":"attempt","schema":"s","backend":"b","at":"` + atText +
		`","state":"st","inputs":{"i":"v"},"answers":{"q":{"type":"choice","value":"done","p":{"done":0.93}}},"usage":{"input_tokens":1,"output_tokens":2}}}` + "\n" +
		`{"outcome":{"id":"x","label":"landed","note":"ok","at":"` + atText + `"}}` + "\n" +
		`{"act":{"id":"x","act":"applying","op":"op-1","at":"` + atText + `"}}` + "\n"
	require.NoError(t, os.WriteFile(legacy, []byte(legacyBody), 0o600))
	legacyDs, err := Load(legacy)
	require.NoError(t, err)
	require.Len(t, legacyDs, 1)
	assert.Equal(t, "x", legacyDs[0].ID)
	require.NotNil(t, legacyDs[0].Outcome)
	assert.Equal(t, "landed", legacyDs[0].Outcome.Label)
	assert.Equal(t, []Act{{ID: "x", Act: "applying", Op: "op-1", At: atText}}, legacyDs[0].Acts)
}
