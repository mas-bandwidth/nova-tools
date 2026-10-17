package swarmwire

// TestSwarmRecordsGolden is the golden test of the swarm definitions
// (schemas/swarm): every table of the area (the exit record, the worker
// description, the harness config, the fence config, the declared model and
// the member carry) round-trips as the binary wire, as padded standard base64
// of that wire and as the JSON text form, each read strictly through
// pkg/schemaread (owner's rules S1 and S2); a value past each declared bound
// and a damaged image of each form are refused by name, never clamped.
//
// The tables are FIXED tables (S1): a small, bounded record whose reader is
// the same version or newer. The test walks each table's generated field
// descriptors, so a field added to the definition is covered the day it is
// generated and the bounds the test checks are the bounds the definition
// declares, never a copy of them.

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// table is one table's generated functions and what the strict read needs to
// refuse it by name. required names the fields a reader refuses when empty
// (S1: a fixed table has no absent field, so an empty value is the absence).
type table[T any] struct {
	name     string
	fields   []TableFieldInfo
	reset    func(*T)
	load     func(*T, []byte, *TableReport) bool
	fromJSON func(*T, []byte, *TableReport) bool
	measure  func(*T) int64
	save     func(*T, []byte) int64
	toMeas   func(*T) int64
	toJSON   func(*T, []byte) int64
	saveText func(*T) (string, error)
	loadText func(string, *TableReport) (*T, error)
	required []string
	// typical is a plausible value, set by the table's own function.
	typical func(*T)
}

// elementBounds are the text bounds of the element types the tables hold in
// counted arrays, read from their generated descriptors.
var elementBounds = map[string][]TableFieldInfo{}

// num reads and sets an integer member whatever its signedness.
func num(v reflect.Value) int64 {
	if v.CanInt() {
		return v.Int()
	}
	return int64(v.Uint())
}

func setNum(v reflect.Value, n int64) {
	if v.CanInt() {
		v.SetInt(n)
	} else {
		v.SetUint(uint64(n))
	}
}

func unsigned(v reflect.Value) bool { return !v.CanInt() }

// goName is the generated Go name of a schema field name.
func goName(s string) string {
	var b strings.Builder
	for _, p := range strings.Split(s, "_") {
		b.WriteString(strings.ToUpper(p[:1]) + p[1:])
	}
	return b.String()
}

func isString(f TableFieldInfo) bool   { return f.TypeName == "string" && !f.IsArray }
func isCounted(f TableFieldInfo) bool  { return f.IsArray && f.Counted }
func elemType(f TableFieldInfo) string { return f.TypeName }
func isInt(f TableFieldInfo) bool {
	return f.HasRange && (strings.HasPrefix(f.TypeName, "int") || strings.HasPrefix(f.TypeName, "uint"))
}

// setText fills a counted string member (a [N]byte and its Length) of the
// struct s, whose Go name is name; a length past the buffer is stored as the
// buffer's length (the generated storage cannot hold more).
func setText(s reflect.Value, name, text string) {
	buf := s.FieldByName(name)
	clear := reflect.Zero(buf.Type())
	buf.Set(clear)
	n := min(len(text), buf.Len())
	reflect.Copy(buf, reflect.ValueOf([]byte(text[:n])).Convert(reflect.SliceOf(buf.Type().Elem())))
	s.FieldByName(name + "Length").SetInt(int64(len(text)))
}

func getText(s reflect.Value, name string) string {
	buf := s.FieldByName(name)
	n := int(s.FieldByName(name + "Length").Int())
	if n < 0 {
		return ""
	}
	raw := make([]byte, buf.Len())
	for i := range raw {
		raw[i] = byte(buf.Index(i).Uint())
	}
	if n > len(raw) {
		return string(raw) + strings.Repeat("?", n-len(raw))
	}
	return string(raw[:n])
}

// codec is the strict read and write over the generated functions. The facade
// rules are UNION.md section 4.6: refuse every clamp, refuse absence where
// empty is no value, and validate before writing because the generated writer
// checks no bound.
func (tb table[T]) codec() *schemaread.Codec[T] {
	return &schemaread.Codec[T]{
		Name:  strings.ToUpper(tb.name),
		Help:  "nova-swarm -h",
		Reset: tb.reset,
		LoadWire: func(v *T, wire []byte) schemaread.Report {
			var rep TableReport
			ok := tb.load(v, wire, &rep)
			return report(rep, ok)
		},
		LoadJSON: func(v *T, text []byte) schemaread.Report {
			var rep TableReport
			ok := tb.fromJSON(v, text, &rep)
			return report(rep, ok)
		},
		Constraints: tb.constraints,
		SaveWire: func(v *T) []byte {
			n := tb.measure(v)
			if n < 0 {
				return nil
			}
			buf := make([]byte, n)
			if tb.save(v, buf) != n {
				return nil
			}
			return buf
		},
		SaveJSON: func(v *T) []byte {
			n := tb.toMeas(v)
			if n < 0 {
				return nil
			}
			buf := make([]byte, n)
			if tb.toJSON(v, buf) != n {
				return nil
			}
			return buf
		},
	}
}

func report(rep TableReport, ok bool) schemaread.Report {
	out := schemaread.Report{Unknown: int(rep.Unknown), KindMismatch: int(rep.KindMismatch), Clamped: int(rep.Clamped), Malformed: rep.Malformed || !ok}
	if rep.Verdict == TableOpenRefused || rep.Verdict == TableOpenDamaged {
		out.Reason = rep.Err().Error()
	}
	return out
}

