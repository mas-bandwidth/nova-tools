package tablemodel

// TestTableModelRecordsDualRead is the reader half of the table model's table
// adoption (the owner's rules S2 and S4, readers first): every record of the
// table model (the State, the Trace of trace.json, the capture's Action and
// receipt Event, the witness Finding and the ReplayEvent) is read alike from
// its binary image (the table wire), the padded standard base64 of that image,
// its schema JSON and the legacy JSON its writer writes today; a damaged value
// of each form is refused by name (`<RECORD> REFUSED: <reason>; run: <help>`)
// and never read as absent, empty or zero; a clamped value is refused; and
// each read is counted under its record and its form.
//
// Unit tier: no clock, no socket, no store. The readers run over byte slices
// and over files this test writes into a t.TempDir().

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	tassert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTableModelRecordsDualRead(t *testing.T) {
	t.Parallel()
	refusedFalse, refusedTrue := false, true
	state := State{
		Live:   []Phys{{"t1", 1}, {"t2", 2}},
		Rows:   []RowAt{{Phys{"t1", 1}, "r1"}, {Phys{"t2", 2}, "r2"}},
		Binds:  []Bind{{Cell{Phys{"t1", 1}, "r1", "c1"}, External}},
		Data:   []Datum{{Cell{Phys{"t1", 1}, "r1", "c1"}, "m1", 1}, {External, "m2", 2}},
		Place:  []Placement{{"m1", []Location{{Phys{"t1", 1}, Cell{Phys{"t1", 1}, "r1", "c1"}}, {Phys{"t2", 2}, NoPlace}}}},
		Active: 1,
		Seen:   map[string]int{"w1": 1, "w2": 1},
	}
	receipt := Event{"id": "1-0", "verb": "cell_add", "rev_before": "0", "rev_after": "1", "members": `[{"k":"r1","to":"m1"}]`}
	trace := Trace{
		SourceSHA256: "aaaabbbbccccdddd",
		ModelSHA256:  map[string]string{"TableMachine.tla": "1111", "EpochMemberTable.tla": "2222"},
		Initial:      state,
		Steps: []TraceStep{
			{Verb: "cell_add", Args: []string{"t1", "r1", "c1", "m1", "1"}, Actor: "w1", Refused: &refusedFalse, Receipt: receipt, Model: "EpochCellAdd", State: state},
			{Verb: "cell_add", Args: []string{"t1", "r1", "c9", "m1", "1"}, Actor: "w2", Refused: &refusedTrue, Model: "EpochCellAddRefused", State: state},
			{Verb: "read_epoch", Args: []string{"w2"}, Actor: "w2", Model: "ReadEpoch", State: state},
		},
	}

	t.Run("STATE", func(t *testing.T) {
		t.Parallel()
		checkRecord(t, stateSpec, stateCodec, state)
	})
	t.Run("TRACE", func(t *testing.T) {
		t.Parallel()
		checkRecord(t, traceSpec, traceCodec, trace)
	})
	t.Run("ACTION", func(t *testing.T) {
		t.Parallel()
		checkRecord(t, actionSpec, actionCodec, Action{Verb: "row_add", Args: []string{"t1", "r1", `{"binds":{"c2":"table:t1:cell:r1:c1"}}`}, Actor: "w1"})
	})
	t.Run("EVENT", func(t *testing.T) {
		t.Parallel()
		checkRecord(t, eventSpec, eventCodec, receipt)
	})
	t.Run("FINDING", func(t *testing.T) {
		t.Parallel()
		checkRecord(t, findingSpec, findingCodec, Finding{Name: "OnePlacePerTable", Result: "confirmed", Text: "OnePlacePerTable fails: add permits m1 in two owned cells"})
	})
	t.Run("REPLAY_EVENT", func(t *testing.T) {
		t.Parallel()
		checkRecord(t, replayEventSpec, replayEventCodec, ReplayEvent{Step: "capture", Verdict: VerdictPass, Fields: []string{"transitions=3", "trace=/tmp/x/trace.json"}})
	})

	// A clamped value is refused, never read as the bound it was clamped to:
	// an integer past int64 in the schema JSON saturates in the table's JSON
	// reader, and the strict read refuses the clamp. The legacy reader never
	// clamps: the same value is malformed there. (The Action, Event, Finding
	// and ReplayEvent declare text only, which tablemodel.schema leaves
	// unbounded, so no value of theirs can clamp.)
	t.Run("clamped", func(t *testing.T) {
		t.Parallel()
		canon := stateCanon(state)
		canon["active"] = json.Number("99999999999999999999")
		clampedJSON, err := json.Marshal(canon)
		require.NoError(t, err)
		r := schemaread.NewReader(stateCodec)
		got, _, err := r.Read(clampedJSON)
		tassert.Equal(t, schemaread.ReasonClamped, refusal(t, err, "STATE").Reason, "a saturated active epoch: %s", err)
		tassert.Equal(t, State{}, got.v, "a clamped read hands back nothing")
		tassert.Equal(t, uint64(1), r.Counts().Refused(schemaread.ReasonClamped), "the clamp is counted")
		tassert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormJSON), "the clamp is counted under its form")

		tcanon := traceCanon(trace)
		tcanon["initial"] = canon
		clampedTrace, err := json.Marshal(tcanon)
		require.NoError(t, err)
		_, _, err = schemaread.NewReader(traceCodec).Read(clampedTrace)
		tassert.Equal(t, schemaread.ReasonClamped, refusal(t, err, "TRACE").Reason, "a saturated epoch inside a trace: %s", err)

		legacy, err := json.Marshal(state)
		require.NoError(t, err)
		legacy = bytes.Replace(legacy, []byte(`"active":1`), []byte(`"active":99999999999999999999`), 1)
		_, _, err = r.Read(legacy)
		tassert.Equal(t, schemaread.ReasonMalformed, refusal(t, err, "STATE").Reason, "a legacy integer past int: %s", err)
	})

	// The empty State (no table yet, every list empty as the capture writes
	// it) reads as itself in every form.
	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		checkForms(t, stateSpec, stateCodec, State{Live: []Phys{}, Rows: []RowAt{}, Binds: []Bind{}, Data: []Datum{}, Place: []Placement{}, Seen: map[string]int{}})
	})

	// trace.json as a bench wrote it (testdata/replay) reads through the file
	// reader the replay uses, and its table image, base64 and schema JSON read
	// back the same trace.
	t.Run("bench trace", func(t *testing.T) {
		t.Parallel()
		raw, err := os.ReadFile(filepath.Join("testdata", "replay", "trace.json"))
		require.NoError(t, err)
		var want Trace
		require.NoError(t, json.Unmarshal(raw, &want))
		dir := t.TempDir()
		path := filepath.Join(dir, "trace.json")
		require.NoError(t, os.WriteFile(path, raw, 0o600))
		before := traceReader.Counts().Reads(schemaread.FormLegacy)
		got, err := ReadTraceFile(path)
		require.NoError(t, err)
		tassert.True(t, reflect.DeepEqual(want, got), "the bench trace.json reads as the trace it holds")
		tassert.Equal(t, before+1, traceReader.Counts().Reads(schemaread.FormLegacy), "the file read is counted under the legacy form")
		require.Len(t, got.Steps, 32)
		checkForms(t, traceSpec, traceCodec, want)

		// a damaged trace.json is refused by name, never read as an empty trace
		require.NoError(t, os.WriteFile(path, raw[:len(raw)/2], 0o600))
		got, err = ReadTraceFile(path)
		refusal(t, err, "TRACE")
		tassert.True(t, reflect.DeepEqual(Trace{}, got), "a refused trace.json hands back nothing")
		require.NoError(t, os.WriteFile(path, nil, 0o600))
		_, err = ReadTraceFile(path)
		tassert.Equal(t, schemaread.ReasonUnknownForm, refusal(t, err, "TRACE").Reason, "an empty trace.json")
	})
}

