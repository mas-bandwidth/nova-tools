package decide

import (
	"encoding/json"
	"strings"
	"unsafe"

	"github.com/mas-bandwidth/nova-tools/pkg/decidewire"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
)

// The decide records read both forms (the owner's rule S2, readers first S4):
// the binary image of the generated table (pkg/decidewire) and the padded
// standard base64 of that image, plus the legacy form each writer writes today.
// Every read is counted per record and per form (UNION.md C13). The legacy
// form is read exactly as it always was, so a legacy line that does not parse
// names itself as it always has; the typed forms are refused by name.

// form is the one byte a variable table's wire opens with (S2).
const variableForm = 1

// decided is one record's typed forms and its legacy read. Both go through
// pkg/schemaread, so every read is counted under its record and its form and a
// damaged value of any form is refused by name. The legacy read is the
// record's own decode.
type decided[T any] struct {
	name   string
	help   string
	read   func(*T, []byte) schemaread.Report
	legacy func(*T, []byte) schemaread.Report
	check  func(*T, *schemaread.Checker)
}

func (d decided[T]) reader() *schemaread.Reader[T] {
	return schemaread.NewReader(&schemaread.Codec[T]{
		Name:        d.name,
		Help:        d.help,
		Forms:       []byte{variableForm},
		Reset:       func(v *T) { *v = *new(T) },
		LoadWire:    d.read,
		Legacy:      func(in []byte) bool { return len(in) > 0 && in[0] == '{' },
		LoadLegacy:  d.legacy,
		Constraints: d.check,
	})
}

// recordLegacy reads one JSON value into v the way the writer's own decode did.
func recordLegacy[T any](v *T, in []byte) schemaread.Report {
	if err := json.Unmarshal(in, v); err != nil {
		return schemaread.Report{Malformed: true}
	}
	return schemaread.Report{}
}

// reportOf maps a generated table report to the strict read's report.
func reportOf(rep *decidewire.TableReport, ok bool) schemaread.Report {
	out := schemaread.Report{Unknown: int(rep.Unknown), KindMismatch: int(rep.KindMismatch), Clamped: int(rep.Clamped), Malformed: rep.Malformed || !ok}
	if rep.Verdict == decidewire.TableOpenRefused || rep.Verdict == decidewire.TableOpenDamaged {
		out.Reason = rep.Err().Error()
	}
	return out
}

// strAt is the text of a string slot in the arena a table was read into.
func strAt(slot int64, ar []*decidewire.TableArena) string {
	if slot == 0 {
		return ""
	}
	v := decidewire.TableStringAt(&slot, ar...)
	return string(v.Data)
}

// answerFromTable is an answer as the Go code holds it.
func answerFromTable(a *decidewire.AnswerRow, ar []*decidewire.TableArena) Answer {
	out := Answer{Type: typeOf(a.Kind), Value: strAt(a.Value, ar), Confidence: a.Confidence, Method: strAt(a.Method, ar)}
	out.P = map[string]float64{}
	decidewire.AnswerPEach(&a.P, func(o *decidewire.OptionPRow) bool {
		out.P[strAt(o.Option, ar)] = o.P
		return true
	}, ar...)
	return out
}

// decisionFromTable is a decision as the record holds it.
func decisionFromTable(r *decidewire.DecisionRow, ar []*decidewire.TableArena) Decision {
	d := Decision{
		ID: strAt(r.Id, ar), Decision: strAt(r.DecisionName, ar), Schema: strAt(r.Schema, ar),
		Backend: strAt(r.Backend, ar), At: strAt(r.At, ar), State: strAt(r.State, ar),
		Usage:  Usage{InputTokens: int(r.Usage.InputTokens), OutputTokens: int(r.Usage.OutputTokens)},
		Inputs: map[string]string{}, Answers: map[string]Answer{},
	}
	decidewire.DecisionInputsEach(&r.Inputs, func(in *decidewire.InputRow) bool {
		d.Inputs[strAt(in.Key, ar)] = strAt(in.Value, ar)
		return true
	}, ar...)
	decidewire.DecisionAnswersEach(&r.Answers, func(a *decidewire.NamedAnswerRow) bool {
		d.Answers[strAt(a.Question, ar)] = answerFromTable(&a.Answer, ar)
		return true
	}, ar...)
	return d
}

func typeOf(k decidewire.AnswerType) string {
	if k == decidewire.AnswerTypeNoul {
		return Noul
	}
	return Choice
}

// ---- the decision (ParseAttempt, the record's decision line) ----

func parseAttemptTyped(v *Decision, wire []byte) schemaread.Report {
	var b decidewire.DecisionBuilder
	if !b.Init() {
		return schemaread.Report{Reason: schemaread.ReasonMalformed}
	}
	defer b.Shutdown()
	var rep decidewire.TableReport
	if !decidewire.DecisionLoadBuilder(&b, wire, &rep) {
		return reportOf(&rep, false)
	}
	root := b.GetRoot()
	ar := []*decidewire.TableArena{&b.Arena}
	*v = decisionFromTable(root, ar)
	return reportOf(&rep, true)
}