// constraints states every bound of the table once, from its descriptors, for
// the reader and the writer (owner's rule S2: a reader refuses damage by name).
func (tb table[T]) constraints(v *T, c *schemaread.Checker) {
	s := reflect.ValueOf(v).Elem()
	required := map[string]bool{}
	for _, r := range tb.required {
		required[r] = true
	}
	for _, f := range tb.fields {
		g := goName(f.Name)
		switch {
		case isString(f):
			text := getText(s, g)
			c.Required(f.Name, !required[f.Name] || len(text) > 0)
			c.Text(f.Name, text, int(f.ArrayBound))
		case isCounted(f):
			n := int(s.FieldByName(g + "Count").Int())
			c.Required(f.Name, !required[f.Name] || n > 0)
			c.Bound(f.Name, int64(n), 0, int64(f.ArrayBound))
			for i := 0; i < min(n, int(f.ArrayBound)); i++ {
				el := s.FieldByName(g).Index(i)
				for _, ef := range elementBounds[elemType(f)] {
					eg := goName(ef.Name)
					if isString(ef) {
						c.Text(f.Name+"."+ef.Name, getText(el, eg), int(ef.ArrayBound))
					} else if isInt(ef) {
						n := num(el.FieldByName(eg))
						c.Required(f.Name+"."+ef.Name, n != 0)
						c.Bound(f.Name+"."+ef.Name, n, int64(ef.RangeMin), int64(ef.RangeMax))
					}
				}
				if elemType(f) != "FenceRule" {
					c.Required(f.Name, len(getText(el, "Text")) > 0)
				}
			}
		case isInt(f):
			n := num(s.FieldByName(g))
			c.Required(f.Name, !required[f.Name] || n != 0)
			c.Bound(f.Name, n, int64(f.RangeMin), int64(f.RangeMax))
		}
	}
}

// limit fills every field at its declared limit: text at its full bound,
// counted arrays full with full elements, integers at the range's top, enums
// at their last variant, flags set.
func (tb table[T]) limit() T {
	var v T
	tb.reset(&v)
	s := reflect.ValueOf(&v).Elem()
	for _, f := range tb.fields {
		fill(s, f, true)
	}
	return v
}

func fill(s reflect.Value, f TableFieldInfo, top bool) {
	g := goName(f.Name)
	switch {
	case isString(f):
		setText(s, g, strings.Repeat("a", int(f.ArrayBound)))
	case isCounted(f):
		n := int(f.ArrayBound)
		s.FieldByName(g + "Count").SetInt(int64(n))
		for i := range n {
			el := s.FieldByName(g).Index(i)
			for _, ef := range elementBounds[elemType(f)] {
				fill(el, ef, top)
			}
		}
	case isInt(f):
		setNum(s.FieldByName(g), int64(f.RangeMax))
	case f.TypeName == "bool":
		s.FieldByName(g).SetBool(true)
	}
}

// refusal checks the house style of a refusal and returns its reason.
func refusal(t *testing.T, name string, err error) string {
	t.Helper()
	require.Error(t, err)
	var r *schemaread.Refusal
	require.True(t, errors.As(err, &r), "every refusal is a *schemaread.Refusal: %v", err)
	assert.True(t, strings.HasPrefix(err.Error(), strings.ToUpper(name)+" REFUSED: "), err.Error())
	assert.True(t, strings.HasSuffix(err.Error(), "; run: nova-swarm -h"), err.Error())
	if r.Field != "" {
		return r.Reason + " (" + r.Field + ")"
	}
	return r.Reason
}

var b64 = base64.StdEncoding.EncodeToString