// forms is want written in each form a reader accepts: the binary image, the
// padded standard base64 of it, the schema JSON and the legacy JSON.
func forms[V, B, M any](t *testing.T, spec recordSpec[V, B, M], codec *schemaread.Codec[record[V]], want V) map[schemaread.Form][]byte {
	t.Helper()
	wire, err := spec.wire(want)
	require.NoError(t, err)
	r := schemaread.NewReader(codec)
	text, err := r.Write(&record[V]{want}, schemaread.FormJSON)
	require.NoError(t, err)
	b64, err := r.Write(&record[V]{want}, schemaread.FormBase64)
	require.NoError(t, err)
	tassert.Equal(t, base64.StdEncoding.EncodeToString(wire), string(b64), "the base64 form is the padded standard base64 of the image")
	legacy, err := json.Marshal(want)
	require.NoError(t, err)
	return map[schemaread.Form][]byte{
		schemaread.FormBinary: wire,
		schemaread.FormBase64: b64,
		schemaread.FormJSON:   text,
		schemaread.FormLegacy: legacy,
	}
}

var allForms = []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON, schemaread.FormLegacy}

// checkForms reads want back from each form, from bytes and from a file this
// test writes, and counts each read once under its form.
func checkForms[V, B, M any](t *testing.T, spec recordSpec[V, B, M], codec *schemaread.Codec[record[V]], want V) map[schemaread.Form][]byte {
	t.Helper()
	in := forms(t, spec, codec, want)
	dir := t.TempDir()
	r := schemaread.NewReader(codec)
	for _, f := range allForms {
		got, res, err := r.Read(in[f])
		require.NoError(t, err, "%s from its %s form", spec.name, f)
		tassert.Equal(t, f, res.Form, "%s: the %s form is detected as itself", spec.name, f)
		tassert.True(t, reflect.DeepEqual(want, got.v), "%s reads alike from its %s form: got %#v want %#v", spec.name, f, got.v, want)

		path := filepath.Join(dir, spec.name+"."+f.String())
		require.NoError(t, os.WriteFile(path, in[f], 0o600))
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		got, _, err = r.Read(raw)
		require.NoError(t, err, "%s from its %s file", spec.name, f)
		tassert.True(t, reflect.DeepEqual(want, got.v), "%s reads alike from its %s file", spec.name, f)
	}
	for _, f := range allForms {
		tassert.Equal(t, uint64(2), r.Counts().Reads(f), "%s: each read counted once under the %s form", spec.name, f)
	}
	return in
}

