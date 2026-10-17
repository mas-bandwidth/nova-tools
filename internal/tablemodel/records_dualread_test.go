package tablemodel_test

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
// and over files this test writes into a t.TempDir(). The blank import of
// recordsread installs the typed half, as a program that reads the typed
// forms does.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	tm "github.com/mas-bandwidth/nova-tools/internal/tablemodel"
	_ "github.com/mas-bandwidth/nova-tools/internal/tablemodel/recordsread"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	tassert "github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTableModelRecordsDualRead(t *testing.T) {
	t.Parallel()

	refusedFalse, refusedTrue := false, true
	state := tm.State{
		Live:   []tm.Phys{{Table: "t1", Epoch: 1}, {Table: "t2", Epoch: 2}},
		Rows:   []tm.RowAt{{P: tm.Phys{Table: "t1", Epoch: 1}, Row: "r1"}, {P: tm.Phys{Table: "t2", Epoch: 2}, Row: "r2"}},
		Binds:  []tm.Bind{{Cell: tm.Cell{P: tm.Phys{Table: "t1", Epoch: 1}, Row: "r1", Col: "c1"}, Target: tm.External}},
		Data:   []tm.Datum{{Cell: tm.Cell{P: tm.Phys{Table: "t1", Epoch: 1}, Row: "r1", Col: "c1"}, Member: "m1", Score: 1}, {Cell: tm.External, Member: "m2", Score: 2}},
		Place:  []tm.Placement{{Member: "m1", Locations: []tm.Location{{P: tm.Phys{Table: "t1", Epoch: 1}, Cell: tm.Cell{P: tm.Phys{Table: "t1", Epoch: 1}, Row: "r1", Col: "c1"}}, {P: tm.Phys{Table: "t2", Epoch: 2}, Cell: tm.NoPlace}}}},
		Active: 1,
		Seen:   map[string]int{"w1": 1, "w2": 1},
	}
	receipt := tm.Event{"id": "1-0", "verb": "cell_add", "rev_before": "0", "rev_after": "1", "members": `[{"k":"r1","to":"m1"}]`}
	trace := tm.Trace{
		SourceSHA256: "aaaabbbbccccdddd",
		ModelSHA256:  map[string]string{"TableMachine.tla": "1111", "EpochMemberTable.tla": "2222"},
		Initial:      state,
		Steps: []tm.TraceStep{
			{Verb: "cell_add", Args: []string{"t1", "r1", "c1", "m1", "1"}, Actor: "w1", Refused: &refusedFalse, Receipt: receipt, Model: "EpochCellAdd", State: state},
			{Verb: "cell_add", Args: []string{"t1", "r1", "c9", "m1", "1"}, Actor: "w2", Refused: &refusedTrue, Model: "EpochCellAddRefused", State: state},
			{Verb: "read_epoch", Args: []string{}, Actor: "w2", Model: "ReadEpoch", State: state},
		},
	}

	t.Run("STATE", func(t *testing.T) {
		t.Parallel()
		checkRecord(t, tm.StateSpec, state)
	})
	t.Run("TRACE", func(t *testing.T) {
		t.Parallel()
		checkRecord(t, tm.TraceSpec, trace)
	})
	t.Run("ACTION", func(t *testing.T) {
		t.Parallel()
		checkRecord(t, tm.ActionSpec, tm.Action{Verb: "row_add", Args: []string{"t1", "r1", `{"binds":{"c2":"table:t1:cell:r1:c1"}}`}, Actor: "w1"})
	})
	t.Run("EVENT", func(t *testing.T) {
		t.Parallel()
		checkRecord(t, tm.EventSpec, receipt)
	})
	t.Run("FINDING", func(t *testing.T) {
		t.Parallel()
		checkRecord(t, tm.FindingSpec, tm.Finding{Name: "OnePlacePerTable", Result: "confirmed", Text: "OnePlacePerTable fails: add permits m1 in two owned cells"})
	})
	t.Run("REPLAY_EVENT", func(t *testing.T) {
		t.Parallel()
		checkRecord(t, tm.ReplayEventSpec, tm.ReplayEvent{Step: "capture", Verdict: tm.VerdictPass, Fields: []string{"transitions=3", "trace=/tmp/x/trace.json"}})
	})

	// A clamped value is refused, never read as the bound it was clamped to:
	// an integer past int64 in the schema JSON saturates in the table's JSON
	// reader, and the strict read refuses the clamp. The legacy reader never
	// clamps: the same value is malformed there. (The Action, Event, Finding
	// and ReplayEvent declare text only, which tablemodel.schema leaves
	// unbounded, so no value of theirs can clamp.)
	t.Run("clamped", func(t *testing.T) {
		t.Parallel()
		canon := tm.StateCanon(state)
		canon["active"] = json.Number("99999999999999999999")
		clampedJSON, err := json.Marshal(canon)
		require.NoError(t, err)
		r := schemaread.NewReader(tm.SpecCodec(tm.StateSpec))
		got, _, err := r.Read(clampedJSON)
		tassert.Equal(t, schemaread.ReasonClamped, refusal(t, err, "STATE").Reason, "a saturated active epoch: %v", err)
		tassert.True(t, reflect.DeepEqual(tm.State{}, tm.RecordValue(got)), "a clamped read hands back nothing")
		tassert.Equal(t, uint64(1), r.Counts().Refused(schemaread.ReasonClamped), "the clamp is counted")
		tassert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormJSON), "the clamp is counted under its form")

		tcanon := tm.TraceCanon(trace)
		tcanon["initial"] = canon
		clampedTrace, err := json.Marshal(tcanon)
		require.NoError(t, err)
		_, _, err = schemaread.NewReader(tm.SpecCodec(tm.TraceSpec)).Read(clampedTrace)
		tassert.Equal(t, schemaread.ReasonClamped, refusal(t, err, "TRACE").Reason, "a saturated epoch inside a trace: %v", err)

		legacy, err := json.Marshal(state)
		require.NoError(t, err)
		legacy = bytes.Replace(legacy, []byte(`"active":1`), []byte(`"active":99999999999999999999`), 1)
		_, _, err = r.Read(legacy)
		tassert.Equal(t, schemaread.ReasonMalformed, refusal(t, err, "STATE").Reason, "a legacy integer past int: %v", err)
	})

	// A field every writer writes is required in every form: a legacy Finding
	// with no name, and a trace step with no model, are refused as
	// absent_required, never read with the field empty.
	t.Run("required", func(t *testing.T) {
		t.Parallel()
		_, _, err := schemaread.NewReader(tm.SpecCodec(tm.FindingSpec)).Read([]byte(`{"Name":"","Result":"pass","Text":"x"}`))
		tassert.Equal(t, schemaread.ReasonAbsentRequired, refusal(t, err, "FINDING").Reason, "a finding with no name: %v", err)
		bad := trace
		bad.Steps = []tm.TraceStep{trace.Steps[0]}
		bad.Steps[0].Model = ""
		legacy, err := json.Marshal(bad)
		require.NoError(t, err)
		_, _, err = schemaread.NewReader(tm.SpecCodec(tm.TraceSpec)).Read(legacy)
		ref := refusal(t, err, "TRACE")
		tassert.Equal(t, schemaread.ReasonAbsentRequired, ref.Reason, "a step with no model: %v", err)
		tassert.Equal(t, "steps.model", ref.Field)
	})

	// The empty State (no table yet, every list empty as the capture writes
	// it) reads as itself in every form.
	t.Run("empty", func(t *testing.T) {
		t.Parallel()
		checkForms(t, tm.StateSpec, tm.State{Live: []tm.Phys{}, Rows: []tm.RowAt{}, Binds: []tm.Bind{}, Data: []tm.Datum{}, Place: []tm.Placement{}, Seen: map[string]int{}})
	})

	// trace.json as a bench wrote it (testdata/replay) reads through the file
	// reader the replay uses, and its table image, base64 and schema JSON read
	// back the same trace.
	t.Run("bench trace", func(t *testing.T) {
		t.Parallel()
		raw, err := os.ReadFile(filepath.Join("testdata", "replay", "trace.json"))
		require.NoError(t, err)
		var want tm.Trace
		require.NoError(t, json.Unmarshal(raw, &want))
		dir := t.TempDir()
		path := filepath.Join(dir, "trace.json")
		require.NoError(t, os.WriteFile(path, raw, 0o600))
		before := tm.TraceReader.Counts().Reads(schemaread.FormLegacy)
		got, err := tm.ReadTraceFile(path)
		require.NoError(t, err)
		tassert.True(t, reflect.DeepEqual(want, got), "the bench trace.json reads as the trace it holds")
		tassert.Equal(t, before+1, tm.TraceReader.Counts().Reads(schemaread.FormLegacy), "the file read is counted under the legacy form")
		require.Len(t, got.Steps, 32)
		checkForms(t, tm.TraceSpec, want)

		// each typed form of trace.json reads through the file reader too
		for _, f := range []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON} {
			before := tm.TraceReader.Counts().Reads(f)
			in := forms(t, tm.TraceSpec, want)[f]
			require.NoError(t, os.WriteFile(path, in, 0o600))
			got, err := tm.ReadTraceFile(path)
			require.NoError(t, err, "trace.json in its %s form", f)
			tassert.True(t, reflect.DeepEqual(want, got), "trace.json reads alike in its %s form", f)
			tassert.Equal(t, before+1, tm.TraceReader.Counts().Reads(f), "the file read is counted under the %s form", f)
		}

		// a damaged trace.json is refused by name, never read as an empty trace
		require.NoError(t, os.WriteFile(path, raw[:len(raw)/2], 0o600))
		got, err = tm.ReadTraceFile(path)
		refusal(t, err, "TRACE")
		tassert.True(t, reflect.DeepEqual(tm.Trace{}, got), "a refused trace.json hands back nothing")
		require.NoError(t, os.WriteFile(path, nil, 0o600))
		_, err = tm.ReadTraceFile(path)
		tassert.Equal(t, schemaread.ReasonUnknownForm, refusal(t, err, "TRACE").Reason, "an empty trace.json")
	})
}