// run is the whole proof of one table.
func run[T any](t *testing.T, tb table[T]) {
	t.Helper()
	r := schemaread.NewReader(tb.codec())
	var typical T
	tb.reset(&typical)
	tb.typical(&typical)
	limit := tb.limit()

	// every form round-trips, for the typical value and the value at every limit
	for name, v := range map[string]T{"typical": typical, "limit": limit} {
		wire, err := r.Write(&v, schemaread.FormBinary)
		require.NoError(t, err, "%s %s writes", tb.name, name)
		require.NotEmpty(t, wire)
		text, err := r.Write(&v, schemaread.FormBase64)
		require.NoError(t, err)
		assert.Equal(t, b64(wire), string(text), "%s %s: the text is the padded standard base64 of the whole wire", tb.name, name)
		js, err := r.Write(&v, schemaread.FormJSON)
		require.NoError(t, err)
		for _, f := range tb.fields {
			assert.Contains(t, string(js), `"`+f.Json+`": `, "%s %s: the text form is keyed by field name", tb.name, name)
		}
		for _, in := range []struct {
			form schemaread.Form
			data []byte
		}{{schemaread.FormBinary, wire}, {schemaread.FormBase64, text}, {schemaread.FormJSON, js}} {
			got, res, err := r.Read(in.data)
			require.NoError(t, err, "%s %s as %s", tb.name, name, in.form)
			assert.Equal(t, in.form, res.Form)
			assert.Equal(t, v, got, "%s %s as %s reads the value", tb.name, name, in.form)
			again, err := r.Write(&got, in.form)
			require.NoError(t, err)
			assert.True(t, bytes.Equal(again, in.data) || in.form == schemaread.FormJSON, "%s %s: a read value writes the same %s", tb.name, name, in.form)
		}
		// the generated SaveText and LoadText are the same text transport
		sv := v
		gen, err := tb.saveText(&sv)
		require.NoError(t, err)
		assert.Equal(t, b64(wire), gen, "%s %s: SaveText is the padded standard base64 of the wire", tb.name, name)
		back, err := tb.loadText(gen, nil)
		require.NoError(t, err)
		assert.Equal(t, v, *back, "%s %s: LoadText reads what SaveText wrote", tb.name, name)
	}
	c := r.Counts()
	assert.Equal(t, uint64(2), c.Reads(schemaread.FormBinary), "%s: each read is counted once, on its form", tb.name)
	assert.Equal(t, uint64(2), c.Reads(schemaread.FormBase64))
	assert.Equal(t, uint64(2), c.Reads(schemaread.FormJSON))
	assert.Equal(t, uint64(0), c.Reads(schemaread.FormLegacy))

	// a value past each declared bound is refused by name by the writer,
	// before a byte is written
	overCases := 0
	for _, f := range tb.fields {
		g := goName(f.Name)
		over := func(edit func(s reflect.Value)) string {
			v := tb.limit()
			edit(reflect.ValueOf(&v).Elem())
			out, err := r.Write(&v, schemaread.FormBinary)
			assert.Nil(t, out, "%s %s past its bound writes nothing", tb.name, f.Name)
			overCases++
			return refusal(t, tb.name, err)
		}
		switch {
		case isString(f):
			assert.Equal(t, "clamped ("+f.Name+")", over(func(s reflect.Value) { setText(s, g, strings.Repeat("a", int(f.ArrayBound)+1)) }), "%s.%s one byte over", tb.name, f.Name)
			assert.Equal(t, "malformed ("+f.Name+")", over(func(s reflect.Value) { setText(s, g, "a\x00") }), "%s.%s with a NUL", tb.name, f.Name)
		case isCounted(f):
			assert.Equal(t, "clamped ("+f.Name+")", over(func(s reflect.Value) { s.FieldByName(g + "Count").SetInt(int64(f.ArrayBound) + 1) }), "%s.%s one entry over", tb.name, f.Name)
		case isInt(f):
			assert.Equal(t, "clamped ("+f.Name+")", over(func(s reflect.Value) { setNum(s.FieldByName(g), int64(f.RangeMax)+1) }), "%s.%s one over its max", tb.name, f.Name)
			if f.RangeMin < 0 || !unsigned(reflect.ValueOf(&limit).Elem().FieldByName(g)) {
				assert.Equal(t, "clamped ("+f.Name+")", over(func(s reflect.Value) { setNum(s.FieldByName(g), int64(f.RangeMin)-1) }), "%s.%s one under its min", tb.name, f.Name)
			}
		}
	}
	assert.GreaterOrEqual(t, overCases, len(tb.fields), "%s: every bounded field is probed past its bound", tb.name)
	// an empty required field is refused by name, by the writer and the reader
	for _, name := range tb.required {
		var f TableFieldInfo
		for _, x := range tb.fields {
			if x.Name == name {
				f = x
			}
		}
		v := typical
		s := reflect.ValueOf(&v).Elem()
		switch {
		case isString(f):
			setText(s, goName(name), "")
		case isInt(f):
			setNum(s.FieldByName(goName(name)), 0)
		case isCounted(f):
			s.FieldByName(goName(name) + "Count").SetInt(0)
		}
		out, err := r.Write(&v, schemaread.FormBinary)
		assert.Nil(t, out)
		assert.Equal(t, "absent_required ("+name+")", refusal(t, tb.name, err), "%s.%s empty", tb.name, name)
		// the same value, written without the check, is refused on the way in
		raw := tb.codec().SaveWire(&v)
		require.NotNil(t, raw)
		_, _, err = r.Read(raw)
		assert.Equal(t, "absent_required ("+name+")", refusal(t, tb.name, err))
	}

	// a value past a bound in the JSON text form is refused on the way in
	// (the generated reader clamps and counts; the strict read refuses)
	jsonCases := 0
	base := limit
	js, err := r.Write(&base, schemaread.FormJSON)
	require.NoError(t, err)
	for _, f := range tb.fields {
		var edited string
		switch {
		case isString(f):
			edited = strings.Replace(string(js), `"`+f.Json+`": "`+strings.Repeat("a", int(f.ArrayBound))+`"`, `"`+f.Json+`": "`+strings.Repeat("a", int(f.ArrayBound)+1)+`"`, 1)
		case isInt(f):
			edited = strings.Replace(string(js), fmt.Sprintf(`"%s": %d`, f.Json, int64(f.RangeMax)), fmt.Sprintf(`"%s": %d`, f.Json, int64(f.RangeMax)+1), 1)
		default:
			continue
		}
		if edited == string(js) {
			continue
		}
		jsonCases++
		got, _, err := r.Read([]byte(edited))
		assert.Equal(t, "clamped", refusal(t, tb.name, err), "%s.%s over its bound as JSON text", tb.name, f.Name)
		var zero T
		assert.Equal(t, zero, got, "a refused read returns nothing, never a partial value")
	}

	assert.Positive(t, jsonCases, "%s: some field is probed past its bound as JSON text", tb.name)

	// a damaged image of each form is refused by name, never read as a value
	wire, err := r.Write(&typical, schemaread.FormBinary)
	require.NoError(t, err)
	newer := append([]byte{}, wire...)
	newer[0] = 9
	text := b64(wire)
	for _, tc := range []struct {
		name   string
		in     []byte
		reason string
	}{
		{"trailing junk in the wire", append(append([]byte{}, wire...), 0, 1), "wire_damaged"},
		{"the form byte alone", wire[:1], "wire_damaged"},
		{"an unknown newer form", newer, "unknown_form"},
		{"base64 that does not decode", []byte(text[:len(text)-3]), "malformed"},
		{"base64 with trailing junk", []byte(text + " junk"), "malformed"},
		{"damaged JSON text", []byte(strings.Replace(string(mustWrite(t, r, &typical, schemaread.FormJSON)), `": `, `" `, 1)), "malformed"},
		{"empty", nil, "unknown_form"},
		{"no record at all", []byte("hello swarm"), "malformed"},
	} {
		_, _, err := r.Read(tc.in)
		assert.Equal(t, tc.reason, refusal(t, tb.name, err), "%s: %s", tb.name, tc.name)
	}
	// every truncation of the wire is refused, never read as a shorter record
	for i := range len(wire) {
		_, _, err := r.Read(wire[:i])
		assert.Error(t, err, "%s cut at byte %d", tb.name, i)
	}

	// a deterministic mutation pass over the wire and the base64 text: every
	// mutant either is refused by name or reads a value that passes every
	// constraint. Nothing panics and nothing reads out of bound.
	mutants := 0
	for _, in := range [][]byte{wire, []byte(text)} {
		for i := range in {
			for _, b := range []byte{0x00, in[i] ^ 0x01, in[i] ^ 0x80} {
				m := append([]byte{}, in...)
				m[i] = b
				mutants++
				got, _, err := r.Read(m)
				if err != nil {
					var ref *schemaread.Refusal
					require.True(t, errors.As(err, &ref), "%s mutant %d: %v", tb.name, mutants, err)
					continue
				}
				require.NoError(t, r.Check(&got), "%s: a mutant that reads passes every constraint", tb.name)
			}
		}
	}
	assert.Greater(t, mutants, 100)
}

