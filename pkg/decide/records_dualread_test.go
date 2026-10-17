package decide_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/decide"
	"github.com/mas-bandwidth/nova-tools/pkg/decidewire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
)

// TestDecideRecordLinesDualRead is the reader half of the decide records'
// table adoption (v1.5 aspect 13; the owner's rules S2 and S4): every record
// the decide package reads back (a record line, an attempt decision, the
// import of a log export, a decision schema) is read alike from its binary
// image (the table wire), the padded standard base64 of that image, and the
// legacy form its writer writes today; every damaged value of each form is
// refused by name (`<RECORD> REFUSED: <reason>; run: <help>`) and never read
// as absent; and each read is counted under its record and its form.

func TestDecideRecordLinesDualRead(t *testing.T) {
	t.Parallel()

	t.Run("record line", func(t *testing.T) {
		t.Parallel()
		d := decisionSample()
		want := decide.RecordLine{Decision: &d}
		legacy, err := json.Marshal(map[string]any{"decision": d})
		require.NoError(t, err)
		over := decisionSample()
		a := over.Answers["class"]
		a.P = map[string]float64{"done": 1.5}
		over.Answers["class"] = a
		overLegacy, err := json.Marshal(map[string]any{"decision": over})
		require.NoError(t, err)
		line := dual[decide.RecordLine]{name: "RECORD_LINE", want: want, wire: decisionLineImage(t, d), legacy: legacy,
			clamped: []formInput{{"binary", decisionLineImage(t, over)}, {"base64", []byte(base64.StdEncoding.EncodeToString(decisionLineImage(t, over)))}, {"legacy", overLegacy}},
			read:    func(rd *decide.Parser, in []byte) (decide.RecordLine, error) { return rd.RecordLine(in) },
			counts:  func(rd *decide.Parser) *schemaread.Counts { return rd.RecordLineCounts() }}
		line.run(t)
		// the files are read from a directory the test writes
		dir := t.TempDir()
		rd := decide.NewParser()
		for name, body := range map[string][]byte{
			"binary": line.wire,
			"base64": []byte(base64.StdEncoding.EncodeToString(line.wire)),
			"legacy": line.legacy,
		} {
			path := filepath.Join(dir, name)
			require.NoError(t, os.WriteFile(path, body, 0o600))
			in, err := os.ReadFile(path)
			require.NoError(t, err)
			got, err := rd.RecordLine(in)
			require.NoError(t, err, name)
			assert.Equal(t, want, got, name)
		}
	})

	t.Run("attempt decision", func(t *testing.T) {
		t.Parallel()
		d := decisionSample()
		legacy, err := json.Marshal(d)
		require.NoError(t, err)
		over := decisionSample()
		a := over.Answers["class"]
		a.P = map[string]float64{"done": 1.5}
		over.Answers["class"] = a
		overLegacy, err := json.Marshal(over)
		require.NoError(t, err)
		overWire := decisionImage(t, over)
		att := dual[decide.Decision]{name: "DECISION", want: d, wire: decisionImage(t, d), legacy: legacy,
			clamped: []formInput{{"binary", overWire}, {"base64", []byte(base64.StdEncoding.EncodeToString(overWire))}, {"legacy", overLegacy}},
			read:    func(rd *decide.Parser, in []byte) (decide.Decision, error) { return rd.Attempt(in) },
			counts:  func(rd *decide.Parser) *schemaread.Counts { return rd.AttemptCounts() }}
		att.run(t)
	})

	t.Run("import export", func(t *testing.T) {
		t.Parallel()
		want := decide.Export{Lines: []decide.ExportLine{{Verb: "rework", Answers: []string{"judgment-0001", "judgment-0002"}}}}
		legacy, err := json.Marshal(map[string]any{"lines": []map[string]any{{"verb": "rework", "answers": []string{"judgment-0001", "judgment-0002"}}}})
		require.NoError(t, err)
		ex := dual[decide.Export]{name: "EXPORT", want: want, wire: exportImage(t), legacy: legacy,
			read:   func(rd *decide.Parser, in []byte) (decide.Export, error) { return rd.ReadExport(in) },
			counts: func(rd *decide.Parser) *schemaread.Counts { return rd.ExportCounts() }}
		ex.run(t)
	})

	t.Run("decision schema", func(t *testing.T) {
		t.Parallel()
		want := schemaSample()
		legacy, err := json.Marshal(want)
		require.NoError(t, err)
		sc := dual[decide.Schema]{name: "SCHEMA", want: want, wire: schemaImage(t, want), legacy: legacy,
			read:   func(rd *decide.Parser, in []byte) (decide.Schema, error) { return rd.Schema(in) },
			counts: func(rd *decide.Parser) *schemaread.Counts { return rd.SchemaCounts() }}
		sc.run(t)
	})

	t.Run("named cases", func(t *testing.T) {
		t.Parallel()
		rd := decide.NewParser()
		flipped := func(wire []byte, at int, mask byte) []byte {
			out := bytes.Clone(wire)
			out[at] ^= mask
			return out
		}
		// criteria under a kind that is not a choice is refused, never a panic
		_, err := rd.Schema(flipped(schemaImage(t, schemaSample()), 147, 0x80))
		refusal(t, err, "SCHEMA")
		_, err = decide.ParseSchema(flipped(schemaImage(t, schemaSample()), 147, 0x80))
		require.Error(t, err)
		// a reference moved to another field's text, a field read as empty
		d := decisionImage(t, decisionSample())
		for _, c := range []struct {
			at   int
			mask byte
		}{{3, 0x01}, {261, 0x80}} {
			_, err := rd.Attempt(flipped(d, c.at, c.mask))
			ref := refusal(t, err, "DECISION")
			if ref != nil {
				assert.Equal(t, schemaread.ReasonWireDamaged, ref.Reason, "byte %d ^%#02x", c.at, c.mask)
			}
		}
		// an export read with no lines imports nothing: refused by name in every form
		_, err = rd.ReadExport(flipped(exportImage(t), 69, 0x80))
		refusal(t, err, "EXPORT")
		var empty decidewire.ImportExportBuilder
		require.True(t, empty.Init())
		emptyWire := wireSave(t, &empty, decidewire.ImportExportMeasure, decidewire.ImportExportSave)
		for _, in := range [][]byte{emptyWire, []byte(base64.StdEncoding.EncodeToString(emptyWire)), []byte(`{"lines":[]}`), []byte(`{}`)} {
			_, err := rd.ReadExport(in)
			ref := refusal(t, err, "EXPORT")
			if ref != nil {
				assert.Equal(t, schemaread.ReasonAbsentRequired, ref.Reason)
				assert.Equal(t, "lines", ref.Field)
			}
		}
		// a legacy decision line with no decision name reads as the base read it
		got, err := rd.RecordLine([]byte(`{"decision":{"id":"a"}}`))
		require.NoError(t, err)
		require.NotNil(t, got.Decision)
		assert.Equal(t, "a", got.Decision.ID)
		assert.Empty(t, got.Decision.Decision)
	})
}

