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
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

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
var elementBounds = map[string][]TableFieldInfo{
	"Path":       PathTableFields,
	"HarnessArg": HarnessArgTableFields,
	"Phrase":     PhraseTableFields,
	"FenceRule":  FenceRuleTableFields,
}

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

func isString(f TableFieldInfo) bool  { return f.TypeName == "string" && !f.IsArray }
func isCounted(f TableFieldInfo) bool { return f.IsArray && f.Counted }
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

	// The list bounds are not smaller than what the code accepts today:
	// LoadWorker (pkg/swarm/worker.go) and FencePermission (fence.go) take
	// every entry they are given, so a description of 17 harness args or a
	// card naming 9 READ: paths (2 job patterns and 4 per read) is a record,
	// and the real data (5 args, 12 reads) sits well inside.
	t.Run("the list bounds hold what the code accepts", func(t *testing.T) {
		t.Parallel()
		bound := func(fields []TableFieldInfo, name string) int {
			for _, f := range fields {
				if f.Name == name {
					return int(f.ArrayBound)
				}
			}
			t.Fatalf("no field %s", name)
			return 0
		}
		assert.GreaterOrEqual(t, bound(WorkerRecordTableFields, "harness_args"), 17)
		assert.GreaterOrEqual(t, bound(WorkerRecordTableFields, "read_roots"), 9)
		assert.GreaterOrEqual(t, bound(WorkerRecordTableFields, "input_limit_phrases"), 17)
		assert.GreaterOrEqual(t, bound(FenceConfigTableFields, "reads"), 12)
		assert.GreaterOrEqual(t, bound(FenceConfigTableFields, "rules"), 2+4*bound(FenceConfigTableFields, "reads"))
	})

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
	t.Run("the worker description", func(t *testing.T) {
		t.Parallel()
		run(t, table[WorkerRecord]{
			name: "WorkerRecord", fields: WorkerRecordTableFields, reset: WorkerRecordReset, load: WorkerRecordLoad,
			fromJSON: WorkerRecordFromJson, measure: WorkerRecordMeasure, save: WorkerRecordSave,
			toMeas: WorkerRecordToJsonMeasure, toJSON: WorkerRecordToJson, saveText: WorkerRecordSaveText, loadText: WorkerRecordLoadText,
			required: []string{"name", "provider", "model", "env_var", "harness", "worker_dir", "deadline", "usage", "harness_args"},
			typical: func(v *WorkerRecord) {
				s := reflect.ValueOf(v).Elem()
				setText(s, "Name", "deepseek-1")
				setText(s, "Provider", "deepseek")
				setText(s, "Model", "deepseek-chat")
				setText(s, "EnvVar", "DEEPSEEK_API_KEY")
				setText(s, "Secret", "DEEPSEEK_API_KEY")
				setText(s, "Harness", "opencode")
				setText(s, "WorkerDir", "/home/nova/worker")
				setText(s, "Deadline", "20m")
				v.Usage = 1
				for i, a := range []string{"run", "--model", "{model}", "--", "{prompt}"} {
					setText(reflect.ValueOf(&v.HarnessArgs[i]).Elem(), "Text", a)
				}
				v.HarnessArgsCount = 5
				setText(reflect.ValueOf(&v.ReadRoots[0]).Elem(), "Text", "/home/nova/go")
				v.ReadRootsCount = 1
				v.MaxTurns = 80
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
	t.Run("the fence config", func(t *testing.T) {
		t.Parallel()
		run(t, table[FenceConfig]{
			name: "FenceConfig", fields: FenceConfigTableFields, reset: FenceConfigReset, load: FenceConfigLoad,
			fromJSON: FenceConfigFromJson, measure: FenceConfigMeasure, save: FenceConfigSave,
			toMeas: FenceConfigToJsonMeasure, toJSON: FenceConfigToJson, saveText: FenceConfigSaveText, loadText: FenceConfigLoadText,
			required: []string{"job_dir", "webfetch"},
			typical: func(v *FenceConfig) {
				s := reflect.ValueOf(v).Elem()
				setText(s, "JobDir", "/home/nova/jobs/a")
				setText(reflect.ValueOf(&v.Reads[0]).Elem(), "Text", "/sys/kernel/security/lsm")
				v.ReadsCount = 1
				for i, p := range []string{"/home/nova/jobs/a/*", "/home/nova/jobs/a/**"} {
					setText(reflect.ValueOf(&v.Rules[i]).Elem(), "Pattern", p)
					v.Rules[i].Action = 1
				}
				v.RulesCount = 2
				v.Webfetch = 2
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