func mustWrite[T any](t *testing.T, r *schemaread.Reader[T], v *T, form schemaread.Form) []byte {
	t.Helper()
	out, err := r.Write(v, form)
	require.NoError(t, err)
	return out
}

func le32(v int32) []byte {
	u := uint32(v)
	return []byte{byte(u), byte(u >> 8), byte(u >> 16), byte(u >> 24)}
}

// hostile is the wire of a value the generated writer refuses to write (it
// checks every bound): the value is written with a marker in bounds and the
// marker bytes are replaced in the wire, exactly what a broken or older
// writer could put on the wire. The marker must occur exactly once.
func hostile(t *testing.T, wire, marker, with []byte) []byte {
	t.Helper()
	require.NotNil(t, wire)
	require.Equal(t, 1, bytes.Count(wire, marker), "the marker %x occurs once", marker)
	return bytes.Replace(wire, marker, with, 1)
}

func TestSwarmRecordsGolden(t *testing.T) {
	t.Parallel()

	// A value a writer could put on the wire past its bound is refused by
	// name on the way in, as the binary wire and as its base64 text, never
	// clamped to the bound.
	t.Run("a wire past a bound is refused, never clamped", func(t *testing.T) {
		t.Parallel()
		r := schemaread.NewReader(table[ExitRecord]{
			name: "ExitRecord", fields: ExitRecordTableFields, reset: ExitRecordReset, load: ExitRecordLoad,
			fromJSON: ExitRecordFromJson, measure: ExitRecordMeasure, save: ExitRecordSave,
			toMeas: ExitRecordToJsonMeasure, toJSON: ExitRecordToJson, required: []string{"ended", "nonce"},
		}.codec())
		var v ExitRecord
		ExitRecordReset(&v)
		setText(reflect.ValueOf(&v).Elem(), "Ended", "2026-10-17T04:00:00Z")
		setText(reflect.ValueOf(&v).Elem(), "Nonce", "0123456789ab")
		v.Rc, v.Survivors, v.Spent = 255, 1048576, 77
		good, err := r.Write(&v, schemaread.FormBinary)
		require.NoError(t, err)
		for _, tc := range []struct {
			name         string
			marker, with []byte
		}{
			{"rc over 255", le32(255), le32(256)},
			{"survivors over 1048576", le32(1048576), le32(1048577)},
		} {
			bad := hostile(t, good, tc.marker, tc.with)
			for form, in := range map[string][]byte{"binary": bad, "base64": []byte(b64(bad))} {
				_, _, err := r.Read(in)
				assert.Equal(t, "clamped", refusal(t, "ExitRecord", err), "%s as %s", tc.name, form)
			}
		}
		v.Rc = -1
		low, err := r.Write(&v, schemaread.FormBinary)
		require.NoError(t, err, "rc -1 (no exit) is in bounds")
		_, _, err = r.Read(hostile(t, low, le32(-1), le32(-2)))
		assert.Equal(t, "clamped", refusal(t, "ExitRecord", err), "rc under -1")
	})

	t.Run("the documents (variable tables)", documentsGolden)
	t.Run("the exit record", func(t *testing.T) {
		t.Parallel()
		run(t, table[ExitRecord]{
			name: "ExitRecord", fields: ExitRecordTableFields, reset: ExitRecordReset, load: ExitRecordLoad,
			fromJSON: ExitRecordFromJson, measure: ExitRecordMeasure, save: ExitRecordSave,
			toMeas: ExitRecordToJsonMeasure, toJSON: ExitRecordToJson, saveText: ExitRecordSaveText, loadText: ExitRecordLoadText,
			required: []string{"ended", "nonce"},
			typical: func(v *ExitRecord) {
				v.Rc = 2
				setText(reflect.ValueOf(v).Elem(), "Ended", "2026-10-17T04:00:00Z")
				setText(reflect.ValueOf(v).Elem(), "Nonce", "0123456789ab")
				setText(reflect.ValueOf(v).Elem(), "Reason", "deadline")
				v.Survivors = 1
			},
		})
	})
	t.Run("the harness config", func(t *testing.T) {
		t.Parallel()
		run(t, table[HarnessConfigRecord]{
			name: "HarnessConfigRecord", fields: HarnessConfigRecordTableFields, reset: HarnessConfigRecordReset, load: HarnessConfigRecordLoad,
			fromJSON: HarnessConfigRecordFromJson, measure: HarnessConfigRecordMeasure, save: HarnessConfigRecordSave,
			toMeas: HarnessConfigRecordToJsonMeasure, toJSON: HarnessConfigRecordToJson, saveText: HarnessConfigRecordSaveText, loadText: HarnessConfigRecordLoadText,
			required: []string{"provider", "model", "env_var"},
			typical: func(v *HarnessConfigRecord) {
				s := reflect.ValueOf(v).Elem()
				setText(s, "Provider", "deepseek")
				setText(s, "Model", "deepseek-chat")
				setText(s, "EnvVar", "DEEPSEEK_API_KEY")
			},
		})
	})
	t.Run("the declared model", func(t *testing.T) {
		t.Parallel()
		run(t, table[DeclaredModel]{
			name: "DeclaredModel", fields: DeclaredModelTableFields, reset: DeclaredModelReset, load: DeclaredModelLoad,
			fromJSON: DeclaredModelFromJson, measure: DeclaredModelMeasure, save: DeclaredModelSave,
			toMeas: DeclaredModelToJsonMeasure, toJSON: DeclaredModelToJson, saveText: DeclaredModelSaveText, loadText: DeclaredModelLoadText,
			required: []string{"provider", "model"},
			typical: func(v *DeclaredModel) {
				setText(reflect.ValueOf(v).Elem(), "Provider", "deepseek")
				setText(reflect.ValueOf(v).Elem(), "Model", "deepseek-chat")
			},
		})
	})
	t.Run("the member carry", func(t *testing.T) {
		t.Parallel()
		run(t, table[MemberCarry]{
			name: "MemberCarry", fields: MemberCarryTableFields, reset: MemberCarryReset, load: MemberCarryLoad,
			fromJSON: MemberCarryFromJson, measure: MemberCarryMeasure, save: MemberCarrySave,
			toMeas: MemberCarryToJsonMeasure, toJSON: MemberCarryToJson, saveText: MemberCarrySaveText, loadText: MemberCarryLoadText,
			typical: func(v *MemberCarry) {
				s := reflect.ValueOf(v).Elem()
				setText(s, "Card", "recut-widen-r.w1")
				v.Attempt = 2
				setText(s, "Head", strings.Repeat("ab12", 10))
			},
		})
	})
}