// formInput is one value in one of the forms a reader takes.
type formInput struct {
	form string
	in   []byte
}

// dual is one record's dual read: its value, its binary image and its legacy
// form, and how a reader hands each to the reader. clamped holds values past a
// declared bound in each form; a record whose schema declares no bound has none.
type dual[T any] struct {
	name    string
	want    T
	wire    []byte
	legacy  []byte
	clamped []formInput
	read    func(rd *decide.Parser, in []byte) (T, error)
	counts  func(rd *decide.Parser) *schemaread.Counts
}

func (d dual[T]) run(t *testing.T) {
	t.Helper()
	rd := decide.NewParser()
	inputs := []formInput{
		{"binary", d.wire},
		{"base64", []byte(base64.StdEncoding.EncodeToString(d.wire))},
		{"legacy", d.legacy},
	}
	for _, in := range inputs {
		got, err := d.read(rd, in.in)
		require.NoError(t, err, "%s %s", d.name, in.form)
		assert.Equal(t, d.want, got, "%s read alike from its %s", d.name, in.form)
	}
	counts := d.counts(rd)
	assert.Equal(t, uint64(1), counts.Reads(schemaread.FormBinary), d.name)
	assert.Equal(t, uint64(1), counts.Reads(schemaread.FormBase64), d.name)
	assert.Equal(t, uint64(1), counts.Reads(schemaread.FormLegacy), d.name)
	assert.Zero(t, counts.Reads(schemaread.FormJSON), d.name)

	// A truncated, a flipped-byte and an appended value of every form is
	// refused by name and counted under the form it arrived in.
	rd = decide.NewParser()
	wantReads := map[string]uint64{}
	for _, in := range inputs {
		wantReads[in.form] = 0
		for _, m := range damage(in.form, in.in) {
			got, err := d.read(rd, m)
			require.NotNil(t, refusal(t, err, d.name), "%s: a damaged %s (%d bytes) was read as %v", d.name, in.form, len(m), got)
			var zero T
			assert.Equal(t, zero, got, "%s: a refused read hands back nothing", d.name)
			wantReads[in.form]++
		}
	}
	counts = d.counts(rd)
	assert.Equal(t, wantReads["binary"], counts.Reads(schemaread.FormBinary), "%s damaged binary reads are counted", d.name)
	assert.Equal(t, wantReads["base64"], counts.Reads(schemaread.FormBase64), "%s damaged base64 reads are counted", d.name)
	assert.Equal(t, wantReads["legacy"], counts.Reads(schemaread.FormLegacy), "%s damaged legacy reads are counted", d.name)
	assert.Equal(t, wantReads["binary"]+wantReads["base64"]+wantReads["legacy"], refusals(counts), "%s: every damaged read is refused, by reason", d.name)

	// A value past a declared bound is refused, never clamped into the value.
	// A record whose schema declares no bound (the export, the schema) has no
	// clamped case to make.
	for _, in := range d.clamped {
		got, err := d.read(rd, in.in)
		ref := refusal(t, err, d.name)
		if ref != nil {
			assert.Equal(t, schemaread.ReasonClamped, ref.Reason, "%s: a clamped %s", d.name, in.form)
		}
		var zero T
		assert.Equal(t, zero, got, "%s: a refused %s hands back nothing", d.name, in.form)
	}

	flipProbe(t, d.name, d.want, d.wire, d.read)
}

