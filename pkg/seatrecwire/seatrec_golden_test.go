package seatrecwire

// TestSeatAndTokensRecordsGolden is the golden test of the seatrec
// definitions (schemas/seatrec): the seat environment, the tokens day file,
// the tokens bus roster and the secrets place receipt round-trip as the
// binary wire and as padded standard base64 of that wire, each read strictly
// through pkg/schemaread (schemas/README.md; S1 and S2). A value past each
// declared bound is refused by name on write and on read, and a damaged
// image of each form is refused by name. Nothing is clamped. The sample of
// each table is filled from the generated struct itself, so the test and the
// definition cannot drift apart, and the SHA-256 of each sample's wire is
// pinned so a change of layout or encoding shows as a changed digest.
//
// The clock is the test clock: 1000.0 seconds, and the stamps the sample
// carries are that instant. No field and no fixture holds a secret value.

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testClock is the test clock, in seconds. It starts at 1000.0, never 0.
const testClock = 1000.0

// ranged names the scalar fields that declare a min and max in the schema
// (seatrec.schema). A max on the storage limit is a check the compiler
// elides, because no value of the type can stand past it; the refusal the
// test pins for those fields is the minimum.
var ranged = map[string][2]int64{
	"Version":    {1, 1},                 // SeatEnvironment: LoadSeatEnvironment accepts only 1
	"Input":      {0, math.MaxInt64},     // DayLine: ParseDayFile refuses a negative
	"Output":     {0, math.MaxInt64},     // DayLine
	"CacheWrite": {0, math.MaxInt64},     // DayLine
	"CacheRead":  {0, math.MaxInt64},     // DayLine
	"Reasoning":  {0, math.MaxInt64},     // DayLine
	"Rough":      {0, math.MaxInt64},     // DayLine: strconv.Atoi, then rough < 0 is refused
	"Turns":      {0, math.MaxInt64},     // DayFile: a present turns is a non-negative count
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
	field int
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
// two elements per array (one when the bound is one), 7 in a ranged scalar
// when 7 is inside the range and the range's minimum otherwise, true in a
// bool.
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
			n := min(3, fv.Len())
			for j := 0; j < n; j++ {
				fv.Index(j).SetUint(uint64(s[j]))
			}
			v.FieldByName(f.Name + "Length").SetInt(int64(n))
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
			n := int64(7)
			if b, ok := ranged[f.Name]; ok && (n < b[0] || n > b[1]) {
				n = b[0]
			}
			fv.SetInt(n)
		case f.Type.Kind() == reflect.Bool:
			fv.SetBool(true)
		case f.Type.Kind() == reflect.Float64:
			fv.SetFloat(1000.5)
		}
	}
}

// clockStamp is the test clock as an RFC3339 instant: 1000.0 seconds.
func clockStamp() string {
	return time.Unix(int64(testClock), 0).UTC().Format(time.RFC3339)
}

func setText(dst []byte, length *int32, s string) {
	for i := range dst {
		dst[i] = 0
	}
	copy(dst, s)
	*length = int32(len(s))
}

// fixedOps is the generated fixed-form surface of one table.
type fixedOps[T any] struct {
	name      string
	help      string
	measure   func(count int64) int64
	save      func([]T, []byte) int64
	saveWhy   func([]T, []byte) (int64, error)
	load      func([]T, []byte, []TableFixedEntry, *TableReport) int64
	planCount int
	body      func([]byte, *T)
	recBytes  int64
	reset     func(*T)
	after     func(*T)
	wire      string // the pinned SHA-256 of the sample's wire
}