// ---- variable tables: WorkerRecord and FenceConfig are documents --------------
//
// A variable table is sized by its content: its lists are unbounded and its
// text carries no bound the code does not enforce, so the proof is that long
// lists and long text round-trip, that an integer past its range is refused by
// name, and that damaged images of each form are refused by name. The
// generated variable Go is region based, so the test works on a plain Go value
// of each document: a plain value is written through the generated builder (by
// the schema JSON text, the generated reader of lists) and read back out of
// the generated region.

type workerDoc struct {
	Name, Provider, Model, BaseURL, EnvVar, KeyFile, Secret string
	Usage, WorkerClass                                      uint8
	Harness                                                 string
	HarnessArgs                                             []string
	WorkerDir, Deadline, Board                              string
	ReadRoots                                               []string
	MaxTurns                                                int32
	MaxCacheRead                                            int64
	InputLimitPhrases                                       []string
	LaunchGrace                                             string
	KeepData                                                bool
}

type ruleDoc struct {
	Pattern string
	Action  uint8
}

type fenceDoc struct {
	JobDir   string
	Reads    []string
	Rules    []ruleDoc
	Webfetch uint8
}

func entries(v []string) []any {
	out := make([]any, 0, len(v))
	for _, s := range v {
		out = append(out, map[string]any{"value": s})
	}
	return out
}

// jsonOf is the schema's name-keyed text of a document; an empty text is
// absent, as a pointer field is.
func jsonOf(m map[string]any) []byte {
	for k, v := range m {
		switch x := v.(type) {
		case string:
			if x == "" {
				delete(m, k)
			}
		case []any:
			if len(x) == 0 {
				delete(m, k)
			}
		}
	}
	out, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		panic(err)
	}
	return out
}

func (d workerDoc) json() []byte {
	return jsonOf(map[string]any{"name": d.Name, "provider": d.Provider, "model": d.Model, "base_url": d.BaseURL,
		"env_var": d.EnvVar, "key_file": d.KeyFile, "secret": d.Secret, "usage": d.Usage, "worker_class": d.WorkerClass,
		"harness": d.Harness, "harness_args": entries(d.HarnessArgs), "worker_dir": d.WorkerDir, "deadline": d.Deadline,
		"board": d.Board, "read_roots": entries(d.ReadRoots), "max_turns": d.MaxTurns, "max_cache_read": d.MaxCacheRead,
		"input_limit_phrases": entries(d.InputLimitPhrases), "launch_grace": d.LaunchGrace, "keep_data": d.KeepData})
}

func (d fenceDoc) json() []byte {
	rules := make([]any, 0, len(d.Rules))
	for _, r := range d.Rules {
		rules = append(rules, jsonOf(map[string]any{"pattern": r.Pattern, "action": r.Action}))
	}
	// rules are nested objects, not raw bytes
	nested := make([]any, 0, len(d.Rules))
	for _, r := range d.Rules {
		m := map[string]any{"action": r.Action}
		if r.Pattern != "" {
			m["pattern"] = r.Pattern
		}
		nested = append(nested, m)
	}
	_ = rules
	return jsonOf(map[string]any{"job_dir": d.JobDir, "reads": entries(d.Reads), "rules": nested, "webfetch": d.Webfetch})
}

func text(ref *TableRef) string {
	v := TableStringAt(ref)
	return string(v.Data[:v.Length])
}

func list(l *TableList[EntryRow]) []string {
	var out []string
	for i := int32(0); i < l.Count; i++ {
		out = append(out, text(&l.At(i).Value))
	}
	return out
}

func workerOf(w *WorkerRecord) workerDoc {
	return workerDoc{text(&w.Name), text(&w.Provider), text(&w.Model), text(&w.BaseUrl), text(&w.EnvVar), text(&w.KeyFile), text(&w.Secret),
		w.Usage, w.WorkerClass, text(&w.Harness), list(&w.HarnessArgs), text(&w.WorkerDir), text(&w.Deadline), text(&w.Board),
		list(&w.ReadRoots), w.MaxTurns, w.MaxCacheRead, list(&w.InputLimitPhrases), text(&w.LaunchGrace), w.KeepData}
}

func fenceOf(f *FenceConfig) fenceDoc {
	d := fenceDoc{JobDir: text(&f.JobDir), Reads: list(&f.Reads), Webfetch: f.Webfetch}
	for i := int32(0); i < f.Rules.Count; i++ {
		r := f.Rules.At(i)
		d.Rules = append(d.Rules, ruleDoc{text(&r.Pattern), r.Action})
	}
	return d
}