// flipProbe flips each byte of a record's binary image, and of the image
// inside its base64, under each of three masks (the low bit, the high bit,
// every bit), and reads it. No flip panics. Every flip that moves the shape of
// the value (a string read from another field's text, a field or a list read
// as empty, an entry gained or lost) is refused by name: the reader holds a
// typed image to the one canonical image of the value it reads. A flip that
// leaves the shape and changes a number in range, or text in place that stays
// UTF-8, reads as another value: the wire carries no checksum, so nothing in
// it can tell those from a value the writer wrote. They are counted and logged,
// never mistaken for damage (the schema compiler's fix is the framing checksum).
func flipProbe[T any](t *testing.T, name string, want T, wire []byte, read func(rd *decide.Parser, in []byte) (T, error)) {
	t.Helper()
	wantJSON := flatJSON(t, want)
	strs := map[string]bool{}
	collectStrings(wantJSON, strs)
	for _, form := range []string{"binary", "base64"} {
		for _, mask := range []byte{0x01, 0x80, 0xff} {
			rd := decide.NewParser()
			var refused, same, number, text, structural, panics int
			for i := range wire {
				in := bytes.Clone(wire)
				in[i] ^= mask
				if form == "base64" {
					in = []byte(base64.StdEncoding.EncodeToString(in))
				}
				var got T
				var err error
				panicked := func() (p bool) {
					defer func() {
						r := recover()
						p = r != nil
						assert.Nil(t, r, "%s %s byte %d ^%#02x panics", name, form, i, mask)
					}()
					got, err = read(rd, in)
					return false
				}()
				switch {
				case panicked:
					panics++
				case err != nil:
					refusal(t, err, name)
					refused++
				case reflect.DeepEqual(got, want):
					same++
				default:
					switch shapeOf(wantJSON, flatJSON(t, got), strs) {
					case flipNumber:
						number++
					case flipText:
						text++
					default:
						structural++
						t.Errorf("%s %s byte %d ^%#02x is read silently with another shape: %+v", name, form, i, mask, got)
					}
				}
			}
			t.Logf("%s %s mask %#02x: %d bytes, %d refused, %d unchanged, %d number, %d in-place text, %d structural, %d panics",
				name, form, mask, len(wire), refused, same, number, text, structural, panics)
			assert.Equal(t, len(wire), refused+same+number+text+structural+panics, name)
			assert.Zero(t, panics, "%s %s mask %#02x: no flip panics", name, form, mask)
			assert.Zero(t, structural, "%s %s mask %#02x: no flip moves the shape silently", name, form, mask)
		}
	}
}

