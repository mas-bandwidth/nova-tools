package member

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/answerwire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMemberQueueAnswerDualRead pins the dual read of the queue answer
// (queueOut) the member's loop reads in Tick: the record is read alike from
// the QueueOut table's binary wire, the padded standard base64 of it, the
// schema JSON text form and the legacy JSON every writer still writes, each
// read equal to the legacy fixture; a damaged image of each form is refused by
// name, never read as absent, empty or zero; a clamped value is refused; and
// reads are counted per record and per form (S1, S2, S4: readers first).
func TestMemberQueueAnswerDualRead(t *testing.T) {
	t.Parallel()

	want := queueOut{
		As:    "rowan",
		Epoch: 21,
		Width: 3,
		Cards: []queueCard{
			{ID: "a11", Table: "work", Row: "a11", Col: "ready", Gen: 1, Attempt: 1, Packet: &Packet{Card: "a11", Kind: "work", Epoch: 21}},
			{ID: "a12", Table: "read", Row: "a12", Col: "working"},
		},
		Machine: "RUNNING",
	}

	legacy, err := json.Marshal(want)
	require.NoError(t, err)
	row := queueRowFromJSON(t, legacy)
	wire := queueRowSaveWire(row)
	b64 := []byte(base64.StdEncoding.EncodeToString(wire))
	sjson := queueRowSaveJSON(row)

	// the legacy form keeps its present shape, which the reader reads equal to the table forms
	var legacyRead queueOut
	require.NoError(t, json.Unmarshal(legacy, &legacyRead))
	require.Equal(t, want, legacyRead)

	cases := []struct {
		name string
		form schemaread.Form
		in   []byte
	}{
		{"binary", schemaread.FormBinary, wire},
		{"base64", schemaread.FormBase64, b64},
		{"schema json", schemaread.FormLegacy, sjson},
		{"legacy", schemaread.FormLegacy, legacy},
	}
	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := schemaread.NewReader(queueAnswerCodec())
			got, _, err := r.Read(c.in)
			require.NoError(t, err)
			assert.Equal(t, normalizeQueue(want), normalizeQueue(got))
			assert.Equal(t, uint64(1), r.Counts().Reads(c.form), "each read counted once under its form")
		})
	}

	t.Run("damage is refused by name, per form", func(t *testing.T) {
		t.Parallel()
		damaged := []struct {
			name string
			in   []byte
		}{
			{"truncated binary", wire[:len(wire)/2]},
			{"flipped binary form byte", flip(wire, 0)},
			{"garbage base64", []byte("notbase64!!!")},
			{"base64 of damaged wire", []byte(base64.StdEncoding.EncodeToString(flip(wire, 0)))},
			{"bad legacy json", []byte("{\"as\":")},
		}
		for _, d := range damaged {
			r := schemaread.NewReader(queueAnswerCodec())
			got, _, err := r.Read(d.in)
			require.Error(t, err, "%s: refused", d.name)
			assert.Contains(t, err.Error(), "QUEUE REFUSED", d.name)
			assert.Zero(t, got, "%s: never read as absent, empty or zero", d.name)
		}
	})

	t.Run("a clamped value is refused", func(t *testing.T) {
		t.Parallel()
		r := schemaread.NewReader(queueAnswerCodec())
		_, _, err := r.Read([]byte(`{"as":"rowan","width":1025}`))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "QUEUE REFUSED: clamped")
	})

	t.Run("a missing required field is refused", func(t *testing.T) {
		t.Parallel()
		r := schemaread.NewReader(queueAnswerCodec())
		_, _, err := r.Read([]byte(`{"epoch":1}`))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "QUEUE REFUSED: absent_required")
	})

	t.Run("reader present but absent is not false", func(t *testing.T) {
		t.Parallel()
		r := schemaread.NewReader(queueAnswerCodec())
		got, _, err := r.Read([]byte(`{"as":"rowan"}`))
		require.NoError(t, err)
		assert.Nil(t, got.Reader)
		got, _, err = r.Read([]byte(`{"as":"rowan","reader":false}`))
		require.NoError(t, err)
		require.NotNil(t, got.Reader)
		assert.False(t, *got.Reader)
	})
}

// queueRowFromJSON is the QueueOut table value the schema JSON text reads.
func queueRowFromJSON(t *testing.T, text []byte) *answerwire.QueueOutRow {
	t.Helper()
	var b answerwire.QueueOutBuilder
	require.True(t, b.Init())
	var rep answerwire.TableReport
	require.True(t, answerwire.QueueOutFromJson(&b, text, &rep), "schema json: %s", rep.Reason)
	require.True(t, b.Lock())
	return b.AsConst()
}

// queueRowSaveWire is the QueueOut table's binary wire.
func queueRowSaveWire(row *answerwire.QueueOutRow) []byte {
	n := answerwire.QueueOutMeasure(row)
	buf := make([]byte, n)
	answerwire.QueueOutSave(row, buf)
	return buf
}

// queueRowSaveJSON is the QueueOut table's schema JSON text form.
func queueRowSaveJSON(row *answerwire.QueueOutRow) []byte {
	n := answerwire.QueueOutToJsonMeasure(row)
	buf := make([]byte, n)
	answerwire.QueueOutToJson(row, buf)
	return buf
}

// normalizeQueue folds a nil packet notes slice to empty, so the strict table
// forms (nil) and the legacy text form ([]) compare equal (json.Unmarshal
// reads a present notes key as an empty slice).
func normalizeQueue(q queueOut) queueOut {
	for i := range q.Cards {
		if q.Cards[i].Packet != nil && q.Cards[i].Packet.Notes == nil {
			q.Cards[i].Packet.Notes = []string{}
		}
	}
	return q
}

func flip(b []byte, at int) []byte {
	out := append([]byte(nil), b...)
	if len(out) > 0 {
		out[at%len(out)] ^= 0x01
	}
	return out
}