// checkRecord is the whole dual read of one record: every form reads alike;
// every truncated or extended value of each form is refused by name and
// counted as refused; and a flipped byte is refused or reads as some value,
// never as the zero record.
func checkRecord[V, B, M any](t *testing.T, spec recordSpec[V, B, M], codec *schemaread.Codec[record[V]], want V) {
	t.Helper()
	in := checkForms(t, spec, codec, want)
	var zero V

	r := schemaread.NewReader(codec)
	tries := 0
	for _, f := range allForms {
		for _, m := range damaged(in[f]) {
			got, _, err := r.Read(m)
			require.NotNil(t, refusal(t, err, spec.name), "%s: a damaged %s (%d of %d bytes) was read as %#v", spec.name, f, len(m), len(in[f]), got.v)
			require.True(t, reflect.DeepEqual(zero, got.v), "%s: a refused read hands back nothing", spec.name)
			tries++
		}
	}
	refused := uint64(0)
	for k, n := range r.Counts().Snapshot() {
		if bytes.HasPrefix([]byte(k), []byte("refused_")) {
			refused += n
		}
	}
	tassert.Equal(t, uint64(tries), refused, "%s: every damaged read is counted as refused, by reason", spec.name)

	// A flipped byte: the table wire carries no checksum, so a flip inside a
	// text payload can read as another value; it is never read as the zero or
	// the empty record, and every refusal is named.
	flipsRefused, flips := 0, 0
	for _, f := range allForms {
		for _, i := range positions(len(in[f])) {
			m := bytes.Clone(in[f])
			m[i] ^= 0x20
			got, _, err := r.Read(m)
			flips++
			if err != nil {
				refusal(t, err, spec.name)
				flipsRefused++
				continue
			}
			require.False(t, reflect.DeepEqual(zero, got.v) || reflect.DeepEqual(spec.empty(), got.v), "%s: a flipped %s byte %d read as the zero record", spec.name, f, i)
		}
	}
	t.Logf("%s: %d of %d single-byte flips refused by name", spec.name, flipsRefused, flips)

	// No form at all, and a base64 text that is not base64, are refused by
	// name; the empty value is counted under no form.
	r = schemaread.NewReader(codec)
	_, _, err := r.Read(nil)
	tassert.Equal(t, schemaread.ReasonUnknownForm, refusal(t, err, spec.name).Reason, "%s: an empty value", spec.name)
	_, _, err = r.Read([]byte("abcd===="))
	refusal(t, err, spec.name)
	tassert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormBase64), "%s: an invalid base64 text is counted under its form", spec.name)
}

// refusal is err as the reader's named refusal of record name.
func refusal(t *testing.T, err error, name string) *schemaread.Refusal {
	t.Helper()
	var ref *schemaread.Refusal
	if !tassert.True(t, errors.As(err, &ref), "%s: want a named refusal, got %v", name, err) {
		return &schemaread.Refusal{}
	}
	tassert.Equal(t, name, ref.Name)
	tassert.Contains(t, ref.Error(), name+" REFUSED: ")
	tassert.Contains(t, ref.Error(), "; run: "+modelHelp)
	return ref
}

// positions are the offsets a damage test visits in a value of n bytes:
// every offset of a short value, and about 256 spread across a long one, the
// first and last 64 always included, so the test stays cheap.
func positions(n int) []int {
	stride := 1
	if n > 256 {
		stride = n / 128
	}
	var out []int
	for i := 0; i < n; i++ {
		if i < 64 || i >= n-64 || i%stride == 0 {
			out = append(out, i)
		}
	}
	return out
}

// damaged is in cut at each visited length and in with bytes after its end.
func damaged(in []byte) [][]byte {
	cuts := positions(len(in))
	out := make([][]byte, 0, len(cuts)+3)
	for _, n := range cuts {
		out = append(out, bytes.Clone(in[:n]))
	}
	for _, tail := range []string{"\x00", "x", "AAAA"} {
		out = append(out, append(bytes.Clone(in), tail...))
	}
	return out
}