// The classes of a flip read as another value, worst last.
const (
	flipSame = iota
	flipNumber
	flipText
	flipStructural
)

func flatJSON(t *testing.T, v any) any {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	var out any
	require.NoError(t, json.Unmarshal(raw, &out))
	return out
}

func collectStrings(v any, into map[string]bool) {
	switch v := v.(type) {
	case string:
		into[v] = true
	case []any:
		for _, e := range v {
			collectStrings(e, into)
		}
	case map[string]any:
		for k, e := range v {
			into[k] = true
			collectStrings(e, into)
		}
	}
}

// shapeOf classes a read value against the value written: a number changed is
// number; a string changed in place to text no field held is in-place text;
// anything else (an entry gained or lost, a string read as empty or as another
// field's text, a value of another kind) moves the shape.
func shapeOf(want, got any, strs map[string]bool) int {
	switch w := want.(type) {
	case string:
		g, ok := got.(string)
		switch {
		case !ok:
			return flipStructural
		case g == w:
			return flipSame
		case g == "" || strs[g]:
			return flipStructural
		}
		return flipText
	case float64:
		g, ok := got.(float64)
		switch {
		case !ok:
			return flipStructural
		case g == w:
			return flipSame
		}
		return flipNumber
	case []any:
		g, ok := got.([]any)
		if !ok || len(g) != len(w) {
			return flipStructural
		}
		worst := flipSame
		for i := range w {
			worst = max(worst, shapeOf(w[i], g[i], strs))
		}
		return worst
	case map[string]any:
		g, ok := got.(map[string]any)
		if !ok || len(g) != len(w) {
			return flipStructural
		}
		worst := flipSame
		var lostW, lostG []string
		for k := range w {
			if _, ok := g[k]; ok {
				worst = max(worst, shapeOf(w[k], g[k], strs))
			} else {
				lostW = append(lostW, k)
			}
		}
		for k := range g {
			if _, ok := w[k]; !ok {
				lostG = append(lostG, k)
			}
		}
		switch {
		case len(lostW) == 0:
		case len(lostW) == 1 && len(lostG) == 1:
			// one map key changed in place: the key is text, its value compared
			worst = max(worst, shapeOf(lostW[0], lostG[0], strs), shapeOf(w[lostW[0]], g[lostG[0]], strs))
		default:
			return flipStructural
		}
		return worst
	}
	if reflect.DeepEqual(want, got) {
		return flipSame
	}
	return flipStructural
}