// newAttemptReader is the attempt decision's reader; a finish carries the
// decision as one JSON record line, which is the legacy form.
func newAttemptReader() *schemaread.Reader[Decision] {
	return decided[Decision]{name: "DECISION", help: "nova-sprint answer -h", read: parseAttemptTyped, legacy: func(v *Decision, in []byte) schemaread.Report { return recordLegacy(v, in) }}.reader()
}

// ParseAttemptReader is the attempt decision's package reader.
var ParseAttemptReader = newAttemptReader()

// ---- the schema (ParseSchema) ----

func questionFromTable(q *decidewire.QuestionRow, ar []*decidewire.TableArena) Question {
	out := Question{Type: typeOf(q.Kind), Instructions: strAt(q.Instructions, ar)}
	if q.Kind == decidewire.AnswerTypeChoice {
		out.Criteria = map[string]string{}
	}
	decidewire.QuestionCriteriaEach(&q.Criteria, func(c *decidewire.CriterionRow) bool {
		out.Criteria[strAt(c.Option, ar)] = strAt(c.Meaning, ar)
		return true
	}, ar...)
	return out
}

func schemaFromTable(r *decidewire.DecideSchemaRow, ar []*decidewire.TableArena) Schema {
	s := Schema{Name: strAt(r.Name, ar), Questions: map[string]Question{}}
	decidewire.DecideSchemaQuestionsEach(&r.Questions, func(q *decidewire.NamedQuestionRow) bool {
		s.Questions[strAt(q.Name, ar)] = questionFromTable(&q.Question, ar)
		return true
	}, ar...)
	return s
}

func parseSchemaTyped(v *Schema, wire []byte) schemaread.Report {
	var b decidewire.DecideSchemaBuilder
	if !b.Init() {
		return schemaread.Report{Reason: schemaread.ReasonMalformed}
	}
	defer b.Shutdown()
	var rep decidewire.TableReport
	if !decidewire.DecideSchemaLoadBuilder(&b, wire, &rep) {
		return reportOf(&rep, false)
	}
	root := b.GetRoot()
	ar := []*decidewire.TableArena{&b.Arena}
	*v = schemaFromTable(root, ar)
	return reportOf(&rep, true)
}

// newSchemaReader is the schema's reader; the schema's legacy form is the JSON
// object ParseSchema decodes, with no field it does not know.
func newSchemaReader() *schemaread.Reader[Schema] {
	return decided[Schema]{name: "SCHEMA", help: "nova-decide answer -h", read: parseSchemaTyped, legacy: func(v *Schema, in []byte) schemaread.Report {
		dec := json.NewDecoder(strings.NewReader(string(in)))
		dec.DisallowUnknownFields()
		if err := dec.Decode(v); err != nil {
			return schemaread.Report{Reason: err.Error()}
		}
		return schemaread.Report{}
	}}.reader()
}

// ParseSchemaReader is the schema's package reader.
var ParseSchemaReader = newSchemaReader()

// ---- the import export (judgmentItems) ----

// Export is the export judgmentItems reads: one line per log line, its verb and
// every judgment id it answers.
type Export struct {
	Lines []ExportLine
}

// ExportLine is one line of an Export.
type ExportLine struct {
	Verb    string
	Answers []string
}

func exportFromTable(r *decidewire.ImportExportRow, ar []*decidewire.TableArena) Export {
	var out Export
	decidewire.ImportExportLinesEach(&r.Lines, func(l *decidewire.ImportLogLineRow) bool {
		line := ExportLine{Verb: strAt(l.Verb, ar)}
		decidewire.ImportLogLineAnswersEach(&l.Answers, func(j *decidewire.JudgmentIdRow) bool {
			line.Answers = append(line.Answers, strAt(j.Id, ar))
			return true
		}, ar...)
		out.Lines = append(out.Lines, line)
		return true
	}, ar...)
	return out
}

func parseExportTyped(v *Export, wire []byte) schemaread.Report {
	var b decidewire.ImportExportBuilder
	if !b.Init() {
		return schemaread.Report{Reason: schemaread.ReasonMalformed}
	}
	defer b.Shutdown()
	var rep decidewire.TableReport
	if !decidewire.ImportExportLoadBuilder(&b, wire, &rep) {
		return reportOf(&rep, false)
	}
	root := b.GetRoot()
	ar := []*decidewire.TableArena{&b.Arena}
	*v = exportFromTable(root, ar)
	return reportOf(&rep, true)
}

// newExportReader is the import export's reader; the legacy form is the JSON
// object judgmentItems unmarshals.
func newExportReader() *schemaread.Reader[Export] {
	return decided[Export]{name: "EXPORT", help: "nova-decide import -h", read: parseExportTyped, legacy: func(v *Export, in []byte) schemaread.Report { return recordLegacy(v, in) }}.reader()
}

// ParseExportReader is the import export's package reader.
var ParseExportReader = newExportReader()

// ---- the record lines (record.go parse) ----