var allForms = []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON, schemaread.FormLegacy}

// forms is want written in each form a reader accepts: the binary image, the
// padded standard base64 of it, the schema JSON and the legacy JSON.
func forms[V any](t *testing.T, spec tm.Spec[V], want V) map[schemaread.Form][]byte {
	t.Helper()
	wire, err := tm.SpecWire(spec, want)
	require.NoError(t, err)
	r := schemaread.NewReader(tm.SpecCodec(spec))
	text, err := r.Write(tm.RecordOf(want), schemaread.FormJSON)
	require.NoError(t, err)
	b64, err := r.Write(tm.RecordOf(want), schemaread.FormBase64)
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

// checkForms reads want back from each form, from bytes and from a file this
// test writes, and counts each read once under its form.
func checkForms[V any](t *testing.T, spec tm.Spec[V], want V) map[schemaread.Form][]byte {
	t.Helper()
	name := tm.SpecName(spec)
	in := forms(t, spec, want)
	dir := t.TempDir()
	r := schemaread.NewReader(tm.SpecCodec(spec))
	for _, f := range allForms {
		got, res, err := r.Read(in[f])
		require.NoError(t, err, "%s from its %s form", name, f)
		tassert.Equal(t, f, res.Form, "%s: the %s form is detected as itself", name, f)
		tassert.True(t, reflect.DeepEqual(want, tm.RecordValue(got)), "%s reads alike from its %s form", name, f)

		path := filepath.Join(dir, name+"."+f.String())
		require.NoError(t, os.WriteFile(path, in[f], 0o600))
		raw, err := os.ReadFile(path)
		require.NoError(t, err)
		got, _, err = r.Read(raw)
		require.NoError(t, err, "%s from its %s file", name, f)
		tassert.True(t, reflect.DeepEqual(want, tm.RecordValue(got)), "%s reads alike from its %s file", name, f)
	}
	for _, f := range allForms {
		tassert.Equal(t, uint64(2), r.Counts().Reads(f), "%s: each read counted once under the %s form", name, f)
	}
	return in
}

// checkRecord is the whole dual read of one record: every form reads alike;
// every truncated or extended value of each form is refused by name and
// counted as refused; and a flipped byte is refused or reads as some value,
// never as the zero or the empty record.
func checkRecord[V any](t *testing.T, spec tm.Spec[V], want V) {
	t.Helper()
	name := tm.SpecName(spec)
	in := checkForms(t, spec, want)
	var zero V

	r := schemaread.NewReader(tm.SpecCodec(spec))
	tries := 0
	for _, f := range allForms {
		for _, m := range damaged(in[f]) {
			got, _, err := r.Read(m)
			require.NotNil(t, refusal(t, err, name), "%s: a damaged %s (%d of %d bytes) was read", name, f, len(m), len(in[f]))
			require.True(t, reflect.DeepEqual(zero, tm.RecordValue(got)), "%s: a refused read hands back nothing", name)
			tries++
		}
	}
	refused := uint64(0)
	for k, n := range r.Counts().Snapshot() {
		if bytes.HasPrefix([]byte(k), []byte("refused_")) {
			refused += n
		}
	}
	tassert.Equal(t, uint64(tries), refused, "%s: every damaged read is counted as refused, by reason", name)

	// A flipped byte: the table wire carries no checksum, so a flip inside a
	// text payload can read as another value; it is never read as the zero or
	// the empty record, and every refusal is named.
	empty := tm.SpecEmpty(spec)
	flipsRefused, flips := 0, 0
	for _, f := range allForms {
		for _, i := range positions(len(in[f])) {
			m := bytes.Clone(in[f])
			m[i] ^= 0x20
			got, _, err := r.Read(m)
			flips++
			if err != nil {
				refusal(t, err, name)
				flipsRefused++
				continue
			}
			v := tm.RecordValue(got)
			require.False(t, reflect.DeepEqual(zero, v) || reflect.DeepEqual(empty, v), "%s: a flipped %s byte %d read as the empty record", name, f, i)
		}
	}
	t.Logf("%s: %d of %d single-byte flips refused by name", name, flipsRefused, flips)

	// No form at all, and a base64 text that is not base64, are refused by
	// name; the empty value is counted under no form.
	r = schemaread.NewReader(tm.SpecCodec(spec))
	_, _, err := r.Read(nil)
	tassert.Equal(t, schemaread.ReasonUnknownForm, refusal(t, err, name).Reason, "%s: an empty value", name)
	_, _, err = r.Read([]byte("abcd===="))
	refusal(t, err, name)
	tassert.Equal(t, uint64(1), r.Counts().Reads(schemaread.FormBase64), "%s: an invalid base64 text is counted under its form", name)
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
	tassert.Contains(t, ref.Error(), "; run: "+tm.ModelHelp)
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