// codec is the strict read and write over the generated fixed functions.
// Refuse every clamp, refuse every value past a declared bound before a byte
// is written (the generated writer checks them too and answers invalid_value),
// and read the one form byte of the fixed form.
func (o fixedOps[T]) codec(ks []knob) *schemaread.Codec[T] {
	return &schemaread.Codec[T]{
		Name:  o.name,
		Help:  o.help,
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
	if o.after != nil {
		o.after(&sample)
	}

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
			_, err := rd.Write(&bad, schemaread.FormBinary)
			require.Error(t, err, "%s %s=%d is refused on write", o.name, k.name, over)
			if k.isLen || k.array != nil {
				assert.Equal(t, schemaread.ReasonClamped, reasonOf(t, err), "%s %s", o.name, k.name)
			}
			_, err = o.saveWhy([]T{bad}, make([]byte, o.measure(1)))
			require.ErrorIs(t, err, TableRefuseInvalidValue, "%s %s=%d: the generated writer refuses", o.name, k.name, over)
		}
	}

	// A wire carrying a value past a bound is refused by name on read, never
	// clamped: the scalar's place on the wire is found by writing the sample
	// twice with the scalar at two legal values and diffing the images. A
	// bound with only one legal value is found by the encoding of that value.
	for _, k := range ks {
		at := wireOffset(t, o, sample, k, rd)
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

// overs are the values past a bound: one over the maximum, and one under the
// minimum. A maximum on the storage limit has no representable value past it.
func overs(k knob) []int64 {
	if k.array != nil {
		return []int64{k.max + 1, -1}
	}
	storage := int64(math.MaxInt32)
	if k.width == 8 {
		storage = math.MaxInt64
	}
	out := []int64{}
	if k.max < storage {
		out = append(out, k.max+1)
	}
	return append(out, k.min-1)
}

func put(b []byte, v int64) {
	if len(b) == 8 {
		binary.LittleEndian.PutUint64(b, uint64(v))
		return
	}
	binary.LittleEndian.PutUint32(b, uint32(int32(v)))
}

// wireOffset finds where k lives in the image. Two legal values diff to the
// first byte that moves. A bound with a single legal value is the slot whose
// encoding, patched one under the minimum, the reader refuses as clamped.
// The same bytes can sit in slack beside a bool, and that slot still loads.
func wireOffset[T any](t *testing.T, o fixedOps[T], sample T, k knob, rd *schemaread.Reader[T]) int {
	t.Helper()
	if k.array != nil || k.max >= k.min+2 {
		a, b := sample, sample
		k.at(reflect.ValueOf(&a).Elem()).SetInt(k.min + 1)
		k.at(reflect.ValueOf(&b).Elem()).SetInt(k.min + 2)
		ia, ib := image(t, o, a), image(t, o, b)
		for i := range ia {
			if ia[i] != ib[i] {
				return i
			}
		}
		require.Failf(t, "identical", "%s %s: the two images are identical", o.name, k.name)
		return 0
	}
	cur := k.at(reflect.ValueOf(&sample).Elem()).Int()
	img := image(t, o, sample)
	needle := make([]byte, k.width)
	put(needle, cur)
	var hits []int
	for i := 0; i+k.width <= len(img); i++ {
		if !bytes.Equal(img[i:i+k.width], needle) {
			continue
		}
		bad := append([]byte{}, img...)
		put(bad[i:i+k.width], k.min-1)
		_, _, err := rd.Read(bad)
		if err == nil || reasonOf(t, err) != schemaread.ReasonClamped {
			continue
		}
		hits = append(hits, i)
	}
	require.Len(t, hits, 1, "%s %s: want one wire slot that refuses %d", o.name, k.name, k.min-1)
	return hits[0]
}

func TestSeatAndTokensRecordsGolden(t *testing.T) {
	t.Parallel()

	t.Run("SeatEnvironment", func(t *testing.T) {
		t.Parallel()
		goldenFixed(t, fixedOps[SeatEnvironment]{name: "SEATENVIRONMENT", help: "nova-up -h",
			measure: SeatEnvironmentFixedMeasure, save: SeatEnvironmentFixedSave,
			saveWhy: SeatEnvironmentFixedSaveReason, load: SeatEnvironmentFixedLoad,
			planCount: int(SeatEnvironmentFixedPlan.Count), body: SeatEnvironmentFixedWriteBody,
			recBytes: SeatEnvironmentFixedRecordBytes, reset: SeatEnvironmentReset,
			wire: "b81dbc3d74547450d4a948b52d01c3e6facc5703e1ee9d89e3936f129cf530ae"})
	})
	t.Run("DayFile", func(t *testing.T) {
		t.Parallel()
		goldenFixed(t, fixedOps[DayFile]{name: "DAYFILE", help: "nova-tokens -h",
			measure: DayFileFixedMeasure, save: DayFileFixedSave,
			saveWhy: DayFileFixedSaveReason, load: DayFileFixedLoad,
			planCount: int(DayFileFixedPlan.Count), body: DayFileFixedWriteBody,
			recBytes: DayFileFixedRecordBytes, reset: DayFileReset,
			after: func(v *DayFile) { setText(v.At[:], &v.AtLength, clockStamp()) },
			wire: "f7fdb7511d930cad0bd9163364289a0cd0e063f781980f6d32e0f83679b97e5e"})
	})
	t.Run("BusRoster", func(t *testing.T) {
		t.Parallel()
		goldenFixed(t, fixedOps[BusRoster]{name: "BUSROSTER", help: "nova-tokens -h",
			measure: BusRosterFixedMeasure, save: BusRosterFixedSave,
			saveWhy: BusRosterFixedSaveReason, load: BusRosterFixedLoad,
			planCount: int(BusRosterFixedPlan.Count), body: BusRosterFixedWriteBody,
			recBytes: BusRosterFixedRecordBytes, reset: BusRosterReset,
			wire: "1e047c9448bdf0be8f14d4d30f54e9a9cb8f7c4e4639abb81627ea86c189b0dc"})
	})
	t.Run("PlacedReceipt", func(t *testing.T) {
		t.Parallel()
		goldenFixed(t, fixedOps[PlacedReceipt]{name: "PLACEDRECEIPT", help: "nova-secrets placed -h",
			measure: PlacedReceiptFixedMeasure, save: PlacedReceiptFixedSave,
			saveWhy: PlacedReceiptFixedSaveReason, load: PlacedReceiptFixedLoad,
			planCount: int(PlacedReceiptFixedPlan.Count), body: PlacedReceiptFixedWriteBody,
			recBytes: PlacedReceiptFixedRecordBytes, reset: PlacedReceiptReset,
			after: func(v *PlacedReceipt) { setText(v.Stamp[:], &v.StampLength, clockStamp()) },
			wire: "93bc3280d45ce055828cff6a3c778a0f5bd7970d332ca8435ac1ecdee26616b1"})
	})
}