// docTable is one variable table's generated functions, on the plain value P.
type docTable[P any] struct {
	name     string
	json     func(P) []byte
	fromJSON func(text []byte) (P, TableReport, bool) // the generated reader of the JSON text
	fromWire func(wire []byte) (P, TableReport, bool) // the generated region load
	toWire   func(P) []byte                           // the generated writer, via the builder
	toJSON   func(P) []byte                           // the generated ToJson of a built root
	check    func(P, *schemaread.Checker)
}

func (tb docTable[P]) codec() *schemaread.Codec[P] {
	rep := func(r TableReport, ok bool) schemaread.Report {
		out := schemaread.Report{Unknown: int(r.Unknown), KindMismatch: int(r.KindMismatch), Clamped: int(r.Clamped), Malformed: r.Malformed || !ok || r.Duplicate > 0}
		if r.Verdict == TableOpenRefused || r.Verdict == TableOpenDamaged {
			out.Reason = r.Err().Error()
		}
		return out
	}
	return &schemaread.Codec[P]{
		Name: strings.ToUpper(tb.name), Help: "nova-swarm -h",
		LoadWire: func(v *P, wire []byte) schemaread.Report {
			p, r, ok := tb.fromWire(wire)
			*v = p
			return rep(r, ok)
		},
		LoadJSON: func(v *P, text []byte) schemaread.Report {
			p, r, ok := tb.fromJSON(text)
			*v = p
			return rep(r, ok)
		},
		Constraints: func(v *P, c *schemaread.Checker) { tb.check(*v, c) },
		SaveWire:    func(v *P) []byte { return tb.toWire(*v) },
		SaveJSON:    func(v *P) []byte { return tb.toJSON(*v) },
	}
}

func region(n int64) []byte {
	raw := make([]byte, n+63)
	off := (-uintptr(unsafe.Pointer(&raw[0]))) & 63
	return raw[off : off+uintptr(n)]
}

func workerTable() docTable[workerDoc] {
	build := func(d workerDoc) (*WorkerRecord, func()) {
		var b WorkerRecordBuilder
		if !b.Init() {
			return nil, func() {}
		}
		var r TableReport
		if !WorkerRecordFromJson(&b, d.json(), &r) || !b.Lock() {
			return nil, b.Shutdown
		}
		return b.AsConst(), b.Shutdown
	}
	return docTable[workerDoc]{
		name: "WorkerRecord",
		json: workerDoc.json,
		fromJSON: func(text []byte) (workerDoc, TableReport, bool) {
			var b WorkerRecordBuilder
			if !b.Init() {
				return workerDoc{}, TableReport{}, false
			}
			defer b.Shutdown()
			var r TableReport
			ok := WorkerRecordFromJson(&b, text, &r) && b.Lock()
			if !ok {
				return workerDoc{}, r, false
			}
			return workerOf(b.AsConst()), r, true
		},
		fromWire: func(wire []byte) (workerDoc, TableReport, bool) {
			n := WorkerRecordLoadMeasure(wire)
			if n < 0 {
				return workerDoc{}, TableReport{Malformed: true}, false
			}
			var r TableReport
			w := WorkerRecordLoad(region(n), wire, &r)
			if w == nil {
				return workerDoc{}, r, false
			}
			return workerOf(w), r, true
		},
		toWire: func(d workerDoc) []byte {
			w, done := build(d)
			defer done()
			if w == nil {
				return nil
			}
			buf := make([]byte, WorkerRecordMeasure(w))
			if WorkerRecordSave(w, buf) != int64(len(buf)) {
				return nil
			}
			return buf
		},
		toJSON: func(d workerDoc) []byte {
			w, done := build(d)
			defer done()
			if w == nil {
				return nil
			}
			buf := make([]byte, WorkerRecordToJsonMeasure(w))
			if WorkerRecordToJson(w, buf) != int64(len(buf)) {
				return nil
			}
			return buf
		},
		check: func(d workerDoc, c *schemaread.Checker) {
			for _, f := range []struct {
				name, v  string
				required bool
			}{{"name", d.Name, true}, {"provider", d.Provider, true}, {"model", d.Model, true}, {"base_url", d.BaseURL, false},
				{"env_var", d.EnvVar, true}, {"key_file", d.KeyFile, false}, {"secret", d.Secret, false}, {"harness", d.Harness, true},
				{"worker_dir", d.WorkerDir, true}, {"deadline", d.Deadline, true}, {"board", d.Board, false}, {"launch_grace", d.LaunchGrace, false}} {
				c.Required(f.name, !f.required || f.v != "")
				c.Text(f.name, f.v, math.MaxInt32)
			}
			c.Required("usage", d.Usage != 0)
			c.Bound("usage", int64(d.Usage), 0, 2)
			c.Bound("worker_class", int64(d.WorkerClass), 0, 2)
			c.Required("harness_args", len(d.HarnessArgs) > 0)
			for _, l := range [][]string{d.HarnessArgs, d.ReadRoots, d.InputLimitPhrases} {
				for _, s := range l {
					c.Required("entry", s != "")
					c.Text("entry", s, math.MaxInt32)
				}
			}
			c.Bound("max_turns", int64(d.MaxTurns), 0, math.MaxInt32)
			c.Bound("max_cache_read", d.MaxCacheRead, 0, 1<<53-1)
		},
	}
}

