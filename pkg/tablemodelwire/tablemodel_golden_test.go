package tablemodelwire

// TestModelsRecordsGolden is the golden test of the tablemodel definitions
// (schemas/tablemodel): every FIXED table of the area (the table model's
// state, action, receipt, trace head and trace step, the witness finding, the
// replay event, the suite step and its result, and the convergence verb's
// state) round-trips as the binary wire and as padded standard base64 of that
// wire, each read strictly through pkg/schemaread (schemas/README.md; the
// owner's S1 and S2); a value past each declared bound is refused by name on
// write and on read, never clamped; and a damaged image of each form is
// refused by name. The sample of each table is filled from the generated
// struct itself, so the test and the definition cannot drift apart, and the
// SHA-256 of each sample's wire is pinned so a change of layout or encoding
// shows as a changed digest.

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/tablemodel"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const modelsHelp = "nova-sprint help tablemodel"

// ranged names the scalar fields that declare a min and max in the schema
// (tablemodel.schema): the bound is the schema's, and each is also the rule
// the comment beside the field names.
var ranged = map[string][2]int64{
	"Epoch":     {0, 1000000},        // ModelPhys, ModelSeen: a counter that starts at 1
	"Active":    {0, 1000000},        // ModelState: the active epoch
	"StepCount": {0, 40},             // ModelTrace: Actions() is 32 calls
	"Workers":   {0, 1024},           // ModelStep: TLC workers
	"Code":      {0, 255},            // ModelStepResult: an exit status
	"Seconds":   {0, 86400000000000}, // ModelStepResult: a duration of at most one day
	"Widening":  {0, 1000000},        // ConvergeStreamEntry: consecutive ticks
}

// knob is one declared bound of a table: a string length, an array count or a
// ranged scalar, reached from the root value by path.
type knob struct {
	name     string
	max, min int64
	width    int // bytes of the scalar on the wire
	// at returns the scalar the bound is on; array returns the string or element
	// array beside it, nil for a ranged scalar.
	at    func(root reflect.Value) reflect.Value
	array func(root reflect.Value) reflect.Value
	isLen bool
}

type step struct {
	field int // the struct field to enter
	elem  bool
}

func walk(v reflect.Value, path []step) reflect.Value {
	for _, s := range path {
		v = v.Field(s.field)
		if s.elem {
			v = v.Index(0)
		}
	}
	return v
}

// knobs lists every declared bound reachable from t.
func knobs(t reflect.Type, prefix string, path []step) []knob {
	var out []knob
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		here := append(append([]step{}, path...), step{field: i})
		name := prefix + f.Name
		switch {
		case f.Type.Kind() == reflect.Struct:
			out = append(out, knobs(f.Type, name+".", here)...)
		case f.Type.Kind() == reflect.Array && f.Type.Elem().Kind() == reflect.Uint8:
			if _, ok := t.FieldByName(f.Name + "Length"); !ok {
				continue
			}
			li, _ := t.FieldByName(f.Name + "Length")
			lp := append(append([]step{}, path...), step{field: li.Index[0]})
			ap := here
			out = append(out, knob{name: name + "Length", max: int64(f.Type.Len()), width: 4, isLen: true,
				at:    func(r reflect.Value) reflect.Value { return walk(r, lp) },
				array: func(r reflect.Value) reflect.Value { return walk(r, ap) }})
		case f.Type.Kind() == reflect.Array:
			ci, ok := t.FieldByName(f.Name + "Count")
			if !ok {
				continue
			}
			cp := append(append([]step{}, path...), step{field: ci.Index[0]})
			ap := here
			out = append(out, knob{name: name + "Count", max: int64(f.Type.Len()), width: 4,
				at:    func(r reflect.Value) reflect.Value { return walk(r, cp) },
				array: func(r reflect.Value) reflect.Value { return walk(r, ap) }})
			if f.Type.Elem().Kind() == reflect.Struct {
				out = append(out, knobs(f.Type.Elem(), name+"[0].", append(append([]step{}, path...), step{field: i, elem: true}))...)
			}
		case f.Type.Kind() == reflect.Int32 || f.Type.Kind() == reflect.Int64:
			b, ok := ranged[f.Name]
			if !ok {
				continue
			}
			w := 4
			if f.Type.Kind() == reflect.Int64 {
				w = 8
			}
			p := here
			out = append(out, knob{name: name, min: b[0], max: b[1], width: w,
				at: func(r reflect.Value) reflect.Value { return walk(r, p) }})
		}
	}
	return out
}

