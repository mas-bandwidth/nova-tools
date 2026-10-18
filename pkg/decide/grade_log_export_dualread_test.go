package decide_test

import (
	"bytes"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/decide"
	"github.com/mas-bandwidth/nova-tools/pkg/decidewire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
)

// TestGradeLogExportDualRead is the reader half of the sprint log export's
// table adoption (v1.5 aspect 13; the owner's rules S2 and S4): the export
// ReadLog reads is read alike from its binary image (the LogExport table
// wire), the padded standard base64 of that image, the schema JSON text and
// the legacy `nova-sprint log --json` text with keys LogLine does not name;
// a truncated, a flipped-byte and an out-of-kind value of each form is
// refused by name (`LOG_EXPORT REFUSED: <reason>; run: <help>`) through
// ReadLog's error, never read as absent or empty; and every read is counted
// under its form. The schema declares no bound on a line (ReadLog bounds
// none), so no value of LogExport can be clamped: the out-of-range value of
// each form is the one its framing or its kind refuses (a count over its
// length on the wire, a bool that is a number in text).
//
// ReadLog is the only reader of the package counters in this package's tests,
// so the subtests run in order and read the counters' deltas.
func TestGradeLogExportDualRead(t *testing.T) {
	t.Parallel()

	wire, schemaJSON := logExportImage(t, false)
	forms := []struct {
		name string
		form schemaread.Form
		in   []byte
	}{
		{"binary", schemaread.FormBinary, wire},
		{"base64", schemaread.FormBase64, []byte(base64.StdEncoding.EncodeToString(wire))},
		{"json", schemaread.FormJSON, schemaJSON},
		{"legacy", schemaread.FormLegacy, []byte(logExportLegacyText)},
	}
	counts := decide.LogExportCounts()

	t.Run("every form reads to the legacy facts", func(t *testing.T) {
		want, err := decide.ReadLog(strings.NewReader(logExportLegacyText))
		require.NoError(t, err)
		require.Len(t, want, 2)
		assert.Equal(t, "landed", want["c1"].State)
		assert.Equal(t, "flash", want["c1"].Start)
		assert.True(t, want["c1"].FirstFailed)
		assert.Equal(t, 2, want["c1"].MaxAttempt)
		assert.True(t, want["c1"].Escalated)
		assert.Equal(t, decide.LabelDropped, want["c2"].State)
		for _, f := range forms {
			before := counts.Reads(f.form)
			got, err := decide.ReadLog(strings.NewReader(string(f.in)))
			require.NoError(t, err, f.name)
			assert.Equal(t, want, got, f.name)
			assert.Equal(t, before+1, counts.Reads(f.form), "%s is counted on its form", f.name)
		}
	})

	t.Run("damage of each form is refused by name and counted on its form", func(t *testing.T) {
		flip := func(in []byte, at int, b byte) []byte {
			out := bytes.Clone(in)
			out[at] = b
			return out
		}
		colon := bytes.IndexByte(schemaJSON, ':')
		legacyColon := strings.IndexByte(logExportLegacyText, ':')
		cases := []struct {
			name, reason string
			form         schemaread.Form
			in           []byte
		}{
			{"binary truncated", schemaread.ReasonWireDamaged, schemaread.FormBinary, wire[:len(wire)-1]},
			{"binary flipped", schemaread.ReasonMalformed, schemaread.FormBinary, flip(wire, len(wire)/2, wire[len(wire)/2]^0x80)},
			{"binary form byte", schemaread.ReasonUnknownForm, schemaread.FormBinary, flip(wire, 0, 0x02)},
			{"binary count over its length", "count_over_length", schemaread.FormBinary, flip(wire, 3, 0x02)},
			{"base64 truncated", schemaread.ReasonMalformed, schemaread.FormBase64, []byte(base64.StdEncoding.EncodeToString(wire))[:10]},
			{"base64 of a truncated wire", schemaread.ReasonWireDamaged, schemaread.FormBase64, []byte(base64.StdEncoding.EncodeToString(wire[:len(wire)-1]))},
			{"base64 flipped", schemaread.ReasonMalformed, schemaread.FormBase64, []byte(base64.StdEncoding.EncodeToString(flip(wire, len(wire)/2, wire[len(wire)/2]^0x80)))},
			{"base64 count over its length", "count_over_length", schemaread.FormBase64, []byte(base64.StdEncoding.EncodeToString(flip(wire, 3, 0x02)))},
			{"json truncated", schemaread.ReasonMalformed, schemaread.FormJSON, schemaJSON[:len(schemaJSON)-2]},
			{"json flipped", schemaread.ReasonMalformed, schemaread.FormJSON, flip(schemaJSON, colon, ';')},
			{"json bool out of kind", schemaread.ReasonKindMismatch, schemaread.FormJSON, bytes.Replace(schemaJSON, []byte(`"removed": false`), []byte(`"removed": 2`), 1)},
			{"legacy truncated", schemaread.ReasonMalformed, schemaread.FormLegacy, []byte(logExportLegacyText[:len(logExportLegacyText)-2])},
			{"legacy flipped", schemaread.ReasonMalformed, schemaread.FormLegacy, flip([]byte(logExportLegacyText), legacyColon, ';')},
			{"legacy bool out of kind", schemaread.ReasonMalformed, schemaread.FormLegacy, []byte(`{"lines":[{"card":"c1","primary":"c1","removed":2}]}`)},
			{"legacy set value not text", schemaread.ReasonMalformed, schemaread.FormLegacy, []byte(`{"lines":[{"card":"c1","primary":"c1","set":{"cost_record:1":1}}]}`)},
			{"binary set key twice", schemaread.ReasonMalformed, schemaread.FormBinary, logExportDupImage(t)},
			{"nothing", schemaread.ReasonUnknownForm, schemaread.FormNone, nil},
		}
		for _, c := range cases {
			before, refusedBefore := counts.Reads(c.form), counts.Refused(c.reason)
			got, err := decide.ReadLog(strings.NewReader(string(c.in)))
			assert.Nil(t, got, c.name)
			var ref *schemaread.Refusal
			if assert.True(t, errors.As(err, &ref), "%s: want a named refusal, got %v", c.name, err) {
				assert.Equal(t, "LOG_EXPORT", ref.Name, c.name)
				assert.Equal(t, c.reason, ref.Reason, c.name)
				assert.Regexp(t, `^LOG_EXPORT REFUSED: [^;]+; run: nova-decide score-grades -h$`, ref.Error(), c.name)
				assert.Contains(t, err.Error(), "the log is not a nova-sprint log --json export: "+ref.Error(), c.name)
			}
			if c.form != schemaread.FormNone {
				assert.Equal(t, before+1, counts.Reads(c.form), "%s is counted on its form", c.name)
			}
			assert.Equal(t, refusedBefore+1, counts.Refused(c.reason), "%s is counted under its reason", c.name)
		}
	})

	t.Run("every truncation and every flipped byte of the image is refused", func(t *testing.T) {
		for n := 1; n < len(wire); n++ {
			_, err := decide.ReadLog(bytes.NewReader(wire[:n]))
			assert.Error(t, err, "truncated to %d", n)
		}
		for at := range wire {
			out := bytes.Clone(wire)
			out[at] ^= 0x80
			_, err := decide.ReadLog(bytes.NewReader(out))
			var ref *schemaread.Refusal
			assert.True(t, errors.As(err, &ref), "byte %d flipped: want a named refusal, got %v", at, err)
		}
	})

	t.Run("the legacy export reads as it always did", func(t *testing.T) {
		before := counts.Reads(schemaread.FormLegacy)
		for _, in := range []string{`{"lines":[]}`, `{}`, " \n" + logExportLegacyText} {
			_, err := decide.ReadLog(strings.NewReader(in))
			require.NoError(t, err, in)
		}
		facts, err := decide.ReadLog(strings.NewReader(`{"lines":[]}`))
		require.NoError(t, err)
		assert.Empty(t, facts)
		// a set value naming a schema-only key is text, not a key: still legacy
		facts, err = decide.ReadLog(strings.NewReader(`{"lines":[{"card":"c3","primary":"c3","table":"work","to":"work:landed","set":{"note":"\"table_name\": [\"set\": ["}}]}`))
		require.NoError(t, err)
		assert.Equal(t, "landed", facts["c3"].State)
		assert.Equal(t, before+5, counts.Reads(schemaread.FormLegacy))
	})

	t.Run("the schema JSON text tolerates and counts an unknown key", func(t *testing.T) {
		before := counts.Unknown()
		in := bytes.Replace(schemaJSON, []byte(`"card"`), []byte(`"newer": 1, "card"`), 1)
		_, err := decide.ReadLog(bytes.NewReader(in))
		require.NoError(t, err)
		assert.Equal(t, before+1, counts.Unknown())
	})
}