// refusals sums every refused_<reason> counter.
func refusals(c *schemaread.Counts) uint64 {
	var n uint64
	for k, v := range c.Snapshot() {
		if strings.HasPrefix(k, "refused_") {
			n += v
		}
	}
	return n
}

// damage is the malformed corpus of one form: truncations, appended tails, a
// flipped byte at the form byte and at the framing trailer, and for the legacy
// text a flipped brace and a JSON value of the wrong kind.
func damage(form string, in []byte) [][]byte {
	out := damaged(in)
	switch form {
	case "legacy":
		// every entry keeps its opening brace, so it is still the legacy text;
		// the flipped byte is the first key's opening quote.
		out = append(out, flipByte(in, 1), []byte("{"))
	case "binary":
		out = append(out, flipByte(in, 0), flipByte(in, len(in)-1))
	case "base64":
		// the first character names the form; flip the form byte and the tail
		// of the image itself, then wrap it in base64 again
		dec, err := base64.StdEncoding.DecodeString(string(in))
		if err == nil && len(dec) > 0 {
			out = append(out,
				[]byte(base64.StdEncoding.EncodeToString(flipByte(dec, 0))),
				[]byte(base64.StdEncoding.EncodeToString(flipByte(dec, len(dec)-1))))
		}
	}
	return out
}

func flipByte(in []byte, at int) []byte {
	out := bytes.Clone(in)
	out[at] ^= 0xff
	return out
}

var refusalText = regexp.MustCompile(`^[A-Z_]+ REFUSED: [^;]+; run: \S.*$`)

func refusal(t *testing.T, err error, name string) *schemaread.Refusal {
	t.Helper()
	var ref *schemaread.Refusal
	if !assert.True(t, errors.As(err, &ref), "%s: want a named refusal, got %v", name, err) {
		return nil
	}
	assert.Equal(t, name, ref.Name)
	assert.Regexp(t, refusalText, err.Error())
	return ref
}

func damaged(in []byte) [][]byte {
	var out [][]byte
	for n := 1; n < len(in); n++ {
		out = append(out, bytes.Clone(in[:n]))
	}
	for _, tail := range []string{"\x00", "x", "AAAA"} {
		out = append(out, append(bytes.Clone(in), tail...))
	}
	return out
}

func setS(w *decidewire.TableWorker, slot *int64, text string) {
	if text != "" {
		decidewire.TableStringEmplace(w, slot, []byte(text))
	}
}

func decisionSample() decide.Decision {
	return decide.Decision{
		ID: "a13@1.0123456789ab", Decision: "attempt", Schema: "0123456789abcdef", Backend: "jev",
		At: "2026-10-17T04:07:16Z", State: "CARD ...",
		Inputs:  map[string]string{"brief_sha256": "abc", "reason": "red"},
		Answers: map[string]decide.Answer{"class": {Type: "choice", Value: "done", P: map[string]float64{"done": 0.93}}},
		Usage:   decide.Usage{InputTokens: 1200, OutputTokens: 85},
	}
}

func decisionImage(t *testing.T, d decide.Decision) []byte {
	t.Helper()
	var b decidewire.DecisionBuilder
	require.True(t, b.Init())
	fillDecision(t, &b.Main, b.GetRoot(), d)
	return wireSave(t, &b, decidewire.DecisionMeasure, decidewire.DecisionSave)
}

func decisionLineImage(t *testing.T, d decide.Decision) []byte {
	t.Helper()
	var b decidewire.RecordLineBuilder
	require.True(t, b.Init())
	w := b.GetRoot()
	w.Kind = decidewire.LineKindDecision
	fillDecision(t, &b.Main, decidewire.DecisionEmplace(&b.Main, &w.AsDecision), d)
	return wireSave(t, &b, decidewire.RecordLineMeasure, decidewire.RecordLineSave)
}