// fill sets every field of v to a short valid value: strings of three bytes,
// two elements per array (one when the bound is one), 7 in a ranged scalar,
// true in a bool and 1000.5 in a float.
func fill(v reflect.Value, seed *int) {
	t := v.Type()
	for i := 0; i < t.NumField(); i++ {
		f, fv := t.Field(i), v.Field(i)
		switch {
		case f.Type.Kind() == reflect.Struct:
			fill(fv, seed)
		case f.Type.Kind() == reflect.Array && f.Type.Elem().Kind() == reflect.Uint8:
			if _, ok := t.FieldByName(f.Name + "Length"); !ok {
				continue
			}
			*seed++
			s := []byte{byte('a' + *seed%26), byte('a' + (*seed+1)%26), byte('a' + (*seed+2)%26)}
			for j := 0; j < 3 && j < fv.Len(); j++ {
				fv.Index(j).SetUint(uint64(s[j]))
			}
			v.FieldByName(f.Name + "Length").SetInt(int64(min(3, fv.Len())))
		case f.Type.Kind() == reflect.Array:
			if _, ok := t.FieldByName(f.Name + "Count"); !ok {
				continue
			}
			n := min(2, fv.Len())
			v.FieldByName(f.Name + "Count").SetInt(int64(n))
			if f.Type.Elem().Kind() == reflect.Struct {
				for j := 0; j < n; j++ {
					fill(fv.Index(j), seed)
				}
			}
		case f.Type.Kind() == reflect.Int32 || f.Type.Kind() == reflect.Int64:
			if strings.HasSuffix(f.Name, "Length") || strings.HasSuffix(f.Name, "Count") && f.Name != "StepCount" {
				continue
			}
			*seed++
			fv.SetInt(7)
		case f.Type.Kind() == reflect.Bool:
			fv.SetBool(true)
		case f.Type.Kind() == reflect.Float64:
			fv.SetFloat(1000.5)
		}
	}
}

// fixedOps is the generated fixed-form surface of one table.
type fixedOps[T any] struct {
	name      string
	measure   func(count int64) int64
	save      func([]T, []byte) int64
	saveWhy   func([]T, []byte) (int64, error)
	load      func([]T, []byte, []TableFixedEntry, *TableReport) int64
	planCount int
	body      func([]byte, *T)
	recBytes  int64
	reset     func(*T)
	wire      string // the pinned SHA-256 of the sample's wire
}

// codec is the strict read and write over the generated fixed functions. The
// facade rules are UNION.md section 4.6: refuse every clamp, refuse every
// value past a declared bound before a byte is written (the generated writer
// checks them too and answers invalid_value), and read the one form byte of
// the fixed form.
func (o fixedOps[T]) codec(ks []knob) *schemaread.Codec[T] {
	return &schemaread.Codec[T]{
		Name:  o.name,
		Help:  modelsHelp,
		Reset: o.reset,
		Forms: []byte{TableFixedForm},
		LoadWire: func(v *T, wire []byte) schemaread.Report {
			vals := make([]T, 1)
			var rep TableReport
			n := o.load(vals, wire, make([]TableFixedEntry, o.planCount), &rep)
			r := schemaread.Report{Unknown: int(rep.Unknown), KindMismatch: int(rep.KindMismatch), Clamped: int(rep.Clamped),
				Malformed: rep.Malformed, Reason: rep.Reason}
			if n != 1 && r.Reason == "" && !r.Malformed {
				r.Malformed = true
			}
			*v = vals[0]
			return r
		},
		Constraints: func(v *T, c *schemaread.Checker) { check(reflect.ValueOf(v).Elem(), ks, c) },
		SaveWire: func(v *T) []byte {
			buf := make([]byte, o.measure(1))
			if n := o.save([]T{*v}, buf); n < 0 {
				return nil
			}
			return buf
		},
	}
}

// check states the bounds once: a length or count past its bound, a ranged
// scalar outside its range, text that is not UTF-8. A path through an array
// element is checked on element 0, which the sample fills.
func check(root reflect.Value, ks []knob, c *schemaread.Checker) {
	for _, k := range ks {
		n := k.at(root).Int()
		lo := k.min
		if k.array != nil {
			lo = 0
		}
		c.Bound(k.name, n, lo, k.max)
		if k.isLen && n >= 0 && n <= k.max {
			b := make([]byte, n)
			for i := range b {
				b[i] = byte(k.array(root).Index(i).Uint())
			}
			c.Text(k.name, string(b), int(k.max))
		}
	}
}