// logExportLegacyText is a `nova-sprint log --json` export as the log verb
// writes it today, with keys LogLine does not name (at, actor, op, from, and
// a top-level count), and cost_record texts inside set.
const logExportLegacyText = `{"count":3,"lines":[` +
	`{"at":"2026-10-17T01:00:00Z","actor":"rowan","op":"take","card":"c1","primary":"c1","table":"work","from":"work:ready","to":"work:working","set":{"cost_record:1":"kind=work attempt=1 on_tier=flash end=failed on_route=r1"}},` +
	`{"at":"2026-10-17T02:00:00Z","actor":"rowan","op":"land","card":"c1","primary":"c1","table":"work","to":"work:landed","removed":false,"set":{"cost_record:2":"kind=work attempt=2 on_tier=pro end=ok on_route=-"}},` +
	`{"at":"2026-10-17T03:00:00Z","actor":"rowan","op":"drop","card":"c2","primary":"c2","table":"work","to":"work:waiting","removed":true,"set":{"outcome":"dropped"}}` +
	`]}`

type logLineSample struct {
	card, table, to string
	removed         bool
	set             [][2]string
}

// logExportSample is logExportLegacyText's lines, the fields LogLine names.
var logExportSample = []logLineSample{
	{"c1", "work", "work:working", false, [][2]string{{"cost_record:1", "kind=work attempt=1 on_tier=flash end=failed on_route=r1"}}},
	{"c1", "work", "work:landed", false, [][2]string{{"cost_record:2", "kind=work attempt=2 on_tier=pro end=ok on_route=-"}}},
	{"c2", "work", "work:waiting", true, [][2]string{{"outcome", "dropped"}}},
}