func fenceTable() docTable[fenceDoc] {
	build := func(d fenceDoc) (*FenceConfig, func()) {
		var b FenceConfigBuilder
		if !b.Init() {
			return nil, func() {}
		}
		var r TableReport
		if !FenceConfigFromJson(&b, d.json(), &r) || !b.Lock() {
			return nil, b.Shutdown
		}
		return b.AsConst(), b.Shutdown
	}
	return docTable[fenceDoc]{
		name: "FenceConfig",
		json: fenceDoc.json,
		fromJSON: func(text []byte) (fenceDoc, TableReport, bool) {
			var b FenceConfigBuilder
			if !b.Init() {
				return fenceDoc{}, TableReport{}, false
			}
			defer b.Shutdown()
			var r TableReport
			if !FenceConfigFromJson(&b, text, &r) || !b.Lock() {
				return fenceDoc{}, r, false
			}
			return fenceOf(b.AsConst()), r, true
		},
		fromWire: func(wire []byte) (fenceDoc, TableReport, bool) {
			n := FenceConfigLoadMeasure(wire)
			if n < 0 {
				return fenceDoc{}, TableReport{Malformed: true}, false
			}
			var r TableReport
			f := FenceConfigLoad(region(n), wire, &r)
			if f == nil {
				return fenceDoc{}, r, false
			}
			return fenceOf(f), r, true
		},
		toWire: func(d fenceDoc) []byte {
			f, done := build(d)
			defer done()
			if f == nil {
				return nil
			}
			buf := make([]byte, FenceConfigMeasure(f))
			if FenceConfigSave(f, buf) != int64(len(buf)) {
				return nil
			}
			return buf
		},
		toJSON: func(d fenceDoc) []byte {
			f, done := build(d)
			defer done()
			if f == nil {
				return nil
			}
			buf := make([]byte, FenceConfigToJsonMeasure(f))
			if FenceConfigToJson(f, buf) != int64(len(buf)) {
				return nil
			}
			return buf
		},
		check: func(d fenceDoc, c *schemaread.Checker) {
			c.Required("job_dir", d.JobDir != "")
			c.Text("job_dir", d.JobDir, math.MaxInt32)
			for _, s := range d.Reads {
				c.Required("reads", s != "")
				c.Text("reads", s, math.MaxInt32)
			}
			for _, r := range d.Rules {
				c.Required("rules", r.Pattern != "" && r.Action != 0)
				c.Text("rules", r.Pattern, math.MaxInt32)
				c.Bound("rules", int64(r.Action), 0, 2)
			}
			c.Required("webfetch", d.Webfetch != 0)
			c.Bound("webfetch", int64(d.Webfetch), 0, 2)
		},
	}
}

// runDoc is the whole proof of one variable table over its documents.
func runDoc[P any](t *testing.T, tb docTable[P], docs map[string]P, over map[string]P, patch map[string][2]P) {
	t.Helper()
	r := schemaread.NewReader(tb.codec())
	n := 0
	for name, d := range docs {
		wire, err := r.Write(&d, schemaread.FormBinary)
		require.NoError(t, err, "%s %s writes", tb.name, name)
		text64, err := r.Write(&d, schemaread.FormBase64)
		require.NoError(t, err)
		assert.Equal(t, b64(wire), string(text64), "%s %s: the text is the padded standard base64 of the whole wire", tb.name, name)
		js, err := r.Write(&d, schemaread.FormJSON)
		require.NoError(t, err)
		for _, in := range []struct {
			form schemaread.Form
			data []byte
		}{{schemaread.FormBinary, wire}, {schemaread.FormBase64, text64}, {schemaread.FormJSON, js}} {
			got, res, err := r.Read(in.data)
			require.NoError(t, err, "%s %s as %s", tb.name, name, in.form)
			assert.Equal(t, in.form, res.Form)
			assert.Equal(t, d, got, "%s %s as %s reads the document", tb.name, name, in.form)
		}
		n++
	}
	c := r.Counts()
	assert.Equal(t, uint64(n), c.Reads(schemaread.FormBinary), "%s: each read is counted once, on its form", tb.name)
	assert.Equal(t, uint64(n), c.Reads(schemaread.FormBase64))
	assert.Equal(t, uint64(n), c.Reads(schemaread.FormJSON))

	// a value past its bound (or an empty required field) is refused by name before a byte is written
	for name, d := range over {
		out, err := r.Write(&d, schemaread.FormBinary)
		assert.Nil(t, out, "%s %s writes nothing", tb.name, name)
		assert.Contains(t, []string{"clamped", "absent_required", "malformed"}, strings.Fields(refusal(t, tb.name, err))[0], "%s %s", tb.name, name)
	}

	// a wire past a bound is refused, never clamped: the one byte that differs between a
	// document at the bound and one a step inside it is moved past the bound
	for name, pair := range patch {
		inside, err := r.Write(&pair[0], schemaread.FormBinary)
		require.NoError(t, err)
		atBound, err := r.Write(&pair[1], schemaread.FormBinary)
		require.NoError(t, err)
		require.Equal(t, len(inside), len(atBound))
		at := -1
		for i := range inside {
			if inside[i] != atBound[i] {
				require.Equal(t, -1, at, "%s %s differ at one byte", tb.name, name)
				at = i
			}
		}
		require.GreaterOrEqual(t, at, 0)
		bad := append([]byte{}, atBound...)
		bad[at]++
		for form, in := range map[string][]byte{"binary": bad, "base64": []byte(b64(bad))} {
			_, _, err := r.Read(in)
			assert.Equal(t, "clamped", refusal(t, tb.name, err), "%s %s as %s", tb.name, name, form)
		}
	}

	// damaged images, by name
	// the typical document, never the long one: the mutation pass reads every
	// byte of the image
	d, ok := docs["typical"]
	require.True(t, ok, "%s: the documents name a typical one", tb.name)
	wire, err := r.Write(&d, schemaread.FormBinary)
	require.NoError(t, err)
	newer := append([]byte{}, wire...)
	newer[0] = 9
	text := b64(wire)
	js := mustWrite(t, r, &d, schemaread.FormJSON)
	for _, tc := range []struct {
		name   string
		in     []byte
		reason string
	}{
		{"an unknown newer form", newer, "unknown_form"},
		{"base64 that does not decode", []byte(text[:len(text)-3]), "malformed"},
		{"base64 with trailing junk", []byte(text + " junk"), "malformed"},
		{"damaged JSON text", []byte(strings.Replace(string(js), `": `, `" `, 1)), "malformed"},
		{"empty", nil, "unknown_form"},
		{"no record at all", []byte("hello swarm"), "malformed"},
	} {
		_, _, err := r.Read(tc.in)
		assert.Equal(t, tc.reason, refusal(t, tb.name, err), "%s: %s", tb.name, tc.name)
	}
	for i := range len(wire) {
		_, _, err := r.Read(wire[:i])
		assert.Error(t, err, "%s cut at byte %d", tb.name, i)
	}
	mutants := 0
	for _, in := range [][]byte{wire, []byte(text)} {
		for i := range in {
			for _, b := range []byte{0x00, in[i] ^ 0x01, in[i] ^ 0x80} {
				m := append([]byte{}, in...)
				m[i] = b
				mutants++
				got, _, err := r.Read(m)
				if err != nil {
					var ref *schemaread.Refusal
					require.True(t, errors.As(err, &ref), "%s mutant %d: %v", tb.name, mutants, err)
					continue
				}
				require.NoError(t, r.Check(&got), "%s: a mutant that reads passes every constraint", tb.name)
			}
		}
	}
	assert.Greater(t, mutants, 100)
}

