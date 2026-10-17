package decide_test

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
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
		line := dual[decide.RecordLine]{name: "RECORD_LINE", want: want, wire: decisionLineImage(t, d), legacy: legacy,
			read:   func(rd *decide.Parser, in []byte) (decide.RecordLine, error) { return rd.RecordLine(in) },
			counts: func(rd *decide.Parser) *schemaread.Counts { return rd.RecordLineCounts() }}
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
		att := dual[decide.Decision]{name: "DECISION", want: d, wire: decisionImage(t, d), legacy: legacy,
			read:   func(rd *decide.Parser, in []byte) (decide.Decision, error) { return rd.Attempt(in) },
			counts: func(rd *decide.Parser) *schemaread.Counts { return rd.AttemptCounts() }}
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
}

// dual is one record's dual read: its value, its binary image and its legacy
// form, and how a reader hands each to the reader.
type dual[T any] struct {
	name   string
	want   T
	wire   []byte
	legacy []byte
	read   func(rd *decide.Parser, in []byte) (T, error)
	counts func(rd *decide.Parser) *schemaread.Counts
}

func (d dual[T]) run(t *testing.T) {
	t.Helper()
	rd := decide.NewParser()
	inputs := []struct {
		form string
		in   []byte
	}{
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

	rd = decide.NewParser()
	tries := 0
	for _, in := range inputs {
		if in.form == "legacy" {
			continue
		}
		for _, m := range damaged(in.in) {
			got, err := d.read(rd, m)
			require.NotNil(t, refusal(t, err, d.name), "%s: a damaged %s (%d bytes) was read as %v", d.name, in.form, len(m), got)
			var zero T
			assert.Equal(t, zero, got, "%s: a refused read hands back nothing", d.name)
			tries++
		}
	}
	refusals := uint64(0)
	for k, n := range d.counts(rd).Snapshot() {
		if strings.HasPrefix(k, "refused_") {
			refusals += n
		}
	}
	assert.Equal(t, uint64(tries), refusals, "%s: every damaged read is counted as refused, by reason", d.name)
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
	for n := 0; n < len(in); n++ {
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