// logExportImage is the sample's table wire and its schema JSON text; dup
// names the first line's set key twice.
func logExportImage(t *testing.T, dup bool) ([]byte, []byte) {
	t.Helper()
	var b decidewire.LogExportBuilder
	require.True(t, b.Init())
	root := b.GetRoot()
	for i, s := range logExportSample {
		l := decidewire.LogExportLinesAdd(&b.Main, &root.Lines)
		setS(&b.Main, &l.Card, s.card)
		setS(&b.Main, &l.Primary, s.card)
		setS(&b.Main, &l.TableName, s.table)
		setS(&b.Main, &l.To, s.to)
		l.Removed = s.removed
		set := s.set
		if dup && i == 0 {
			set = append(set, set[0])
		}
		for _, kv := range set {
			e := decidewire.LogLineSetAdd(&b.Main, &l.Set)
			setS(&b.Main, &e.Key, kv[0])
			setS(&b.Main, &e.Value, kv[1])
		}
	}
	wire := wireSave(t, &b, decidewire.LogExportMeasure, decidewire.LogExportSave)
	n := decidewire.LogExportToJsonMeasure(b.AsConst())
	require.Positive(t, n)
	text := make([]byte, n)
	require.Equal(t, n, decidewire.LogExportToJson(b.AsConst(), text))
	return wire, text
}

func logExportDupImage(t *testing.T) []byte {
	t.Helper()
	wire, _ := logExportImage(t, true)
	return wire
}