func outcomeFromTable(r *decidewire.OutcomeRow, ar []*decidewire.TableArena) Outcome {
	return Outcome{ID: strAt(r.Id, ar), Label: strAt(r.Label, ar), Note: strAt(r.Note, ar), At: strAt(r.At, ar)}
}

func actFromTable(r *decidewire.ActRow, ar []*decidewire.TableArena) Act {
	return Act{ID: strAt(r.Id, ar), Act: strAt(r.Step, ar), Op: strAt(r.Op, ar), At: strAt(r.At, ar)}
}

func lineFromTable(r *decidewire.RecordLineRow, ar []*decidewire.TableArena) (line, bool) {
	present := 0
	for _, ref := range []int64{r.AsDecision, r.AsOutcome, r.AsAct} {
		if ref != 0 {
			present++
		}
	}
	if present != 1 {
		return line{}, false
	}
	at := func(ref int64) unsafe.Pointer { return ar[0].At(decidewire.TableRef(ref)) }
	switch r.Kind {
	case decidewire.LineKindDecision:
		if r.AsDecision == 0 {
			return line{}, false
		}
		d := decisionFromTable((*decidewire.DecisionRow)(at(r.AsDecision)), ar)
		return line{Decision: &d}, true
	case decidewire.LineKindOutcome:
		if r.AsOutcome == 0 {
			return line{}, false
		}
		o := outcomeFromTable((*decidewire.OutcomeRow)(at(r.AsOutcome)), ar)
		return line{Outcome: &o}, true
	case decidewire.LineKindAct:
		if r.AsAct == 0 {
			return line{}, false
		}
		a := actFromTable((*decidewire.ActRow)(at(r.AsAct)), ar)
		return line{Act: &a}, true
	}
	return line{}, false
}

func recordLineTyped(v *line, wire []byte) schemaread.Report {
	var b decidewire.RecordLineBuilder
	if !b.Init() {
		return schemaread.Report{Reason: schemaread.ReasonMalformed}
	}
	defer b.Shutdown()
	var rep decidewire.TableReport
	if !decidewire.RecordLineLoadBuilder(&b, wire, &rep) {
		return reportOf(&rep, false)
	}
	root := b.GetRoot()
	ar := []*decidewire.TableArena{&b.Arena}
	l, ok := lineFromTable(root, ar)
	if !ok {
		return schemaread.Report{Reason: schemaread.ReasonMalformed}
	}
	*v = l
	return reportOf(&rep, true)
}

// newRecordLineReader is a record line's reader; the legacy form is the one
// JSON object the line's writer appends today.
func newRecordLineReader() *schemaread.Reader[line] {
	return decided[line]{name: "RECORD_LINE", help: "nova-decide record -h", read: recordLineTyped, legacy: func(v *line, in []byte) schemaread.Report { return recordLegacy(v, in) }}.reader()
}

// RecordLineReader is a record line's package reader.
var RecordLineReader = newRecordLineReader()

// RecordLine is one parsed record line as the dual read hands it back.
type RecordLine struct {
	Decision *Decision
	Outcome  *Outcome
	Act      *Act
}

// Parser reads the decide records in every form and counts each read per form.
// A fresh Parser has empty counters; the production read sites use the package
// readers, and a test makes its own to count.
type Parser struct {
	recordLine *schemaread.Reader[line]
	attempt    *schemaread.Reader[Decision]
	export     *schemaread.Reader[Export]
	schema     *schemaread.Reader[Schema]
}

// NewParser is a set of the decide records' readers with empty counters.
func NewParser() *Parser {
	return &Parser{
		recordLine: newRecordLineReader(),
		attempt:    newAttemptReader(),
		export:     newExportReader(),
		schema:     newSchemaReader(),
	}
}

// RecordLine reads one record line in any form.
func (p *Parser) RecordLine(in []byte) (RecordLine, error) {
	l, _, err := p.recordLine.Read(in)
	if err != nil {
		return RecordLine{}, err
	}
	return RecordLine{Decision: l.Decision, Outcome: l.Outcome, Act: l.Act}, nil
}

// Attempt reads an attempt decision in any form.
func (p *Parser) Attempt(in []byte) (Decision, error) {
	d, _, err := p.attempt.Read(in)
	return d, err
}

// ReadExport reads an import export in any form.
func (p *Parser) ReadExport(in []byte) (Export, error) {
	e, _, err := p.export.Read(in)
	return e, err
}

// Schema reads a decision schema in any form.
func (p *Parser) Schema(in []byte) (Schema, error) {
	s, _, err := p.schema.Read(in)
	return s, err
}

// RecordLineCounts is the record line reader's counters.
func (p *Parser) RecordLineCounts() *schemaread.Counts { return p.recordLine.Counts() }

// AttemptCounts is the attempt reader's counters.
func (p *Parser) AttemptCounts() *schemaread.Counts { return p.attempt.Counts() }

// ExportCounts is the export reader's counters.
func (p *Parser) ExportCounts() *schemaread.Counts { return p.export.Counts() }

// SchemaCounts is the schema reader's counters.
func (p *Parser) SchemaCounts() *schemaread.Counts { return p.schema.Counts() }