func many(n int, f func(i int) string) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = f(i)
	}
	return out
}

// documentsGolden is the variable tables' proof, run inside TestSwarmRecordsGolden.
func documentsGolden(t *testing.T) {
	typicalWorker := workerDoc{Name: "deepseek-1", Provider: "deepseek", Model: "deepseek-chat", EnvVar: "DEEPSEEK_API_KEY", Secret: "DEEPSEEK_API_KEY",
		Usage: 1, Harness: "opencode", HarnessArgs: []string{"run", "--model", "{model}", "--", "{prompt}"}, WorkerDir: "/home/nova/worker",
		Deadline: "20m", ReadRoots: []string{"/home/nova/go"}, MaxTurns: 80}
	t.Run("the worker description", func(t *testing.T) {
		t.Parallel()
		// no list or text is bounded: 200 entries, entries of 5000 bytes, a 70000-byte path
		long := typicalWorker
		long.HarnessArgs = many(200, func(i int) string { return fmt.Sprintf("--arg-%d", i) })
		long.ReadRoots = many(200, func(i int) string { return fmt.Sprintf("/read/root/%d/%s", i, strings.Repeat("d", 5000)) })
		long.InputLimitPhrases = many(200, func(i int) string { return fmt.Sprintf("phrase %d", i) })
		long.WorkerDir = "/" + strings.Repeat("w", 70000)
		long.MaxTurns, long.MaxCacheRead, long.Usage, long.WorkerClass, long.KeepData = math.MaxInt32, 1<<53-1, 2, 2, true
		w := func(edit func(*workerDoc)) workerDoc { d := typicalWorker; edit(&d); return d }
		runDoc(t, workerTable(),
			map[string]workerDoc{"typical": typicalWorker, "long": long},
			map[string]workerDoc{
				"usage 3":           w(func(d *workerDoc) { d.Usage = 3 }),
				"class 3":           w(func(d *workerDoc) { d.WorkerClass = 3 }),
				"max_turns -1":      w(func(d *workerDoc) { d.MaxTurns = -1 }),
				"max_cache_read -1": w(func(d *workerDoc) { d.MaxCacheRead = -1 }),
				"no name":           w(func(d *workerDoc) { d.Name = "" }),
				"no usage":          w(func(d *workerDoc) { d.Usage = 0 }),
				"no harness args":   w(func(d *workerDoc) { d.HarnessArgs = nil }),
				"a NUL in a model":  w(func(d *workerDoc) { d.Model = "a\x00b" }),
				"an empty arg":      w(func(d *workerDoc) { d.HarnessArgs = []string{""} }),
			},
			map[string][2]workerDoc{
				"usage over 2":        {w(func(d *workerDoc) { d.Usage = 1 }), w(func(d *workerDoc) { d.Usage = 2 })},
				"worker_class over 2": {w(func(d *workerDoc) { d.WorkerClass = 1 }), w(func(d *workerDoc) { d.WorkerClass = 2 })},
			})
	})
	t.Run("the fence config", func(t *testing.T) {
		t.Parallel()
		typical := fenceDoc{JobDir: "/home/nova/jobs/a", Reads: []string{"/sys/kernel/security/lsm"},
			Rules:    []ruleDoc{{"/home/nova/jobs/a/*", 1}, {"/home/nova/jobs/a/**", 1}},
			Webfetch: 2}
		long := typical
		long.Reads = many(200, func(i int) string { return fmt.Sprintf("/read/%d/%s", i, strings.Repeat("r", 3000)) })
		long.Rules = nil
		for i := range 2 + 4*200 {
			long.Rules = append(long.Rules, ruleDoc{fmt.Sprintf("/rule/%d/%s", i, strings.Repeat("p", 2000)), uint8(1 + i%2)})
		}
		f := func(edit func(*fenceDoc)) fenceDoc {
			d := typical
			d.Rules = append([]ruleDoc{}, typical.Rules...)
			edit(&d)
			return d
		}
		runDoc(t, fenceTable(),
			map[string]fenceDoc{"typical": typical, "long": long},
			map[string]fenceDoc{
				"webfetch 3":  f(func(d *fenceDoc) { d.Webfetch = 3 }),
				"action 3":    f(func(d *fenceDoc) { d.Rules[0].Action = 3 }),
				"no job dir":  f(func(d *fenceDoc) { d.JobDir = "" }),
				"no webfetch": f(func(d *fenceDoc) { d.Webfetch = 0 }),
				"a NUL path":  f(func(d *fenceDoc) { d.Reads = []string{"/a\x00"} }),
			},
			map[string][2]fenceDoc{
				"webfetch over 2": {f(func(d *fenceDoc) { d.Webfetch = 1 }), f(func(d *fenceDoc) { d.Webfetch = 2 })},
			})
	})
}