// image returns the one-record wire of v.
func image[T any](t *testing.T, o fixedOps[T], v T) []byte {
	t.Helper()
	buf := make([]byte, o.measure(1))
	n, err := o.saveWhy([]T{v}, buf)
	require.NoError(t, err, o.name)
	require.EqualValues(t, len(buf), n, o.name)
	return buf
}

// reasonOf is the name a refusal carries.
func reasonOf(t *testing.T, err error) string {
	t.Helper()
	var ref *schemaread.Refusal
	require.True(t, errors.As(err, &ref), "a refusal by name: %v", err)
	require.NotEmpty(t, ref.Reason)
	return ref.Reason
}

// goldenFixed runs the whole contract over one table.
func goldenFixed[T any](t *testing.T, o fixedOps[T]) {
	t.Helper()
	var sample T
	ks := knobs(reflect.TypeOf(sample), "", nil)
	require.NotEmpty(t, ks, o.name+" declares a bound")
	seed := 0
	fill(reflect.ValueOf(&sample).Elem(), &seed)

	rd := schemaread.NewReader(o.codec(ks))
	wire, err := rd.Write(&sample, schemaread.FormBinary)
	require.NoError(t, err, o.name)
	require.Equal(t, wire, image(t, o, sample), o.name)
	text, err := rd.Write(&sample, schemaread.FormBase64)
	require.NoError(t, err, o.name)
	assert.Equal(t, base64.StdEncoding.EncodeToString(wire), string(text), o.name+": base64 is padded standard base64 of the wire")
	digest := sha256.Sum256(wire)
	assert.Equal(t, o.wire, hex.EncodeToString(digest[:]), o.name+": the wire of the sample changed (pin it again only for an agreed layout change)")

	// Both forms read back to the same value, and each read is counted once on
	// the form it arrived in.
	for _, in := range []struct {
		form schemaread.Form
		data []byte
	}{{schemaread.FormBinary, wire}, {schemaread.FormBase64, text}} {
		got, res, err := rd.Read(in.data)
		require.NoError(t, err, o.name+" "+in.form.String())
		assert.Equal(t, in.form, res.Form, o.name)
		assert.True(t, reflect.DeepEqual(sample, got), "%s %s: the read value differs", o.name, in.form)
	}
	assert.EqualValues(t, 1, rd.Counts().Reads(schemaread.FormBinary), o.name)
	assert.EqualValues(t, 1, rd.Counts().Reads(schemaread.FormBase64), o.name)

	// A value past each declared bound is refused by name on write: by the
	// reader's facade and by the generated writer, which answers invalid_value.
	for _, k := range ks {
		for _, over := range overs(k) {
			bad := sample
			k.at(reflect.ValueOf(&bad).Elem()).SetInt(over)
			if k.isLen || k.array != nil {
				// a length or count past the array it counts is refused before
				// any element is touched
				_, err := rd.Write(&bad, schemaread.FormBinary)
				require.Error(t, err, "%s %s=%d is refused on write", o.name, k.name, over)
				assert.Equal(t, schemaread.ReasonClamped, reasonOf(t, err), "%s %s", o.name, k.name)
			} else {
				_, err := rd.Write(&bad, schemaread.FormBinary)
				require.Error(t, err, "%s %s=%d is refused on write", o.name, k.name, over)
			}
			_, err := o.saveWhy([]T{bad}, make([]byte, o.measure(1)))
			require.ErrorIs(t, err, TableRefuseInvalidValue, "%s %s=%d: the generated writer refuses", o.name, k.name, over)
		}
	}

	// A wire carrying a value past a bound is refused by name on read, never
	// clamped: the scalar's place on the wire is found by writing the sample
	// twice with the scalar at 1 and at 2 and diffing the images.
	for _, k := range ks {
		at := wireOffset(t, o, sample, k)
		for _, over := range overs(k) {
			bad := append([]byte{}, wire...)
			put(bad[at:at+k.width], over)
			for _, in := range [][]byte{bad, []byte(base64.StdEncoding.EncodeToString(bad))} {
				got, _, err := rd.Read(in)
				require.Error(t, err, "%s %s=%d on the wire is refused", o.name, k.name, over)
				assert.Contains(t, []string{schemaread.ReasonClamped, schemaread.ReasonMalformed, TableFixedReasonTextIllFormed}, reasonOf(t, err), "%s %s", o.name, k.name)
				assert.True(t, reflect.DeepEqual(got, *new(T)), "%s %s: a refused read returns nothing", o.name, k.name)
			}
		}
	}

	// A damaged image of each form is refused by name.
	hashAt, layoutAt := TableFixedHashAt, TableFixedHeaderBytes
	damaged := []struct {
		what   string
		mutate func([]byte) []byte
		reason string
	}{
		{"previous form byte", func(b []byte) []byte { b[0] = 1; return b }, schemaread.ReasonUnknownForm},
		{"message form byte", func(b []byte) []byte { b[0] = 2; return b }, schemaread.ReasonUnknownForm},
		{"newer form byte", func(b []byte) []byte { b[0] = 9; return b }, schemaread.ReasonUnknownForm},
		{"reserved byte set", func(b []byte) []byte { b[3] = 1; return b }, TableFixedReasonReservedNotZero},
		{"layout hash flipped", func(b []byte) []byte { b[hashAt] ^= 0xff; return b }, TableFixedReasonLayoutUnknown},
		{"layout length past the image", func(b []byte) []byte { binary.LittleEndian.PutUint32(b[layoutAt:], 1<<30); return b }, TableFixedReasonLayoutMalformed},
		{"layout byte flipped", func(b []byte) []byte { b[layoutAt+4] ^= 0xff; return b }, TableFixedReasonLayoutMalformed},
		{"record hash flipped", func(b []byte) []byte { b[len(b)-int(o.recBytes)] ^= 0xff; return b }, TableFixedReasonNoLayout},
		{"last byte cut", func(b []byte) []byte { return b[:len(b)-1] }, TableFixedReasonTruncated},
		{"header only", func(b []byte) []byte { return b[:TableFixedHeaderBytes] }, TableFixedReasonTruncated},
		{"a second record", func(b []byte) []byte { return append(b, b[len(b)-int(o.recBytes):]...) }, TableFixedReasonBatchTooLarge},
	}
	for _, d := range damaged {
		bad := d.mutate(append([]byte{}, wire...))
		for _, in := range []struct {
			form schemaread.Form
			data []byte
		}{{schemaread.FormBinary, bad}, {schemaread.FormBase64, []byte(base64.StdEncoding.EncodeToString(bad))}} {
			_, res, err := rd.Read(in.data)
			require.Error(t, err, "%s %s %s", o.name, in.form, d.what)
			assert.Equal(t, d.reason, reasonOf(t, err), "%s %s %s", o.name, in.form, d.what)
			assert.Equal(t, in.form, res.Form, "%s %s", o.name, d.what)
		}
	}
	// Damage of the text form itself: bytes that are not base64, a missing
	// pad, and nothing at all.
	ts := string(text)
	for _, bad := range []string{ts[:len(ts)-1], ts[:5] + "!" + ts[6:], ts + "A"} {
		_, res, err := rd.Read([]byte(bad))
		require.Error(t, err, o.name+" damaged base64")
		assert.Equal(t, schemaread.ReasonMalformed, reasonOf(t, err), o.name)
		assert.Equal(t, schemaread.FormBase64, res.Form, o.name)
	}
	_, _, err = rd.Read(nil)
	require.Error(t, err, o.name)
	assert.Equal(t, schemaread.ReasonUnknownForm, reasonOf(t, err), o.name)
	assert.NotZero(t, rd.Counts().Refused(TableFixedReasonTruncated), o.name+": refusals are counted by name")
}