func exportImage(t *testing.T) []byte {
	t.Helper()
	var b decidewire.ImportExportBuilder
	require.True(t, b.Init())
	w := b.GetRoot()
	l := decidewire.ImportExportLinesAdd(&b.Main, &w.Lines)
	setS(&b.Main, &l.Verb, "rework")
	for _, id := range []string{"judgment-0001", "judgment-0002"} {
		setS(&b.Main, &decidewire.ImportLogLineAnswersAdd(&b.Main, &l.Answers).Id, id)
	}
	return wireSave(t, &b, decidewire.ImportExportMeasure, decidewire.ImportExportSave)
}

func schemaSample() decide.Schema {
	return decide.Schema{Name: "gate-class", Questions: map[string]decide.Question{
		"choice-0": {Type: "choice", Instructions: "why", Criteria: map[string]string{"A": "a", "B": "b"}},
		"noul-0":   {Type: "noul", Instructions: "is it so?"},
	}}
}

func schemaImage(t *testing.T, s decide.Schema) []byte {
	t.Helper()
	var b decidewire.DecideSchemaBuilder
	require.True(t, b.Init())
	w := b.GetRoot()
	setS(&b.Main, &w.Name, s.Name)
	for _, name := range []string{"choice-0", "noul-0"} {
		q := decidewire.DecideSchemaQuestionsAdd(&b.Main, &w.Questions)
		setS(&b.Main, &q.Name, name)
		qq := s.Questions[name]
		if strings.HasPrefix(name, "choice") {
			q.Question.Kind = decidewire.AnswerTypeChoice
			for opt, meaning := range qq.Criteria {
				c := decidewire.QuestionCriteriaAdd(&b.Main, &q.Question.Criteria)
				setS(&b.Main, &c.Option, opt)
				setS(&b.Main, &c.Meaning, meaning)
			}
		} else {
			q.Question.Kind = decidewire.AnswerTypeNoul
		}
		setS(&b.Main, &q.Question.Instructions, qq.Instructions)
	}
	return wireSave(t, &b, decidewire.DecideSchemaMeasure, decidewire.DecideSchemaSave)
}

func wireSave[R any, B any](t *testing.T, b B, measure func(*R, ...decidewire.TableWriteContext) int64, save func(*R, []byte, ...decidewire.TableWriteContext) int64) []byte {
	t.Helper()
	type builder interface {
		Lock() bool
		AsConst() *R
	}
	bd := any(b).(builder)
	require.True(t, bd.Lock())
	root := bd.AsConst()
	n := measure(root)
	require.Positive(t, n)
	buf := make([]byte, n)
	require.Equal(t, n, save(root, buf))
	return buf
}

func fillDecision(t *testing.T, w *decidewire.TableWorker, r *decidewire.DecisionRow, d decide.Decision) {
	t.Helper()
	setS(w, &r.Id, d.ID)
	setS(w, &r.DecisionName, d.Decision)
	setS(w, &r.Schema, d.Schema)
	setS(w, &r.Backend, d.Backend)
	setS(w, &r.At, d.At)
	setS(w, &r.State, d.State)
	r.Usage = decidewire.UsageRow{InputTokens: int64(d.Usage.InputTokens), OutputTokens: int64(d.Usage.OutputTokens)}
	for k, v := range d.Inputs {
		in := decidewire.DecisionInputsAdd(w, &r.Inputs)
		setS(w, &in.Key, k)
		setS(w, &in.Value, v)
	}
	for q, a := range d.Answers {
		na := decidewire.DecisionAnswersAdd(w, &r.Answers)
		setS(w, &na.Question, q)
		na.Answer.Kind = decidewire.AnswerTypeChoice
		setS(w, &na.Answer.Value, a.Value)
		for opt, p := range a.P {
			o := decidewire.AnswerPAdd(w, &na.Answer.P)
			setS(w, &o.Option, opt)
			o.P = p
		}
	}
}