// overs are the values past a bound: one over the maximum, and for a ranged
// scalar one under the minimum.
func overs(k knob) []int64 {
	if k.array != nil {
		return []int64{k.max + 1, -1}
	}
	return []int64{k.max + 1, k.min - 1}
}

func put(b []byte, v int64) {
	if len(b) == 8 {
		binary.LittleEndian.PutUint64(b, uint64(v))
		return
	}
	binary.LittleEndian.PutUint32(b, uint32(int32(v)))
}

// wireOffset finds where k lives in the image by diffing two images that
// differ only in k.
func wireOffset[T any](t *testing.T, o fixedOps[T], sample T, k knob) int {
	t.Helper()
	a, b := sample, sample
	// a bound's value is set low and mid-range: a count or length of 1 and 2
	// are both within every bound this area declares.
	k.at(reflect.ValueOf(&a).Elem()).SetInt(k.min + 1)
	k.at(reflect.ValueOf(&b).Elem()).SetInt(k.min + 2)
	ia, ib := image(t, o, a), image(t, o, b)
	for i := range ia {
		if ia[i] != ib[i] {
			return i
		}
	}
	t.Fatalf("%s %s: the two images are identical", o.name, k.name)
	return 0
}

func TestModelsRecordsGolden(t *testing.T) {
	t.Parallel()

	t.Run("ModelState", func(t *testing.T) {
		t.Parallel()
		goldenFixed(t, fixedOps[ModelState]{name: "MODELSTATE", measure: ModelStateFixedMeasure, save: ModelStateFixedSave,
			saveWhy: ModelStateFixedSaveReason, load: ModelStateFixedLoad, planCount: int(ModelStateFixedPlan.Count),
			body: ModelStateFixedWriteBody, recBytes: ModelStateFixedRecordBytes, reset: ModelStateReset,
			wire: "1d9b4313171efd8c5148e2fe761bbbfacbcd2c8e225d9901d3269ea9ba1c4f59"})
	})
	t.Run("ModelAction", func(t *testing.T) {
		t.Parallel()
		goldenFixed(t, fixedOps[ModelAction]{name: "MODELACTION", measure: ModelActionFixedMeasure, save: ModelActionFixedSave,
			saveWhy: ModelActionFixedSaveReason, load: ModelActionFixedLoad, planCount: int(ModelActionFixedPlan.Count),
			body: ModelActionFixedWriteBody, recBytes: ModelActionFixedRecordBytes, reset: ModelActionReset,
			wire: "5f7c5fee5e02689a2b6b4e1cf0cbde32c11a7b0048df4991237a2d9730fc765e"})
	})
	t.Run("ModelEvent", func(t *testing.T) {
		t.Parallel()
		goldenFixed(t, fixedOps[ModelEvent]{name: "MODELEVENT", measure: ModelEventFixedMeasure, save: ModelEventFixedSave,
			saveWhy: ModelEventFixedSaveReason, load: ModelEventFixedLoad, planCount: int(ModelEventFixedPlan.Count),
			body: ModelEventFixedWriteBody, recBytes: ModelEventFixedRecordBytes, reset: ModelEventReset,
			wire: "47066da0bb23346f104a52e8769977bfac1573cdb5ed604df8ef5c20674e01dd"})
	})
	t.Run("ModelTraceStep", func(t *testing.T) {
		t.Parallel()
		goldenFixed(t, fixedOps[ModelTraceStep]{name: "MODELTRACESTEP", measure: ModelTraceStepFixedMeasure, save: ModelTraceStepFixedSave,
			saveWhy: ModelTraceStepFixedSaveReason, load: ModelTraceStepFixedLoad, planCount: int(ModelTraceStepFixedPlan.Count),
			body: ModelTraceStepFixedWriteBody, recBytes: ModelTraceStepFixedRecordBytes, reset: ModelTraceStepReset,
			wire: "349f4f0700902424d98c45f41027004833ca0fa55a41ea0b845b71b59ae731e6"})
	})
	t.Run("ModelTrace", func(t *testing.T) {
		t.Parallel()
		goldenFixed(t, fixedOps[ModelTrace]{name: "MODELTRACE", measure: ModelTraceFixedMeasure, save: ModelTraceFixedSave,
			saveWhy: ModelTraceFixedSaveReason, load: ModelTraceFixedLoad, planCount: int(ModelTraceFixedPlan.Count),
			body: ModelTraceFixedWriteBody, recBytes: ModelTraceFixedRecordBytes, reset: ModelTraceReset,
			wire: "41aaf0895d5c927090927a3af6548aa10e579e10a9ffcd360d67fa4f501f4ef0"})
	})
	t.Run("ModelFinding", func(t *testing.T) {
		t.Parallel()
		goldenFixed(t, fixedOps[ModelFinding]{name: "MODELFINDING", measure: ModelFindingFixedMeasure, save: ModelFindingFixedSave,
			saveWhy: ModelFindingFixedSaveReason, load: ModelFindingFixedLoad, planCount: int(ModelFindingFixedPlan.Count),
			body: ModelFindingFixedWriteBody, recBytes: ModelFindingFixedRecordBytes, reset: ModelFindingReset,
			wire: "dc2ff5f277590e6c112dd6db699fa8881d574b77f39f006bf3cff6bf3fbc578b"})
	})
	t.Run("ModelReplayEvent", func(t *testing.T) {
		t.Parallel()
		goldenFixed(t, fixedOps[ModelReplayEvent]{name: "MODELREPLAYEVENT", measure: ModelReplayEventFixedMeasure, save: ModelReplayEventFixedSave,
			saveWhy: ModelReplayEventFixedSaveReason, load: ModelReplayEventFixedLoad, planCount: int(ModelReplayEventFixedPlan.Count),
			body: ModelReplayEventFixedWriteBody, recBytes: ModelReplayEventFixedRecordBytes, reset: ModelReplayEventReset,
			wire: "f28a05b815bcd266f27bf5e365f04fb7a4dcc6d385a5479bfd7ffb1c3982289b"})
	})
	t.Run("ModelStep", func(t *testing.T) {
		t.Parallel()
		goldenFixed(t, fixedOps[ModelStep]{name: "MODELSTEP", measure: ModelStepFixedMeasure, save: ModelStepFixedSave,
			saveWhy: ModelStepFixedSaveReason, load: ModelStepFixedLoad, planCount: int(ModelStepFixedPlan.Count),
			body: ModelStepFixedWriteBody, recBytes: ModelStepFixedRecordBytes, reset: ModelStepReset,
			wire: "291e99eab78df14a1072d022558f26a1b2fd20a22075aa8d12e01bb2f54fe8d7"})
	})
	t.Run("ModelStepResult", func(t *testing.T) {
		t.Parallel()
		goldenFixed(t, fixedOps[ModelStepResult]{name: "MODELSTEPRESULT", measure: ModelStepResultFixedMeasure, save: ModelStepResultFixedSave,
			saveWhy: ModelStepResultFixedSaveReason, load: ModelStepResultFixedLoad, planCount: int(ModelStepResultFixedPlan.Count),
			body: ModelStepResultFixedWriteBody, recBytes: ModelStepResultFixedRecordBytes, reset: ModelStepResultReset,
			wire: "d40ca0e363bb7d0a15bbeb8a86e013567701d08de53007b8040d2ab6419f7c85"})
	})
	t.Run("ConvergeState", func(t *testing.T) {
		t.Parallel()
		goldenFixed(t, fixedOps[ConvergeState]{name: "CONVERGESTATE", measure: ConvergeStateFixedMeasure, save: ConvergeStateFixedSave,
			saveWhy: ConvergeStateFixedSaveReason, load: ConvergeStateFixedLoad, planCount: int(ConvergeStateFixedPlan.Count),
			body: ConvergeStateFixedWriteBody, recBytes: ConvergeStateFixedRecordBytes, reset: ConvergeStateReset,
			wire: "c98150b8c4a8082039492ae0e480b8677b188936facae0c9d1bf85ca002fc395"})
	})

	// The bounds are the code's: the fixture the model replays fits the action
	// table (at most 5 arguments of at most 512 bytes, a verb of 24 bytes, an
	// actor of 16) and the trace head's 40 steps.
	t.Run("FixtureFits", func(t *testing.T) {
		t.Parallel()
		actions := tablemodel.Actions()
		assert.LessOrEqual(t, len(actions), 40, "Actions() fits ModelTrace's 40 steps")
		var proto ModelAction
		verbMax, actorMax, argMax := len(proto.Verb), len(proto.Actor), len(proto.Args[0].Text)
		argsMax := len(proto.Args)
		for _, a := range actions {
			var v ModelAction
			require.LessOrEqual(t, len(a.Verb), verbMax, a.Verb)
			require.LessOrEqual(t, len(a.Actor), actorMax, a.Actor)
			require.LessOrEqual(t, len(a.Args), argsMax, a.Verb)
			copy(v.Verb[:], a.Verb)
			v.VerbLength = int32(len(a.Verb))
			copy(v.Actor[:], a.Actor)
			v.ActorLength = int32(len(a.Actor))
			for i, arg := range a.Args {
				require.LessOrEqual(t, len(arg), argMax, "%s argument %d", a.Verb, i)
				copy(v.Args[i].Text[:], arg)
				v.Args[i].TextLength = int32(len(arg))
			}
			v.ArgsCount = int32(len(a.Args))
			buf := make([]byte, ModelActionFixedMeasure(1))
			require.EqualValues(t, len(buf), ModelActionFixedSave([]ModelAction{v}, buf), a.Verb)
			got := make([]ModelAction, 1)
			var rep TableReport
			require.EqualValues(t, 1, ModelActionFixedLoad(got, buf, make([]TableFixedEntry, ModelActionFixedPlan.Count), &rep), a.Verb)
			assert.True(t, reflect.DeepEqual(v, got[0]), a.Verb)
			assert.Zero(t, rep.Clamped)
		}
	})
}
