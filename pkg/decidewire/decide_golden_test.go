package decidewire

// TestDecideRecordsGolden is the golden test of the decide definitions
// (schemas/decide): every table the area declares round-trips as binary wire,
// as padded standard base64 of that wire and as the JSON text form, each read
// strictly through pkg/schemaread (S1, S2). Usage and RawTokens, whose every
// bound the code states, are FIXED tables read and written by value; every
// other table holds text or a list the code does not bound, so it is a VARIABLE
// table held in an arena, with unbounded text (*string) and unbounded lists.
// A value past each bound the code enforces, and a damaged image of each form,
// is refused by name and never clamped; a text, a list or an id line far past
// any size a fixed table could hold round-trips whole; the reader counts each
// read on the form it arrived in; and the unit's protocol id, the hash of its
// wire shape, is pinned.
//
// The facade here is the strict read and write over the generated functions.
// The generated writer checks no range and the generated reader clamps and
// counts: the facade refuses every clamp and states each rule the Go code
// enforces today once, for the reader and the writer (UNION.md section 4.6).
// The rules and their sources are the comments of schemas/decide/decide.schema.

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/cardcost"
	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const decideHelp = "nova-tools decide -h"

// chk states the facade's rules over a schemaread.Checker. ar is the arena a
// read of the table's text and lists passes: before the arena is locked a
// reference is an arena offset; after it, in the packed region, a reference is
// relative to its own slot and no arena is passed.
type chk struct {
	c  *schemaread.Checker
	ar []*TableArena
}

func arenaOf(a *TableArena) []*TableArena {
	if a.Locked {
		return nil
	}
	return []*TableArena{a}
}

// maxText is the largest text the area holds: the record reader's 64 MiB line
// (record.go:97) is the only size any of the code states.
const maxText = 64 << 20

// s is the text a string slot holds.
func (k chk) s(slot *TableRef) string { return string(TableStringAt(slot, k.ar...).Data) }

// text holds a text to UTF-8 with no NUL (S2: refused by name).
func (k chk) text(field string, slot *TableRef) { k.c.Text(field, k.s(slot), maxText) }

// must is text the code refuses when empty.
func (k chk) must(field string, slot *TableRef) {
	k.text(field, slot)
	k.c.Required(field, k.s(slot) != "")
}

// span holds a value to [lo, hi].
func (k chk) span(field string, v, lo, hi int64) { k.c.Bound(field, v, lo, hi) }

// prob holds a probability to [0, 1]: decide.go:163 Check and record.go:169
// recordedProbabilities refuse a probability outside it or not a number. The
// Checker has no float bound, so an out-of-range value is a clamp and NaN is
// malformed.
func (k chk) prob(field string, p float64) {
	if math.IsNaN(p) {
		k.c.Text(field, "\x00", 1)
		return
	}
	in := int64(0)
	if p >= 0 && p <= 1 {
		in = 1
	}
	k.c.Bound(field, in, 1, 1)
}

// is refuses a text that is not the one value the code accepts (raw.go:51).
func (k chk) is(field string, slot *TableRef, want string) {
	k.text(field, slot)
	if k.s(slot) != want {
		k.c.Text(field, "\x00", 1)
	}
}

// bare refuses a text holding whitespace: ParseDecided splits on it
// (attempt.go:170).
func (k chk) bare(field string, slot *TableRef) {
	if strings.ContainsAny(k.s(slot), " \t\r\n") {
		k.c.Text(field, "\x00", 1)
	}
}

// notBlank is text the code refuses when blank: decide.go:102 and :110 trim before
// the test.
func (k chk) notBlank(field string, slot *TableRef) {
	k.text(field, slot)
	k.c.Required(field, strings.TrimSpace(k.s(slot)) != "")
}

// distinct refuses a name that repeats: the code holds these lists as maps
// (decide.go Questions and Criteria, backend.go Table, FixedAnswer.P), where a
// key is one entry.
func (k chk) distinct(field string, seen map[string]bool, name string) {
	if seen[name] {
		k.c.Text(field, "\x00", 1)
	}
	seen[name] = true
}

// optionalProb holds a probability that is optional: when present it is the
// probability of yes (decide.go:163 Check), so it lies in [0, 1].
func (k chk) optionalProb(field string, present bool, p float64) {
	if present {
		k.prob(field, p)
	}
}

// ---- the rules, one function a table ----

func usageRules(v *Usage, k chk) {}

func optionPRules(v *OptionP, k chk) {
	k.must("option", &v.Option)
	k.prob("p", v.P)
}

// answerRules: the answer's kind is choice or noul, its options an open-ended
// list (decide.go:114 wants two, Check sets no ceiling), each probability in
// [0, 1] (decide.go:163).
func answerRules(v *Answer, k chk) {
	k.span("kind", int64(v.Kind), 1, 2)
	k.must("value", &v.Value)
	AnswerPEach(&v.P, func(o *OptionP) bool { optionPRules(o, k); return true }, k.ar...)
	k.text("method", &v.Method)
}

func namedAnswerRules(v *NamedAnswer, k chk) {
	k.must("question", &v.Question)
	answerRules(&v.Answer, k)
}

func inputRules(v *Input, k chk) {
	k.must("key", &v.Key)
	k.text("value", &v.Value)
}

func decidedRules(v *Decided, k chk) {
	k.must("value", &v.Value)
	k.bare("value", &v.Value)
	k.prob("p", v.P)
	k.must("op", &v.Op)
	k.bare("op", &v.Op)
}

func decisionRules(v *Decision, k chk) {
	k.must("id", &v.Id)
	k.must("decision_name", &v.DecisionName)
	k.text("schema", &v.Schema)
	k.text("backend", &v.Backend)
	k.text("at", &v.At)
	DecisionInputsEach(&v.Inputs, func(in *Input) bool { inputRules(in, k); return true }, k.ar...)
	k.text("state", &v.State)
	DecisionAnswersEach(&v.Answers, func(n *NamedAnswer) bool { namedAnswerRules(n, k); return true }, k.ar...)
	usageRules(&v.Usage, k)
}

func outcomeRules(v *Outcome, k chk) {
	k.must("id", &v.Id)
	k.must("label", &v.Label)
	k.text("note", &v.Note)
	k.text("at", &v.At)
}

func actRules(v *Act, k chk) {
	k.must("id", &v.Id)
	k.must("step", &v.Step)
	k.text("op", &v.Op)
	k.text("at", &v.At)
}

func (l LineKind) name() string {
	switch l {
	case LineKindDecision:
		return "decision"
	case LineKindOutcome:
		return "outcome"
	case LineKindAct:
		return "act"
	}
	return "none"
}

// recordLineRules: a line is one record (record.go:70 parse), the one kind names.
func recordLineRules(v *RecordLine, k chk) {
	k.span("kind", int64(v.Kind), 1, 3)
	d, o, a := DecisionAt(&v.AsDecision, k.ar...), OutcomeAt(&v.AsOutcome, k.ar...), ActAt(&v.AsAct, k.ar...)
	present := map[LineKind]bool{LineKindDecision: d != nil, LineKindOutcome: o != nil, LineKindAct: a != nil}
	k.c.Required("as_"+v.Kind.name(), present[v.Kind])
	for kind, p := range present {
		if p && kind != v.Kind {
			k.c.Text("as_"+kind.name(), "\x00", 1)
		}
	}
	if d != nil {
		decisionRules(d, k)
	}
	if o != nil {
		outcomeRules(o, k)
	}
	if a != nil {
		actRules(a, k)
	}
}

func judgmentIdRules(v *JudgmentId, k chk) { k.must("id", &v.Id) }

// importLogLineRules: the code takes every id of an export line
// (import.go:161-176), so the list has no count bound.
func importLogLineRules(v *ImportLogLine, k chk) {
	k.text("verb", &v.Verb)
	ImportLogLineAnswersEach(&v.Answers, func(j *JudgmentId) bool { judgmentIdRules(j, k); return true }, k.ar...)
}

// criterionRules: Criteria is a map (decide.go Question); Problems states no rule
// of an option or its meaning.
func criterionRules(v *Criterion, k chk) {
	k.text("option", &v.Option)
	k.text("meaning", &v.Meaning)
}

// questionRules are decide.go:101 Problems for one question: the type is choice or
// noul, the instructions are not blank, a choice names at least two options in
// criteria and a noul none. A criteria list holds each option once, as the map does.
func questionRules(v *Question, k chk) {
	k.span("kind", int64(v.Kind), 1, 2)
	k.notBlank("instructions", &v.Instructions)
	seen := map[string]bool{}
	QuestionCriteriaEach(&v.Criteria, func(c *Criterion) bool {
		criterionRules(c, k)
		k.distinct("option", seen, string(TableStringAt(&c.Option, k.ar...).Data))
		return true
	}, k.ar...)
	switch v.Kind {
	case AnswerTypeChoice:
		k.span("criteria", int64(v.Criteria.Count), 2, math.MaxInt32)
	case AnswerTypeNoul:
		k.span("criteria", int64(v.Criteria.Count), 0, 0)
	}
}

// namedQuestionRules: the name is the map key; Problems states no rule of it.
func namedQuestionRules(v *NamedQuestion, k chk) {
	k.text("name", &v.Name)
	questionRules(&v.Question, k)
}

// namedQuestions holds a list of questions as the map of a Schema: each name once.
func namedQuestions(k chk, each func(visit func(*NamedQuestion) bool) bool) {
	seen := map[string]bool{}
	each(func(n *NamedQuestion) bool {
		namedQuestionRules(n, k)
		k.distinct("name", seen, string(TableStringAt(&n.Name, k.ar...).Data))
		return true
	})
}

// decideSchemaRules: decide.go:101 Problems wants a name that is not blank and at
// least one question.
func decideSchemaRules(v *DecideSchema, k chk) {
	k.notBlank("name", &v.Name)
	k.c.Required("questions", v.Questions.Count > 0)
	namedQuestions(k, func(visit func(*NamedQuestion) bool) bool {
		return DecideSchemaQuestionsEach(&v.Questions, visit, k.ar...)
	})
}

// jevRequestRules: Ask (decide.go:196) refuses an empty state; the questions are a
// schema's.
func jevRequestRules(v *JevRequest, k chk) {
	k.notBlank("state", &v.State)
	k.text("model", &v.Model)
	namedQuestions(k, func(visit func(*NamedQuestion) bool) bool {
		return JevRequestQuestionsEach(&v.Questions, visit, k.ar...)
	})
}

// jevAnswerRules: the wire holds what the backend sent. jevAnswers (jev.go:63)
// reads an unknown type and a missing number as errors of its own, so the table
// holds both; a probability is held to [0, 1] by Check once read (decide.go:163).
func jevAnswerRules(v *JevAnswer, k chk) {
	k.text("kind", &v.Kind)
	k.text("choice", &v.Choice)
	JevAnswerProbabilitiesEach(&v.Probabilities, func(o *OptionP) bool { optionPRules(o, k); return true }, k.ar...)
	k.optionalProb("noul", v.NoulPresent, v.Noul)
}

func namedJevAnswerRules(v *NamedJevAnswer, k chk) {
	k.text("question", &v.Question)
	jevAnswerRules(&v.Answer, k)
}

func jevResponseRules(v *JevResponse, k chk) {
	JevResponseAnswersEach(&v.Answers, func(n *NamedJevAnswer) bool { namedJevAnswerRules(n, k); return true }, k.ar...)
	usageRules(&v.Usage, k)
}

// fixedAnswerRules: ParseFixed (backend.go:30) bounds nothing of a row; Check holds
// its probabilities to [0, 1] when it is asked.
func fixedAnswerRules(v *FixedAnswer, k chk) {
	k.text("choice", &v.Choice)
	FixedAnswerPEach(&v.P, func(o *OptionP) bool { optionPRules(o, k); return true }, k.ar...)
	k.optionalProb("noul", v.NoulPresent, v.Noul)
}

func namedFixedAnswerRules(v *NamedFixedAnswer, k chk) {
	k.text("question", &v.Question)
	fixedAnswerRules(&v.Answer, k)
}

func fixedTableRules(v *FixedTable, k chk) {
	FixedTableAnswersEach(&v.Answers, func(n *NamedFixedAnswer) bool { namedFixedAnswerRules(n, k); return true }, k.ar...)
}

// exampleRules: ParseExamples (gradefewshot.go:62) wants a card, a heading and a
// label of the three classes.
func exampleRules(v *Example, k chk) {
	k.must("card", &v.Card)
	k.must("heading", &v.Heading)
	k.text("paths", &v.Paths)
	k.text("kind", &v.Kind)
	k.span("label", int64(v.Label), 1, 3)
}

func setEntryRules(v *SetEntry, k chk) {
	k.text("key", &v.Key)
	k.text("value", &v.Value)
}

// logLineRules: ReadLog (gradescore.go:56) bounds nothing of a line.
func logLineRules(v *LogLine, k chk) {
	k.text("card", &v.Card)
	k.text("primary", &v.Primary)
	k.text("table_name", &v.TableName)
	k.text("to", &v.To)
	LogLineSetEach(&v.Set, func(e *SetEntry) bool { setEntryRules(e, k); return true }, k.ar...)
}

func logExportRules(v *LogExport, k chk) {
	LogExportLinesEach(&v.Lines, func(l *LogLine) bool { logLineRules(l, k); return true }, k.ar...)
}

// importExportRules: the code takes every line of the export (import.go:167).
func importExportRules(v *ImportExport, k chk) {
	ImportExportLinesEach(&v.Lines, func(l *ImportLogLine) bool { importLogLineRules(l, k); return true }, k.ar...)
}

// barsRules: ParseBars (firstread.go:37) holds each bar to a probability in [0, 1]
// and the review bar to at most the bounce bar.
func barsRules(v *Bars, k chk) {
	k.prob("bounce", v.Bounce)
	k.prob("review", v.Review)
	if !math.IsNaN(v.Bounce) && !math.IsNaN(v.Review) {
		k.c.Bound("review", boolBit(v.Review <= v.Bounce), 1, 1)
	}
}

// boolBit is 1 for true: the Checker bounds an integer.
func boolBit(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// gateBarUnset is the gate bar the sprint row leaves empty (gate.go Unset).
const gateBarUnset = 2.0

// gateBarRules: a bar is a probability in [0, 1] or Unset (gate.go:296 Unset,
// ParseGateBars gate.go:300); two set bars sum above 1.
func gateBarRules(field string, v float64, k chk) {
	if v == gateBarUnset {
		return
	}
	k.prob(field, v)
}

func gateBarsRules(v *GateBars, k chk) {
	gateBarRules("flaky", v.Flaky, k)
	gateBarRules("pre_existing", v.PreExisting, k)
	if v.Flaky <= 1 && v.PreExisting <= 1 {
		k.c.Bound("flaky", boolBit(v.Flaky+v.PreExisting > 1), 1, 1)
	}
}

func gateLineRules(v *GateLine, k chk) { k.text("text", &v.Text) }

// failureRules: the struct bounds no length or count (gate.go:66); the text is
// held to UTF-8.
func failureRules(v *Failure, k chk) {
	k.text("pkg", &v.Pkg)
	k.text("test", &v.Test)
	FailureLinesEach(&v.Lines, func(l *GateLine) bool { gateLineRules(l, k); return true }, k.ar...)
}

// chosenRules: Choose (judgment.go:194) writes the struct and states no bound.
func chosenRules(v *Chosen, k chk) {
	k.text("verb", &v.Verb)
	k.text("fix", &v.Fix)
	k.text("reason", &v.Reason)
	k.text("act", &v.Act)
	k.text("why", &v.Why)
}

func clusterCardRules(v *ClusterCard, k chk) { k.text("card", &v.Card) }

// clusterRules: the struct bounds neither the count nor the cards (score.go:127).
func clusterRules(v *Cluster, k chk) {
	k.text("class_name", &v.ClassName)
	ClusterCardsEach(&v.Cards, func(c *ClusterCard) bool { clusterCardRules(c, k); return true }, k.ar...)
}

// rawTokensRules: a count is a non-negative int64 (raw.go:55).
func rawTokensRules(v *RawTokens, k chk) {
	for _, c := range []struct {
		field   string
		n       int64
		present bool
	}{
		{"input_tokens", v.InputTokens, v.InputTokensPresent}, {"output_tokens", v.OutputTokens, v.OutputTokensPresent},
		{"cached_tokens", v.CachedTokens, v.CachedTokensPresent}, {"cache_write_tokens", v.CacheWriteTokens, v.CacheWriteTokensPresent},
		{"cache_read_tokens", v.CacheReadTokens, v.CacheReadTokensPresent}, {"reasoning_tokens", v.ReasoningTokens, v.ReasoningTokensPresent},
	} {
		if c.present {
			k.span(c.field, c.n, 0, math.MaxInt64)
		}
	}
}

// rawRecordRules are raw.go:51's provenance rules.
func rawRecordRules(v *RawRecord, k chk) {
	k.is("harness", &v.Harness, "muse")
	k.is("execution", &v.Execution, "fleet")
	k.is("source_schema", &v.SourceSchema, "muse-jsonl-v1")
	k.must("producer_version", &v.ProducerVersion)
	k.span("billing", int64(v.Billing), 1, 3)
	rawTokensRules(&v.Tokens, k)
}

// rawUsageRules: version 1 and at least one record (raw.go:47). The code
// bounds the envelope by bytes (MaxRawBytes, raw.go:21, enforced at raw.go:72
// and raw.go:80), never by record count, so the list has no count bound here;
// the byte rule is tested through cardcost at its boundary.
func rawUsageRules(v *RawUsage, k chk) {
	k.span("version", int64(v.Version), 1, 1)
	k.c.Required("records", v.Records.Count > 0)
	RawUsageRecordsEach(&v.Records, func(r *RawRecord) bool { rawRecordRules(r, k); return true }, k.ar...)
}

func stagePathRules(v *StagePath, k chk) { k.must("path", &v.Path) }

// frameRules: ReadFrame (contract.go:142) refuses a kind that is not work or
// read and an empty card; it bounds no length, count or number.
func frameRules(v *Frame, k chk) {
	k.span("kind", int64(v.Kind), 1, 2)
	k.must("card", &v.Card)
	for _, f := range []struct {
		field string
		slot  *TableRef
	}{
		{"tier", &v.Tier}, {"model", &v.Model}, {"repo", &v.Repo}, {"base_ref", &v.BaseRef}, {"stage_sha", &v.StageSha},
		{"branch", &v.Branch}, {"prev_head", &v.PrevHead}, {"why", &v.Why}, {"finding", &v.Finding}, {"fix", &v.Fix},
		{"review_base", &v.ReviewBase}, {"recipes", &v.Recipes}, {"decide_bounce", &v.DecideBounce}, {"decide_review", &v.DecideReview},
		{"decide_gate_flaky", &v.DecideGateFlaky}, {"decide_gate_preexisting", &v.DecideGatePreexisting},
	} {
		k.text(f.field, f.slot)
	}
	FrameStageEach(&v.Stage, func(p *StagePath) bool { stagePathRules(p, k); return true }, k.ar...)
}

// varFns are the generated functions of one VARIABLE table and the rules its
// facade holds. The value a codec carries is its builder, which owns the arena
// the table's lists, blobs and pointers live in.
type varFns[R any, B any, PB interface {
	*B
	Init(...TableAllocator) bool
	GetRoot() *R
	AsConst() *R
	Lock() bool
	Shutdown()
}] struct {
	name          string
	load          func(PB, []byte, *TableReport) bool
	fromJSON      func(PB, []byte, *TableReport) bool
	measureReason func([]byte) (int64, error)
	measure       func(*R, ...TableWriteContext) int64
	save          func(*R, []byte, ...TableWriteContext) int64
	jsonMeasure   func(*R, ...TableAllocator) int64
	toJSON        func(*R, []byte, ...TableAllocator) int64
	arena         func(PB) *TableArena
	rules         func(PB, *R, chk)
}

func (f varFns[R, B, PB]) fresh() PB {
	p := PB(new(B))
	if !p.Init() {
		panic("builder")
	}
	return p
}

// root is the table's root. A table holding lists, blobs or pointers is
// written from a locked arena, which packs it into one region the writer's
// self-relative references resolve in; a locked builder answers AsConst.
func (f varFns[R, B, PB]) root(b PB) *R {
	if r := b.GetRoot(); r != nil {
		return r
	}
	return b.AsConst()
}

// locked locks the arena of a builder that has not been, and answers its root.
func (f varFns[R, B, PB]) locked(b PB) *R {
	if b.GetRoot() != nil {
		b.Lock()
	}
	return f.root(b)
}

func (f varFns[R, B, PB]) viaBuilder(v *PB, do func(PB, *TableReport) bool) schemaread.Report {
	var rep TableReport
	ok := do(*v, &rep)
	out := schemaread.Report{Unknown: int(rep.Unknown), KindMismatch: int(rep.KindMismatch), Clamped: int(rep.Clamped), Malformed: rep.Malformed || !ok}
	if rep.Verdict == TableOpenRefused || rep.Verdict == TableOpenDamaged {
		out.Reason = rep.Err().Error()
	}
	return out
}

func (f varFns[R, B, PB]) codec() *schemaread.Codec[PB] {
	return &schemaread.Codec[PB]{
		Name:    strings.ToUpper(f.name),
		Help:    decideHelp,
		Reset:   func(v *PB) { *v = f.fresh() },
		Measure: f.measureWire(),
		LoadWire: func(v *PB, wire []byte) schemaread.Report {
			return f.viaBuilder(v, func(b PB, rep *TableReport) bool { return f.load(b, wire, rep) })
		},
		LoadJSON: func(v *PB, text []byte) schemaread.Report {
			return f.viaBuilder(v, func(b PB, rep *TableReport) bool { return f.fromJSON(b, text, rep) })
		},
		Constraints: func(v *PB, c *schemaread.Checker) {
			var ar []*TableArena
			if f.arena != nil {
				ar = arenaOf(f.arena(*v))
			}
			f.rules(*v, f.root(*v), chk{c, ar})
		},
		SaveWire: func(v *PB) []byte {
			root := f.locked(*v)
			n := f.measure(root)
			if n < 0 {
				return nil
			}
			buf := make([]byte, n)
			if f.save(root, buf) != n {
				return nil
			}
			return buf
		},
		SaveJSON: func(v *PB) []byte {
			root := f.locked(*v)
			n := f.jsonMeasure(root)
			if n < 0 {
				return nil
			}
			buf := make([]byte, n)
			if f.toJSON(root, buf) != n {
				return nil
			}
			return buf
		},
	}
}

// vbound is a value past one declared bound of a variable table.
type vbound[R any, PB any] struct {
	name, field, reason string
	mutate              func(PB, *R)
}

// exerciseVar round-trips a variable table in all three forms, refuses each
// bound and damage by name, and counts each read on its form.
func exerciseVar[R any, B any, PB interface {
	*B
	Init(...TableAllocator) bool
	GetRoot() *R
	AsConst() *R
	Lock() bool
	Shutdown()
}](t *testing.T, f varFns[R, B, PB], build func(PB, *R), bounds []vbound[R, PB]) {
	t.Helper()
	r := schemaread.NewReader(f.codec())
	good := func() PB {
		b := f.fresh()
		build(b, b.GetRoot())
		return b
	}

	t.Run("round trips in all three forms", func(t *testing.T) {
		g := good()
		want, err := r.Write(&g, schemaread.FormBinary)
		require.NoError(t, err)
		wantJSON, err := r.Write(&g, schemaread.FormJSON)
		require.NoError(t, err)
		for _, form := range []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON} {
			image, err := r.Write(&g, form)
			require.NoError(t, err, form.String())
			got, res, err := r.Read(image)
			require.NoError(t, err, form.String())
			assert.Equal(t, form, res.Form)
			again, err := r.Write(&got, schemaread.FormBinary)
			require.NoError(t, err)
			assert.Equal(t, want, again, "%s through %s", f.name, form)
			againJSON, err := r.Write(&got, schemaread.FormJSON)
			require.NoError(t, err)
			assert.Equal(t, wantJSON, againJSON, "%s through %s, as JSON", f.name, form)
		}
		for _, form := range []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON} {
			assert.EqualValues(t, 1, r.Counts().Reads(form), form.String())
		}
		text, err := r.Write(&g, schemaread.FormBase64)
		require.NoError(t, err)
		assert.Equal(t, base64.StdEncoding.EncodeToString(want), string(text), "text is padded standard base64 of the wire")
		assert.Equal(t, byte(1), want[0], "a variable table's wire opens with form byte 1")
		assert.True(t, bytes.HasPrefix(wantJSON, []byte("{")))
	})

	t.Run("a value past a bound is refused by name", func(t *testing.T) {
		for _, b := range bounds {
			g := good()
			b.mutate(g, g.GetRoot())
			_, err := r.Write(&g, schemaread.FormBinary)
			var ref *schemaread.Refusal
			require.ErrorAs(t, err, &ref, b.name)
			assert.Equal(t, b.reason, ref.Reason, b.name)
			assert.Equal(t, b.field, ref.Field, b.name)
			// The generated writer checks no range: the same value written
			// as it stands is refused when read back, never clamped.
			if wire := (f.codec()).SaveWire(&g); wire != nil {
				_, _, rerr := r.Read(wire)
				require.ErrorAs(t, rerr, &ref, b.name+" read back")
				assert.Contains(t, []string{b.reason, schemaread.ReasonMalformed}, ref.Reason, b.name+" read back")
			}
		}
	})

	t.Run("a damaged image is refused by name", func(t *testing.T) {
		g := good()
		wire, err := r.Write(&g, schemaread.FormBinary)
		require.NoError(t, err)
		jsonText, err := r.Write(&g, schemaread.FormJSON)
		require.NoError(t, err)
		patched := bytes.Clone(wire)
		patched[0] = 3
		trailer := bytes.Clone(wire)
		trailer[len(trailer)-1] ^= 0x7f // the id count at the tail
		for name, d := range map[string]struct {
			image  []byte
			reason string
		}{
			"truncated one byte":     {wire[:len(wire)-1], ""},
			"truncated to the form":  {wire[:1], ""},
			"the fixed form byte":    {patched, "unknown_form"},
			"the trailer damaged":    {trailer, ""},
			"JSON cut short":         {jsonText[:len(jsonText)-2], ""},
			"JSON that is not JSON":  {[]byte("{not json"), ""},
			"JSON of the wrong kind": {[]byte(`[1,2,3]`), "unknown_form"},
		} {
			for _, in := range [][]byte{d.image, []byte(base64.StdEncoding.EncodeToString(d.image))} {
				if d.image[0] == '{' && &in[0] != &d.image[0] {
					continue // a JSON text is not wrapped in base64
				}
				_, _, err := r.Read(in)
				var ref *schemaread.Refusal
				require.ErrorAs(t, err, &ref, name)
				assert.NotEmpty(t, ref.Reason, name)
				if d.reason != "" {
					assert.Equal(t, d.reason, ref.Reason, name)
				}
			}
		}
		text := base64.StdEncoding.EncodeToString(wire)
		_, _, err = r.Read([]byte(text[:8] + "!" + text[9:]))
		var ref *schemaread.Refusal
		require.ErrorAs(t, err, &ref)
		assert.Equal(t, schemaread.ReasonMalformed, ref.Reason)
		_, _, err = r.Read(nil)
		require.ErrorAs(t, err, &ref)
		assert.Equal(t, schemaread.ReasonUnknownForm, ref.Reason)
	})
}

// wholeIn fills a table, writes it in every form and reads each back: the image
// written again is the first image, and check sees the table read from each form.
func wholeIn[R any, B any, PB interface {
	*B
	Init(...TableAllocator) bool
	GetRoot() *R
	AsConst() *R
	Lock() bool
	Shutdown()
}](t *testing.T, f varFns[R, B, PB], what string, fill func(PB, *R), check func(*testing.T, PB, *R)) {
	t.Helper()
	rd := schemaread.NewReader(f.codec())
	w := f.fresh()
	fill(w, w.GetRoot())
	want, err := rd.Write(&w, schemaread.FormBinary)
	require.NoError(t, err, what)
	for _, form := range []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON} {
		image, err := rd.Write(&w, form)
		require.NoError(t, err, "%s as %s", what, form)
		got, _, err := rd.Read(image)
		require.NoError(t, err, "%s as %s", what, form)
		check(t, got, f.root(got))
		again, err := rd.Write(&got, schemaread.FormBinary)
		require.NoError(t, err, "%s as %s", what, form)
		assert.Equal(t, want, again, "%s as %s", what, form)
	}
}

// eraseCriteria erases the last n criteria of q, whose arena is a, leaving the list
// as the code would hold it with fewer options.
func eraseCriteria(a *TableArena, q *Question, n int) {
	for ; n > 0; n-- {
		QuestionCriteriaErase(a, &q.Criteria, q.Criteria.At(q.Criteria.Count-1, a))
	}
}

// strAt is the text of a slot in the arena a table was read into.
func strAt(slot *TableRef, a *TableArena) string { return string(TableStringAt(slot, a).Data) }

// optionalFloats checks that a present zero and an absent number differ in every
// form (jev.go: a missing number is an error, never a 0).
func optionalFloats[R any, B any, PB interface {
	*B
	Init(...TableAllocator) bool
	GetRoot() *R
	AsConst() *R
	Lock() bool
	Shutdown()
}](t *testing.T, f varFns[R, B, PB], fill func(PB, *R), set func(*R, bool) *bool) {
	t.Helper()
	rd := schemaread.NewReader(f.codec())
	for _, form := range []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON} {
		for _, present := range []bool{false, true} {
			w := f.fresh()
			fill(w, w.GetRoot())
			set(w.GetRoot(), present)
			image, err := rd.Write(&w, form)
			require.NoError(t, err)
			got, _, err := rd.Read(image)
			require.NoError(t, err)
			assert.Equal(t, present, *set(f.root(got), present), "%s through %s", f.name, form)
		}
	}
}

// ---- samples ----

// setS stores text in a string slot of the worker's arena.
func setS(w *TableWorker, slot *TableRef, text string) {
	if text != "" {
		TableStringEmplace(w, slot, []byte(text))
	}
}

func fillOptionP(w *TableWorker, r *OptionP, option string, p float64) {
	setS(w, &r.Option, option)
	r.P = p
}

// fillAnswer fills a choice with options options.
func fillAnswer(w *TableWorker, r *Answer, options int) {
	r.Kind = AnswerTypeChoice
	setS(w, &r.Value, "opt-0")
	for i := 0; i < options; i++ {
		fillOptionP(w, AnswerPAdd(w, &r.P), fmt.Sprintf("opt-%d", i), 1/float64(options))
	}
	r.Confidence = 0.5
	setS(w, &r.Method, "coordinator")
}

func fillNamedAnswer(w *TableWorker, r *NamedAnswer, options int) {
	setS(w, &r.Question, "class")
	fillAnswer(w, &r.Answer, options)
}

func fillInput(w *TableWorker, r *Input, i int) {
	setS(w, &r.Key, fmt.Sprintf("key-%d", i))
	setS(w, &r.Value, "gate green; pushed the head")
}

func sampleUsage() Usage { return Usage{InputTokens: 1200, OutputTokens: 85} }

func fillDecided(w *TableWorker, r *Decided) {
	setS(w, &r.Value, "done")
	r.P = 0.931
	setS(w, &r.Op, "a13-defs-decide@1.0123456789ab")
}

// fillDecision fills a decision with inputs inputs, answers answers of options
// options each, and a state of stateBytes bytes.
func fillDecision(w *TableWorker, r *Decision, inputs, answers, options, stateBytes int) {
	setS(w, &r.Id, "a13-defs-decide@1.0123456789ab")
	setS(w, &r.DecisionName, "attempt")
	setS(w, &r.Schema, "0123456789abcdef")
	setS(w, &r.Backend, "jev")
	setS(w, &r.At, "2026-10-17T04:07:16Z")
	for i := 0; i < inputs; i++ {
		fillInput(w, DecisionInputsAdd(w, &r.Inputs), i)
	}
	setS(w, &r.State, string(bytes.Repeat([]byte("state "), stateBytes/6+1)[:stateBytes]))
	for i := 0; i < answers; i++ {
		fillNamedAnswer(w, DecisionAnswersAdd(w, &r.Answers), options)
	}
	r.Usage = sampleUsage()
}

func fillOutcome(w *TableWorker, r *Outcome) {
	setS(w, &r.Id, "a13-defs-decide@1.0123456789ab")
	setS(w, &r.Label, "landed")
	setS(w, &r.Note, "/work/VERDICT.md")
	setS(w, &r.At, "2026-10-17T04:09:00Z")
}

func fillAct(w *TableWorker, r *Act) {
	setS(w, &r.Id, "a13-defs-decide@1.0123456789ab")
	setS(w, &r.Step, "applying")
	setS(w, &r.Op, "answer-0042")
	setS(w, &r.At, "2026-10-17T04:09:30Z")
}

// fillImportLogLine fills an export line answering ids judgments.
func fillImportLogLine(w *TableWorker, r *ImportLogLine, ids int) {
	setS(w, &r.Verb, "rework")
	for i := 0; i < ids; i++ {
		setS(w, &ImportLogLineAnswersAdd(w, &r.Answers).Id, fmt.Sprintf("judgment-%04d", i))
	}
}

// longText is unit repeated to exactly n bytes.
func longText(unit string, n int) string {
	return string(bytes.Repeat([]byte(unit), n/len(unit)+1)[:n])
}

func fillCriterion(w *TableWorker, r *Criterion, i int) {
	setS(w, &r.Option, fmt.Sprintf("option-%d", i))
	setS(w, &r.Meaning, "it fails by chance and not by the change: a timing bound, a race")
}

// fillQuestion fills a question of kind with options criteria and instructions of
// instrBytes.
func fillQuestion(w *TableWorker, r *Question, kind AnswerType, options, instrBytes int) {
	r.Kind = kind
	setS(w, &r.Instructions, longText("Why the failure failed. ", instrBytes))
	for i := 0; i < options; i++ {
		fillCriterion(w, QuestionCriteriaAdd(w, &r.Criteria), i)
	}
}

func fillNamedQuestion(w *TableWorker, r *NamedQuestion, name string, kind AnswerType, options, instrBytes int) {
	setS(w, &r.Name, name)
	fillQuestion(w, &r.Question, kind, options, instrBytes)
}

// fillDecideSchema fills a schema of choices choice questions of options criteria
// each, then one noul, every instruction instrBytes.
func fillDecideSchema(w *TableWorker, r *DecideSchema, choices, options, instrBytes int) {
	setS(w, &r.Name, "gate-class")
	for i := 0; i < choices; i++ {
		fillNamedQuestion(w, DecideSchemaQuestionsAdd(w, &r.Questions), fmt.Sprintf("choice-%d", i), AnswerTypeChoice, options, instrBytes)
	}
	fillNamedQuestion(w, DecideSchemaQuestionsAdd(w, &r.Questions), "noul-0", AnswerTypeNoul, 0, instrBytes)
}

func fillJevRequest(w *TableWorker, r *JevRequest, choices, options, stateBytes int) {
	setS(w, &r.State, longText("state ", stateBytes))
	setS(w, &r.Model, "jev-1")
	for i := 0; i < choices; i++ {
		fillNamedQuestion(w, JevRequestQuestionsAdd(w, &r.Questions), fmt.Sprintf("choice-%d", i), AnswerTypeChoice, options, 80)
	}
	fillNamedQuestion(w, JevRequestQuestionsAdd(w, &r.Questions), "noul-0", AnswerTypeNoul, 0, 80)
}

// fillJevAnswer fills a choice answer with probs probabilities, a confidence and a
// noul both present.
func fillJevAnswer(w *TableWorker, r *JevAnswer, probs int) {
	setS(w, &r.Kind, "choice")
	setS(w, &r.Choice, "opt-0")
	for i := 0; i < probs; i++ {
		fillOptionP(w, JevAnswerProbabilitiesAdd(w, &r.Probabilities), fmt.Sprintf("opt-%d", i), 1/float64(probs))
	}
	r.Confidence, r.ConfidencePresent = 0.25, true
	r.Noul, r.NoulPresent = 0.75, true
}

func fillNamedJevAnswer(w *TableWorker, r *NamedJevAnswer, name string, probs int) {
	setS(w, &r.Question, name)
	fillJevAnswer(w, &r.Answer, probs)
}

func fillJevResponse(w *TableWorker, r *JevResponse, answers, probs int) {
	for i := 0; i < answers; i++ {
		fillNamedJevAnswer(w, JevResponseAnswersAdd(w, &r.Answers), fmt.Sprintf("question-%d", i), probs)
	}
	r.Usage = sampleUsage()
}

func fillFixedAnswer(w *TableWorker, r *FixedAnswer, options int) {
	setS(w, &r.Choice, "opt-0")
	for i := 0; i < options; i++ {
		fillOptionP(w, FixedAnswerPAdd(w, &r.P), fmt.Sprintf("opt-%d", i), 1/float64(options))
	}
	r.Noul, r.NoulPresent = 0.5, true
}

func fillNamedFixedAnswer(w *TableWorker, r *NamedFixedAnswer, options int) {
	setS(w, &r.Question, "class")
	fillFixedAnswer(w, &r.Answer, options)
}

func fillFixedTable(w *TableWorker, r *FixedTable, answers, options int) {
	for i := 0; i < answers; i++ {
		fillNamedFixedAnswer(w, FixedTableAnswersAdd(w, &r.Answers), options)
	}
}

// fillExample fills an example whose paths line is pathsBytes.
func fillExample(w *TableWorker, r *Example, pathsBytes int) {
	setS(w, &r.Card, "a13-defs-decide")
	setS(w, &r.Heading, "A13 the decide definitions")
	setS(w, &r.Paths, longText("pkg/decide/decide.go ", pathsBytes))
	setS(w, &r.Kind, "work")
	r.Label = ExampleLabelPro
}

func fillSetEntry(w *TableWorker, r *SetEntry, i int, valueBytes int) {
	setS(w, &r.Key, fmt.Sprintf("cost_record.%d", i))
	setS(w, &r.Value, longText("attempt=1 tier=pro end=landed ", valueBytes))
}

// fillLogLine fills a log line with sets set entries of valueBytes each.
func fillLogLine(w *TableWorker, r *LogLine, sets, valueBytes int) {
	setS(w, &r.Card, "a13-defs-decide")
	setS(w, &r.Primary, "a13-defs-decide")
	setS(w, &r.TableName, "work")
	setS(w, &r.To, "working")
	r.Removed = true
	for i := 0; i < sets; i++ {
		fillSetEntry(w, LogLineSetAdd(w, &r.Set), i, valueBytes)
	}
}

func fillLogExport(w *TableWorker, r *LogExport, lines, sets, valueBytes int) {
	for i := 0; i < lines; i++ {
		fillLogLine(w, LogExportLinesAdd(w, &r.Lines), sets, valueBytes)
	}
}

// fillImportExport fills an export of lines lines answering ids judgments each, the
// last line's verb verbBytes long.
func fillImportExport(w *TableWorker, r *ImportExport, lines, ids, verbBytes int) {
	for i := 0; i < lines; i++ {
		l := ImportExportLinesAdd(w, &r.Lines)
		fillImportLogLine(w, l, ids)
		if i == lines-1 && verbBytes > 0 {
			setS(w, &l.Verb, longText("rework ", verbBytes))
		}
	}
}

func fillGateLine(w *TableWorker, r *GateLine, i int, bytes int) {
	setS(w, &r.Text, longText(fmt.Sprintf("--- FAIL: line %d ", i), bytes))
}

// fillFailure fills a failing test with lines printed lines of lineBytes each.
func fillFailure(w *TableWorker, r *Failure, lines, lineBytes int) {
	setS(w, &r.Pkg, "github.com/mas-bandwidth/nova-tools/pkg/decide")
	setS(w, &r.Test, "TestGate")
	for i := 0; i < lines; i++ {
		fillGateLine(w, FailureLinesAdd(w, &r.Lines), i, lineBytes)
	}
}

func fillChosen(w *TableWorker, r *Chosen, whyBytes int) {
	setS(w, &r.Verb, "rework")
	r.P = 0.875
	setS(w, &r.Fix, "narrow")
	setS(w, &r.Reason, "wrong-lane")
	setS(w, &r.Act, "list")
	setS(w, &r.Why, longText("p=0.60 is under the bar 0.70. ", whyBytes))
}

// fillCluster fills a cluster of cards cards.
func fillCluster(w *TableWorker, r *Cluster, cards int) {
	setS(w, &r.ClassName, "env")
	r.Count = int64(cards)
	for i := 0; i < cards; i++ {
		setS(w, &ClusterCardsAdd(w, &r.Cards).Card, fmt.Sprintf("card-%04d", i))
	}
}

func sampleRawTokens() RawTokens {
	// Absence and zero differ: output is present at zero, cached is absent.
	return RawTokens{InputTokens: 4096, InputTokensPresent: true, OutputTokens: 0, OutputTokensPresent: true, ReasoningTokens: 17, ReasoningTokensPresent: true}
}

func fillRawRecord(w *TableWorker, r *RawRecord) {
	setS(w, &r.Harness, "muse")
	setS(w, &r.Execution, "fleet")
	setS(w, &r.SourceSchema, "muse-jsonl-v1")
	setS(w, &r.ProducerVersion, "1.4.2")
	r.Billing = BillingPlan
	r.Tokens = sampleRawTokens()
}

func fillRawUsage(w *TableWorker, r *RawUsage, records int) {
	r.Version = 1
	for i := 0; i < records; i++ {
		fillRawRecord(w, RawUsageRecordsAdd(w, &r.Records))
	}
}

// rawEnvelope is a cardcost envelope of records identical records, the last
// one's producer_version padded by pad bytes.
func rawEnvelope(records, pad int) *cardcost.RawUsage {
	u := &cardcost.RawUsage{Version: cardcost.RawVersion}
	for i := 0; i < records; i++ {
		u.Records = append(u.Records, cardcost.RawRecord{Harness: "muse", Execution: "fleet", SourceSchema: "muse-jsonl-v1",
			ProducerVersion: "1.4.2", Billing: cardcost.BillingPlan, Tokens: map[string]int64{"input_tokens": 4096, "output_tokens": 0}})
	}
	u.Records[records-1].ProducerVersion += strings.Repeat("x", pad)
	return u
}

// rawEnvelopeBytes is the size of the envelope's JSON, the quantity
// MaxRawBytes bounds (raw.go:72).
func rawEnvelopeBytes(t *testing.T, u *cardcost.RawUsage) int {
	t.Helper()
	b, err := json.Marshal(u)
	require.NoError(t, err)
	return len(b)
}

// fillRawUsageFrom fills the table from a cardcost envelope, field for field.
func fillRawUsageFrom(w *TableWorker, r *RawUsage, u *cardcost.RawUsage) {
	r.Version = uint32(u.Version)
	for _, rec := range u.Records {
		row := RawUsageRecordsAdd(w, &r.Records)
		setS(w, &row.Harness, rec.Harness)
		setS(w, &row.Execution, rec.Execution)
		setS(w, &row.SourceSchema, rec.SourceSchema)
		setS(w, &row.ProducerVersion, rec.ProducerVersion)
		row.Billing = BillingPlan
		row.Tokens.InputTokens, row.Tokens.InputTokensPresent = rec.Tokens["input_tokens"], true
		row.Tokens.OutputTokens, row.Tokens.OutputTokensPresent = rec.Tokens["output_tokens"], true
	}
}

// fillFrame fills a frame with stage staged recipes and a why of whyBytes.
func fillFrame(w *TableWorker, r *Frame, stage, whyBytes int) {
	r.Kind = FrameKindWork
	setS(w, &r.Card, "a13-defs-decide")
	r.Attempt = 1
	setS(w, &r.Tier, "pro")
	setS(w, &r.Model, "anthropic/claude-opus")
	setS(w, &r.Repo, "/work/nova-tools")
	setS(w, &r.BaseRef, "sprint/v15-schema-tables")
	setS(w, &r.StageSha, "d03635a82bde0000000000000000000000000000")
	setS(w, &r.Branch, "sprint/a13-defs-decide.w1.g1.e21")
	setS(w, &r.Why, string(bytes.Repeat([]byte("why "), whyBytes/4+1)[:whyBytes]))
	for i := 0; i < stage; i++ {
		setS(w, &FrameStageAdd(w, &r.Stage).Path, fmt.Sprintf("schema/recipe-%d.md", i))
	}
	setS(w, &r.Recipes, "/work/recipes")
	setS(w, &r.DecideBounce, "0.9")
}

func TestDecideRecordsGolden(t *testing.T) {
	t.Parallel()
	optionP := varFns[OptionP, OptionPBuilder, *OptionPBuilder]{name: "OptionP", load: OptionPLoadBuilder, fromJSON: OptionPFromJson, measureReason: OptionPLoadMeasureReason, measure: OptionPMeasure, save: OptionPSave,
		jsonMeasure: OptionPToJsonMeasure, toJSON: OptionPToJson, arena: func(b *OptionPBuilder) *TableArena { return &b.Arena },
		rules: func(_ *OptionPBuilder, r *OptionP, k chk) { optionPRules(r, k) }}
	answer := varFns[Answer, AnswerBuilder, *AnswerBuilder]{name: "Answer", load: AnswerLoadBuilder, fromJSON: AnswerFromJson, measureReason: AnswerLoadMeasureReason, measure: AnswerMeasure, save: AnswerSave,
		jsonMeasure: AnswerToJsonMeasure, toJSON: AnswerToJson, arena: func(b *AnswerBuilder) *TableArena { return &b.Arena },
		rules: func(_ *AnswerBuilder, r *Answer, k chk) { answerRules(r, k) }}
	named := varFns[NamedAnswer, NamedAnswerBuilder, *NamedAnswerBuilder]{name: "NamedAnswer", load: NamedAnswerLoadBuilder, fromJSON: NamedAnswerFromJson, measureReason: NamedAnswerLoadMeasureReason, measure: NamedAnswerMeasure, save: NamedAnswerSave,
		jsonMeasure: NamedAnswerToJsonMeasure, toJSON: NamedAnswerToJson, arena: func(b *NamedAnswerBuilder) *TableArena { return &b.Arena },
		rules: func(_ *NamedAnswerBuilder, r *NamedAnswer, k chk) { namedAnswerRules(r, k) }}
	input := varFns[Input, InputBuilder, *InputBuilder]{name: "Input", load: InputLoadBuilder, fromJSON: InputFromJson, measureReason: InputLoadMeasureReason, measure: InputMeasure, save: InputSave,
		jsonMeasure: InputToJsonMeasure, toJSON: InputToJson, arena: func(b *InputBuilder) *TableArena { return &b.Arena },
		rules: func(_ *InputBuilder, r *Input, k chk) { inputRules(r, k) }}
	decided := varFns[Decided, DecidedBuilder, *DecidedBuilder]{name: "Decided", load: DecidedLoadBuilder, fromJSON: DecidedFromJson, measureReason: DecidedLoadMeasureReason, measure: DecidedMeasure, save: DecidedSave,
		jsonMeasure: DecidedToJsonMeasure, toJSON: DecidedToJson, arena: func(b *DecidedBuilder) *TableArena { return &b.Arena },
		rules: func(_ *DecidedBuilder, r *Decided, k chk) { decidedRules(r, k) }}
	decision := varFns[Decision, DecisionBuilder, *DecisionBuilder]{name: "Decision", load: DecisionLoadBuilder, fromJSON: DecisionFromJson, measureReason: DecisionLoadMeasureReason, measure: DecisionMeasure, save: DecisionSave,
		jsonMeasure: DecisionToJsonMeasure, toJSON: DecisionToJson, arena: func(b *DecisionBuilder) *TableArena { return &b.Arena },
		rules: func(_ *DecisionBuilder, r *Decision, k chk) { decisionRules(r, k) }}
	outcome := varFns[Outcome, OutcomeBuilder, *OutcomeBuilder]{name: "Outcome", load: OutcomeLoadBuilder, fromJSON: OutcomeFromJson, measureReason: OutcomeLoadMeasureReason, measure: OutcomeMeasure, save: OutcomeSave,
		jsonMeasure: OutcomeToJsonMeasure, toJSON: OutcomeToJson, arena: func(b *OutcomeBuilder) *TableArena { return &b.Arena },
		rules: func(_ *OutcomeBuilder, r *Outcome, k chk) { outcomeRules(r, k) }}
	act := varFns[Act, ActBuilder, *ActBuilder]{name: "Act", load: ActLoadBuilder, fromJSON: ActFromJson, measureReason: ActLoadMeasureReason, measure: ActMeasure, save: ActSave,
		jsonMeasure: ActToJsonMeasure, toJSON: ActToJson, arena: func(b *ActBuilder) *TableArena { return &b.Arena },
		rules: func(_ *ActBuilder, r *Act, k chk) { actRules(r, k) }}
	line := varFns[RecordLine, RecordLineBuilder, *RecordLineBuilder]{name: "RecordLine", load: RecordLineLoadBuilder, fromJSON: RecordLineFromJson, measureReason: RecordLineLoadMeasureReason, measure: RecordLineMeasure, save: RecordLineSave,
		jsonMeasure: RecordLineToJsonMeasure, toJSON: RecordLineToJson, arena: func(b *RecordLineBuilder) *TableArena { return &b.Arena },
		rules: func(_ *RecordLineBuilder, r *RecordLine, k chk) { recordLineRules(r, k) }}
	judgment := varFns[JudgmentId, JudgmentIdBuilder, *JudgmentIdBuilder]{name: "JudgmentId", load: JudgmentIdLoadBuilder, fromJSON: JudgmentIdFromJson, measureReason: JudgmentIdLoadMeasureReason, measure: JudgmentIdMeasure, save: JudgmentIdSave,
		jsonMeasure: JudgmentIdToJsonMeasure, toJSON: JudgmentIdToJson, arena: func(b *JudgmentIdBuilder) *TableArena { return &b.Arena },
		rules: func(_ *JudgmentIdBuilder, r *JudgmentId, k chk) { judgmentIdRules(r, k) }}
	importLine := varFns[ImportLogLine, ImportLogLineBuilder, *ImportLogLineBuilder]{name: "ImportLogLine", load: ImportLogLineLoadBuilder, fromJSON: ImportLogLineFromJson, measureReason: ImportLogLineLoadMeasureReason, measure: ImportLogLineMeasure, save: ImportLogLineSave,
		jsonMeasure: ImportLogLineToJsonMeasure, toJSON: ImportLogLineToJson, arena: func(b *ImportLogLineBuilder) *TableArena { return &b.Arena },
		rules: func(_ *ImportLogLineBuilder, r *ImportLogLine, k chk) { importLogLineRules(r, k) }}
	rawRecord := varFns[RawRecord, RawRecordBuilder, *RawRecordBuilder]{name: "RawRecord", load: RawRecordLoadBuilder, fromJSON: RawRecordFromJson, measureReason: RawRecordLoadMeasureReason, measure: RawRecordMeasure, save: RawRecordSave,
		jsonMeasure: RawRecordToJsonMeasure, toJSON: RawRecordToJson, arena: func(b *RawRecordBuilder) *TableArena { return &b.Arena },
		rules: func(_ *RawRecordBuilder, r *RawRecord, k chk) { rawRecordRules(r, k) }}
	raw := varFns[RawUsage, RawUsageBuilder, *RawUsageBuilder]{name: "RawUsage", load: RawUsageLoadBuilder, fromJSON: RawUsageFromJson, measureReason: RawUsageLoadMeasureReason, measure: RawUsageMeasure, save: RawUsageSave,
		jsonMeasure: RawUsageToJsonMeasure, toJSON: RawUsageToJson, arena: func(b *RawUsageBuilder) *TableArena { return &b.Arena },
		rules: func(_ *RawUsageBuilder, r *RawUsage, k chk) { rawUsageRules(r, k) }}
	stagePath := varFns[StagePath, StagePathBuilder, *StagePathBuilder]{name: "StagePath", load: StagePathLoadBuilder, fromJSON: StagePathFromJson, measureReason: StagePathLoadMeasureReason, measure: StagePathMeasure, save: StagePathSave,
		jsonMeasure: StagePathToJsonMeasure, toJSON: StagePathToJson, arena: func(b *StagePathBuilder) *TableArena { return &b.Arena },
		rules: func(_ *StagePathBuilder, r *StagePath, k chk) { stagePathRules(r, k) }}
	frame := varFns[Frame, FrameBuilder, *FrameBuilder]{name: "Frame", load: FrameLoadBuilder, fromJSON: FrameFromJson, measureReason: FrameLoadMeasureReason, measure: FrameMeasure, save: FrameSave,
		jsonMeasure: FrameToJsonMeasure, toJSON: FrameToJson, arena: func(b *FrameBuilder) *TableArena { return &b.Arena },
		rules: func(_ *FrameBuilder, r *Frame, k chk) { frameRules(r, k) }}
	criterion := varFns[Criterion, CriterionBuilder, *CriterionBuilder]{name: "Criterion", load: CriterionLoadBuilder, fromJSON: CriterionFromJson, measureReason: CriterionLoadMeasureReason, measure: CriterionMeasure, save: CriterionSave,
		jsonMeasure: CriterionToJsonMeasure, toJSON: CriterionToJson, arena: func(b *CriterionBuilder) *TableArena { return &b.Arena },
		rules: func(_ *CriterionBuilder, r *Criterion, k chk) { criterionRules(r, k) }}
	question := varFns[Question, QuestionBuilder, *QuestionBuilder]{name: "Question", load: QuestionLoadBuilder, fromJSON: QuestionFromJson, measureReason: QuestionLoadMeasureReason, measure: QuestionMeasure, save: QuestionSave,
		jsonMeasure: QuestionToJsonMeasure, toJSON: QuestionToJson, arena: func(b *QuestionBuilder) *TableArena { return &b.Arena },
		rules: func(_ *QuestionBuilder, r *Question, k chk) { questionRules(r, k) }}
	namedQuestion := varFns[NamedQuestion, NamedQuestionBuilder, *NamedQuestionBuilder]{name: "NamedQuestion", load: NamedQuestionLoadBuilder, fromJSON: NamedQuestionFromJson, measureReason: NamedQuestionLoadMeasureReason, measure: NamedQuestionMeasure, save: NamedQuestionSave,
		jsonMeasure: NamedQuestionToJsonMeasure, toJSON: NamedQuestionToJson, arena: func(b *NamedQuestionBuilder) *TableArena { return &b.Arena },
		rules: func(_ *NamedQuestionBuilder, r *NamedQuestion, k chk) { namedQuestionRules(r, k) }}
	decideSchema := varFns[DecideSchema, DecideSchemaBuilder, *DecideSchemaBuilder]{name: "DecideSchema", load: DecideSchemaLoadBuilder, fromJSON: DecideSchemaFromJson, measureReason: DecideSchemaLoadMeasureReason, measure: DecideSchemaMeasure, save: DecideSchemaSave,
		jsonMeasure: DecideSchemaToJsonMeasure, toJSON: DecideSchemaToJson, arena: func(b *DecideSchemaBuilder) *TableArena { return &b.Arena },
		rules: func(_ *DecideSchemaBuilder, r *DecideSchema, k chk) { decideSchemaRules(r, k) }}
	jevRequest := varFns[JevRequest, JevRequestBuilder, *JevRequestBuilder]{name: "JevRequest", load: JevRequestLoadBuilder, fromJSON: JevRequestFromJson, measureReason: JevRequestLoadMeasureReason, measure: JevRequestMeasure, save: JevRequestSave,
		jsonMeasure: JevRequestToJsonMeasure, toJSON: JevRequestToJson, arena: func(b *JevRequestBuilder) *TableArena { return &b.Arena },
		rules: func(_ *JevRequestBuilder, r *JevRequest, k chk) { jevRequestRules(r, k) }}
	jevAnswer := varFns[JevAnswer, JevAnswerBuilder, *JevAnswerBuilder]{name: "JevAnswer", load: JevAnswerLoadBuilder, fromJSON: JevAnswerFromJson, measureReason: JevAnswerLoadMeasureReason, measure: JevAnswerMeasure, save: JevAnswerSave,
		jsonMeasure: JevAnswerToJsonMeasure, toJSON: JevAnswerToJson, arena: func(b *JevAnswerBuilder) *TableArena { return &b.Arena },
		rules: func(_ *JevAnswerBuilder, r *JevAnswer, k chk) { jevAnswerRules(r, k) }}
	namedJevAnswer := varFns[NamedJevAnswer, NamedJevAnswerBuilder, *NamedJevAnswerBuilder]{name: "NamedJevAnswer", load: NamedJevAnswerLoadBuilder, fromJSON: NamedJevAnswerFromJson, measureReason: NamedJevAnswerLoadMeasureReason, measure: NamedJevAnswerMeasure, save: NamedJevAnswerSave,
		jsonMeasure: NamedJevAnswerToJsonMeasure, toJSON: NamedJevAnswerToJson, arena: func(b *NamedJevAnswerBuilder) *TableArena { return &b.Arena },
		rules: func(_ *NamedJevAnswerBuilder, r *NamedJevAnswer, k chk) { namedJevAnswerRules(r, k) }}
	jevResponse := varFns[JevResponse, JevResponseBuilder, *JevResponseBuilder]{name: "JevResponse", load: JevResponseLoadBuilder, fromJSON: JevResponseFromJson, measureReason: JevResponseLoadMeasureReason, measure: JevResponseMeasure, save: JevResponseSave,
		jsonMeasure: JevResponseToJsonMeasure, toJSON: JevResponseToJson, arena: func(b *JevResponseBuilder) *TableArena { return &b.Arena },
		rules: func(_ *JevResponseBuilder, r *JevResponse, k chk) { jevResponseRules(r, k) }}
	fixedAnswer := varFns[FixedAnswer, FixedAnswerBuilder, *FixedAnswerBuilder]{name: "FixedAnswer", load: FixedAnswerLoadBuilder, fromJSON: FixedAnswerFromJson, measureReason: FixedAnswerLoadMeasureReason, measure: FixedAnswerMeasure, save: FixedAnswerSave,
		jsonMeasure: FixedAnswerToJsonMeasure, toJSON: FixedAnswerToJson, arena: func(b *FixedAnswerBuilder) *TableArena { return &b.Arena },
		rules: func(_ *FixedAnswerBuilder, r *FixedAnswer, k chk) { fixedAnswerRules(r, k) }}
	namedFixedAnswer := varFns[NamedFixedAnswer, NamedFixedAnswerBuilder, *NamedFixedAnswerBuilder]{name: "NamedFixedAnswer", load: NamedFixedAnswerLoadBuilder, fromJSON: NamedFixedAnswerFromJson, measureReason: NamedFixedAnswerLoadMeasureReason, measure: NamedFixedAnswerMeasure, save: NamedFixedAnswerSave,
		jsonMeasure: NamedFixedAnswerToJsonMeasure, toJSON: NamedFixedAnswerToJson, arena: func(b *NamedFixedAnswerBuilder) *TableArena { return &b.Arena },
		rules: func(_ *NamedFixedAnswerBuilder, r *NamedFixedAnswer, k chk) { namedFixedAnswerRules(r, k) }}
	fixedTable := varFns[FixedTable, FixedTableBuilder, *FixedTableBuilder]{name: "FixedTable", load: FixedTableLoadBuilder, fromJSON: FixedTableFromJson, measureReason: FixedTableLoadMeasureReason, measure: FixedTableMeasure, save: FixedTableSave,
		jsonMeasure: FixedTableToJsonMeasure, toJSON: FixedTableToJson, arena: func(b *FixedTableBuilder) *TableArena { return &b.Arena },
		rules: func(_ *FixedTableBuilder, r *FixedTable, k chk) { fixedTableRules(r, k) }}
	example := varFns[Example, ExampleBuilder, *ExampleBuilder]{name: "Example", load: ExampleLoadBuilder, fromJSON: ExampleFromJson, measureReason: ExampleLoadMeasureReason, measure: ExampleMeasure, save: ExampleSave,
		jsonMeasure: ExampleToJsonMeasure, toJSON: ExampleToJson, arena: func(b *ExampleBuilder) *TableArena { return &b.Arena },
		rules: func(_ *ExampleBuilder, r *Example, k chk) { exampleRules(r, k) }}
	setEntry := varFns[SetEntry, SetEntryBuilder, *SetEntryBuilder]{name: "SetEntry", load: SetEntryLoadBuilder, fromJSON: SetEntryFromJson, measureReason: SetEntryLoadMeasureReason, measure: SetEntryMeasure, save: SetEntrySave,
		jsonMeasure: SetEntryToJsonMeasure, toJSON: SetEntryToJson, arena: func(b *SetEntryBuilder) *TableArena { return &b.Arena },
		rules: func(_ *SetEntryBuilder, r *SetEntry, k chk) { setEntryRules(r, k) }}
	logLine := varFns[LogLine, LogLineBuilder, *LogLineBuilder]{name: "LogLine", load: LogLineLoadBuilder, fromJSON: LogLineFromJson, measureReason: LogLineLoadMeasureReason, measure: LogLineMeasure, save: LogLineSave,
		jsonMeasure: LogLineToJsonMeasure, toJSON: LogLineToJson, arena: func(b *LogLineBuilder) *TableArena { return &b.Arena },
		rules: func(_ *LogLineBuilder, r *LogLine, k chk) { logLineRules(r, k) }}
	logExport := varFns[LogExport, LogExportBuilder, *LogExportBuilder]{name: "LogExport", load: LogExportLoadBuilder, fromJSON: LogExportFromJson, measureReason: LogExportLoadMeasureReason, measure: LogExportMeasure, save: LogExportSave,
		jsonMeasure: LogExportToJsonMeasure, toJSON: LogExportToJson, arena: func(b *LogExportBuilder) *TableArena { return &b.Arena },
		rules: func(_ *LogExportBuilder, r *LogExport, k chk) { logExportRules(r, k) }}
	importExport := varFns[ImportExport, ImportExportBuilder, *ImportExportBuilder]{name: "ImportExport", load: ImportExportLoadBuilder, fromJSON: ImportExportFromJson, measureReason: ImportExportLoadMeasureReason, measure: ImportExportMeasure, save: ImportExportSave,
		jsonMeasure: ImportExportToJsonMeasure, toJSON: ImportExportToJson, arena: func(b *ImportExportBuilder) *TableArena { return &b.Arena },
		rules: func(_ *ImportExportBuilder, r *ImportExport, k chk) { importExportRules(r, k) }}

	gateLine := varFns[GateLine, GateLineBuilder, *GateLineBuilder]{name: "GateLine", load: GateLineLoadBuilder, fromJSON: GateLineFromJson, measureReason: GateLineLoadMeasureReason, measure: GateLineMeasure, save: GateLineSave,
		jsonMeasure: GateLineToJsonMeasure, toJSON: GateLineToJson, arena: func(b *GateLineBuilder) *TableArena { return &b.Arena },
		rules: func(_ *GateLineBuilder, r *GateLine, k chk) { gateLineRules(r, k) }}
	failure := varFns[Failure, FailureBuilder, *FailureBuilder]{name: "Failure", load: FailureLoadBuilder, fromJSON: FailureFromJson, measureReason: FailureLoadMeasureReason, measure: FailureMeasure, save: FailureSave,
		jsonMeasure: FailureToJsonMeasure, toJSON: FailureToJson, arena: func(b *FailureBuilder) *TableArena { return &b.Arena },
		rules: func(_ *FailureBuilder, r *Failure, k chk) { failureRules(r, k) }}
	chosen := varFns[Chosen, ChosenBuilder, *ChosenBuilder]{name: "Chosen", load: ChosenLoadBuilder, fromJSON: ChosenFromJson, measureReason: ChosenLoadMeasureReason, measure: ChosenMeasure, save: ChosenSave,
		jsonMeasure: ChosenToJsonMeasure, toJSON: ChosenToJson, arena: func(b *ChosenBuilder) *TableArena { return &b.Arena },
		rules: func(_ *ChosenBuilder, r *Chosen, k chk) { chosenRules(r, k) }}
	clusterCard := varFns[ClusterCard, ClusterCardBuilder, *ClusterCardBuilder]{name: "ClusterCard", load: ClusterCardLoadBuilder, fromJSON: ClusterCardFromJson, measureReason: ClusterCardLoadMeasureReason, measure: ClusterCardMeasure, save: ClusterCardSave,
		jsonMeasure: ClusterCardToJsonMeasure, toJSON: ClusterCardToJson, arena: func(b *ClusterCardBuilder) *TableArena { return &b.Arena },
		rules: func(_ *ClusterCardBuilder, r *ClusterCard, k chk) { clusterCardRules(r, k) }}
	cluster := varFns[Cluster, ClusterBuilder, *ClusterBuilder]{name: "Cluster", load: ClusterLoadBuilder, fromJSON: ClusterFromJson, measureReason: ClusterLoadMeasureReason, measure: ClusterMeasure, save: ClusterSave,
		jsonMeasure: ClusterToJsonMeasure, toJSON: ClusterToJson, arena: func(b *ClusterBuilder) *TableArena { return &b.Arena },
		rules: func(_ *ClusterBuilder, r *Cluster, k chk) { clusterRules(r, k) }}
	bars := plainFns("Bars", BarsLoad, BarsFromJson, BarsMeasure, BarsSave, BarsToJsonMeasure, BarsToJson, func(r *Bars, k chk) { barsRules(r, k) })
	gateBars := plainFns("GateBars", GateBarsLoad, GateBarsFromJson, GateBarsMeasure, GateBarsSave, GateBarsToJsonMeasure, GateBarsToJson, func(r *GateBars, k chk) { gateBarsRules(r, k) })

	t.Run("Usage", func(t *testing.T) {
		t.Parallel()
		// No bound at all: the code states none (decide.go Usage), so none is tested.
		exerciseVar(t, plainFns("Usage", UsageLoad, UsageFromJson, UsageMeasure, UsageSave, UsageToJsonMeasure, UsageToJson, func(r *Usage, k chk) { usageRules(r, k) }),
			func(_ *plain[Usage], r *Usage) { *r = Usage{InputTokens: -3, OutputTokens: math.MaxInt64} }, nil)
	})
	t.Run("RawTokens", func(t *testing.T) {
		t.Parallel()
		tokens := plainFns("RawTokens", RawTokensLoad, RawTokensFromJson, RawTokensMeasure, RawTokensSave, RawTokensToJsonMeasure, RawTokensToJson, func(r *RawTokens, k chk) { rawTokensRules(r, k) })
		exerciseVar(t, tokens, func(_ *plain[RawTokens], r *RawTokens) { *r = sampleRawTokens() }, plainBounds([]pbound[RawTokens]{
			{"input below zero", "input_tokens", "clamped", func(v *RawTokens) { v.InputTokens = -1 }},
			{"cached below zero", "cached_tokens", "clamped", func(v *RawTokens) { v.CachedTokens, v.CachedTokensPresent = -7, true }},
			{"reasoning below zero", "reasoning_tokens", "clamped", func(v *RawTokens) { v.ReasoningTokens = math.MinInt64 }},
		}))
		// Absence and zero differ (raw.go): the image keeps which is which.
		r := schemaread.NewReader(tokens.codec())
		for _, form := range []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON} {
			none, zero := tokens.fresh(), tokens.fresh()
			zero.v.OutputTokensPresent = true
			for want, v := range map[bool]*plain[RawTokens]{false: none, true: zero} {
				image, err := r.Write(&v, form)
				require.NoError(t, err)
				got, _, err := r.Read(image)
				require.NoError(t, err)
				assert.Equal(t, want, got.v.OutputTokensPresent, form.String())
				assert.Zero(t, got.v.OutputTokens)
			}
		}
	})

	allForms := []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON}

	t.Run("OptionP", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, optionP, func(b *OptionPBuilder, r *OptionP) { fillOptionP(&b.Main, r, "done", 0.875) }, []vbound[OptionP, *OptionPBuilder]{
			{"option empty", "option", "absent_required", func(_ *OptionPBuilder, r *OptionP) { r.Option = 0 }},
			{"probability above one", "p", "clamped", func(_ *OptionPBuilder, r *OptionP) { r.P = 1.0000001 }},
			{"probability below zero", "p", "clamped", func(_ *OptionPBuilder, r *OptionP) { r.P = -0.1 }},
			{"probability not a number", "p", "malformed", func(_ *OptionPBuilder, r *OptionP) { r.P = math.NaN() }},
			{"option not UTF-8", "option", "malformed", func(b *OptionPBuilder, r *OptionP) { TableStringAt(&r.Option, &b.Arena).Data[0] = 0xff }},
		})
	})
	t.Run("Answer", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, answer, func(b *AnswerBuilder, r *Answer) { fillAnswer(&b.Main, r, 3) }, []vbound[Answer, *AnswerBuilder]{
			{"kind none", "kind", "clamped", func(_ *AnswerBuilder, r *Answer) { r.Kind = AnswerTypeNone }},
			{"an option above one", "p", "clamped", func(b *AnswerBuilder, r *Answer) { r.P.At(1, &b.Arena).P = 2 }},
			{"value empty", "value", "absent_required", func(_ *AnswerBuilder, r *Answer) { r.Value = 0 }},
			{"method not UTF-8", "method", "malformed", func(b *AnswerBuilder, r *Answer) { TableStringAt(&r.Method, &b.Arena).Data[0] = 0xff }},
		})
		// A choice names any number of options (decide.go sets no ceiling):
		// two, fifteen and forty round-trip in every form, each option intact.
		r := schemaread.NewReader(answer.codec())
		for _, options := range []int{2, 15, 40} {
			w := answer.fresh()
			fillAnswer(&w.Main, w.GetRoot(), options)
			for _, form := range allForms {
				image, err := r.Write(&w, form)
				require.NoError(t, err, "%d options as %s", options, form)
				got, _, err := r.Read(image)
				require.NoError(t, err, "%d options as %s", options, form)
				root := answer.root(got)
				require.EqualValues(t, options, root.P.Count)
				for i := 0; i < options; i++ {
					o := root.P.At(int32(i), &got.Arena)
					assert.Equal(t, fmt.Sprintf("opt-%d", i), string(TableStringAt(&o.Option, &got.Arena).Data))
					assert.Equal(t, 1/float64(options), o.P)
				}
			}
		}
	})
	t.Run("NamedAnswer", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, named, func(b *NamedAnswerBuilder, r *NamedAnswer) { fillNamedAnswer(&b.Main, r, 2) }, []vbound[NamedAnswer, *NamedAnswerBuilder]{
			{"question empty", "question", "absent_required", func(_ *NamedAnswerBuilder, r *NamedAnswer) { r.Question = 0 }},
			{"the answer's kind none", "kind", "clamped", func(_ *NamedAnswerBuilder, r *NamedAnswer) { r.Answer.Kind = AnswerTypeNone }},
		})
	})
	t.Run("Input", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, input, func(b *InputBuilder, r *Input) { fillInput(&b.Main, r, 0) }, []vbound[Input, *InputBuilder]{
			{"key empty", "key", "absent_required", func(_ *InputBuilder, r *Input) { r.Key = 0 }},
			{"value not UTF-8", "value", "malformed", func(b *InputBuilder, r *Input) { TableStringAt(&r.Value, &b.Arena).Data[0] = 0xff }},
		})
	})
	t.Run("Decided", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, decided, func(b *DecidedBuilder, r *Decided) { fillDecided(&b.Main, r) }, []vbound[Decided, *DecidedBuilder]{
			{"probability above one", "p", "clamped", func(_ *DecidedBuilder, r *Decided) { r.P = 1.5 }},
			{"value with a space", "value", "malformed", func(b *DecidedBuilder, r *Decided) { TableStringAt(&r.Value, &b.Arena).Data[1] = ' ' }},
			{"op empty", "op", "absent_required", func(_ *DecidedBuilder, r *Decided) { r.Op = 0 }},
			{"value empty", "value", "absent_required", func(_ *DecidedBuilder, r *Decided) { r.Value = 0 }},
		})
	})
	t.Run("Decision", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, decision, func(b *DecisionBuilder, r *Decision) { fillDecision(&b.Main, r, 3, 1, 2, 200) }, []vbound[Decision, *DecisionBuilder]{
			{"id empty", "id", "absent_required", func(_ *DecisionBuilder, r *Decision) { r.Id = 0 }},
			{"decision name empty", "decision_name", "absent_required", func(_ *DecisionBuilder, r *Decision) { r.DecisionName = 0 }},
			{"state not UTF-8", "state", "malformed", func(b *DecisionBuilder, r *Decision) { TableStringAt(&r.State, &b.Arena).Data[0] = 0xff }},
			{"an input key empty", "key", "absent_required", func(b *DecisionBuilder, r *Decision) { r.Inputs.At(0, &b.Arena).Key = 0 }},
			{"an answer above one", "p", "clamped", func(b *DecisionBuilder, r *Decision) {
				r.Answers.At(0, &b.Arena).Answer.P.At(0, &b.Arena).P = 1.01
			}},
		})
		// The widest record the code writes and beyond: ten inputs
		// (judgment.go:328), nine questions of fourteen options (brief.go), a
		// 100-input record, and a state of 1 MiB, in all three forms.
		r := schemaread.NewReader(decision.codec())
		for _, c := range []struct{ inputs, answers, options, state int }{{10, 9, 14, 2*16<<10 + 1024}, {100, 12, 40, 1 << 20}} {
			w := decision.fresh()
			fillDecision(&w.Main, w.GetRoot(), c.inputs, c.answers, c.options, c.state)
			want, err := r.Write(&w, schemaread.FormBinary)
			require.NoError(t, err)
			for _, form := range allForms {
				image, err := r.Write(&w, form)
				require.NoError(t, err, form.String())
				got, _, err := r.Read(image)
				require.NoError(t, err, form.String())
				root := decision.root(got)
				assert.EqualValues(t, c.inputs, root.Inputs.Count)
				assert.EqualValues(t, c.answers, root.Answers.Count)
				assert.EqualValues(t, c.options, root.Answers.At(int32(c.answers-1), &got.Arena).Answer.P.Count)
				assert.Len(t, TableStringAt(&root.State, &got.Arena).Data, c.state)
				again, err := r.Write(&got, schemaread.FormBinary)
				require.NoError(t, err)
				assert.Equal(t, want, again, form.String())
			}
		}
	})
	t.Run("Outcome", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, outcome, func(b *OutcomeBuilder, r *Outcome) { fillOutcome(&b.Main, r) }, []vbound[Outcome, *OutcomeBuilder]{
			{"label empty", "label", "absent_required", func(_ *OutcomeBuilder, r *Outcome) { r.Label = 0 }},
			{"id empty", "id", "absent_required", func(_ *OutcomeBuilder, r *Outcome) { r.Id = 0 }},
			{"note not UTF-8", "note", "malformed", func(b *OutcomeBuilder, r *Outcome) { TableStringAt(&r.Note, &b.Arena).Data[0] = 0xff }},
		})
	})
	t.Run("Act", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, act, func(b *ActBuilder, r *Act) { fillAct(&b.Main, r) }, []vbound[Act, *ActBuilder]{
			{"step empty", "step", "absent_required", func(_ *ActBuilder, r *Act) { r.Step = 0 }},
			{"id empty", "id", "absent_required", func(_ *ActBuilder, r *Act) { r.Id = 0 }},
		})
	})
	t.Run("RecordLine", func(t *testing.T) {
		t.Parallel()
		decisionLine := func(b *RecordLineBuilder, r *RecordLine) {
			r.Kind = LineKindDecision
			fillDecision(&b.Main, DecisionEmplace(&b.Main, &r.AsDecision), 10, 1, 14, 300)
		}
		exerciseVar(t, line, decisionLine, []vbound[RecordLine, *RecordLineBuilder]{
			{"kind none", "kind", "clamped", func(_ *RecordLineBuilder, r *RecordLine) { r.Kind = LineKindNone }},
			{"the named record absent", "as_decision", "absent_required", func(_ *RecordLineBuilder, r *RecordLine) { r.AsDecision = 0 }},
			{"an outcome beside the decision", "as_outcome", "malformed", func(b *RecordLineBuilder, r *RecordLine) {
				fillOutcome(&b.Main, OutcomeEmplace(&b.Main, &r.AsOutcome))
			}},
			{"an act beside the decision", "as_act", "malformed", func(b *RecordLineBuilder, r *RecordLine) { fillAct(&b.Main, ActEmplace(&b.Main, &r.AsAct)) }},
			{"the decision's id empty", "id", "absent_required", func(b *RecordLineBuilder, r *RecordLine) { DecisionAt(&r.AsDecision, &b.Arena).Id = 0 }},
		})
		// The other two kinds are lines of their own.
		rd := schemaread.NewReader(line.codec())
		for _, fill := range []func(*RecordLineBuilder, *RecordLine){
			func(b *RecordLineBuilder, r *RecordLine) {
				r.Kind = LineKindOutcome
				fillOutcome(&b.Main, OutcomeEmplace(&b.Main, &r.AsOutcome))
			},
			func(b *RecordLineBuilder, r *RecordLine) {
				r.Kind = LineKindAct
				fillAct(&b.Main, ActEmplace(&b.Main, &r.AsAct))
			},
		} {
			w := line.fresh()
			fill(w, w.GetRoot())
			for _, form := range allForms {
				image, err := rd.Write(&w, form)
				require.NoError(t, err)
				got, _, err := rd.Read(image)
				require.NoError(t, err)
				assert.Equal(t, line.root(w).Kind, line.root(got).Kind)
			}
		}
	})
	t.Run("JudgmentId", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, judgment, func(b *JudgmentIdBuilder, r *JudgmentId) { setS(&b.Main, &r.Id, "judgment-0007") }, []vbound[JudgmentId, *JudgmentIdBuilder]{
			{"id empty", "id", "absent_required", func(_ *JudgmentIdBuilder, r *JudgmentId) { r.Id = 0 }},
		})
	})
	t.Run("ImportLogLine", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, importLine, func(b *ImportLogLineBuilder, r *ImportLogLine) { fillImportLogLine(&b.Main, r, 1) }, []vbound[ImportLogLine, *ImportLogLineBuilder]{
			{"verb not UTF-8", "verb", "malformed", func(b *ImportLogLineBuilder, r *ImportLogLine) { TableStringAt(&r.Verb, &b.Arena).Data[0] = 0xff }},
			{"an answer id empty", "id", "absent_required", func(b *ImportLogLineBuilder, r *ImportLogLine) { r.Answers.At(0, &b.Arena).Id = 0 }},
		})
		// The import takes every id of an export line (import.go:161-176): no
		// ids, one, nine and a hundred all round-trip in every form.
		rd := schemaread.NewReader(importLine.codec())
		for _, ids := range []int{0, 1, 9, 100} {
			w := importLine.fresh()
			fillImportLogLine(&w.Main, w.GetRoot(), ids)
			for _, form := range allForms {
				image, err := rd.Write(&w, form)
				require.NoError(t, err, "%d ids as %s", ids, form)
				got, _, err := rd.Read(image)
				require.NoError(t, err, "%d ids as %s", ids, form)
				root := importLine.root(got)
				require.EqualValues(t, ids, root.Answers.Count)
				for i := 0; i < ids; i++ {
					id := root.Answers.At(int32(i), &got.Arena)
					assert.Equal(t, fmt.Sprintf("judgment-%04d", i), string(TableStringAt(&id.Id, &got.Arena).Data))
				}
			}
		}
	})
	t.Run("RawRecord", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, rawRecord, func(b *RawRecordBuilder, r *RawRecord) { fillRawRecord(&b.Main, r) }, []vbound[RawRecord, *RawRecordBuilder]{
			{"harness not muse", "harness", "malformed", func(b *RawRecordBuilder, r *RawRecord) { setS(&b.Main, &r.Harness, "codex") }},
			{"execution not fleet", "execution", "malformed", func(b *RawRecordBuilder, r *RawRecord) { setS(&b.Main, &r.Execution, "local") }},
			{"source schema not muse-jsonl-v1", "source_schema", "malformed", func(b *RawRecordBuilder, r *RawRecord) { setS(&b.Main, &r.SourceSchema, "muse-jsonl-v2") }},
			{"producer version empty", "producer_version", "absent_required", func(_ *RawRecordBuilder, r *RawRecord) { r.ProducerVersion = 0 }},
			{"billing none", "billing", "clamped", func(_ *RawRecordBuilder, r *RawRecord) { r.Billing = BillingNone }},
			{"a negative counter", "input_tokens", "clamped", func(_ *RawRecordBuilder, r *RawRecord) { r.Tokens.InputTokens = -1 }},
		})
	})
	t.Run("RawUsage", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, raw, func(b *RawUsageBuilder, r *RawUsage) { fillRawUsage(&b.Main, r, 2) }, []vbound[RawUsage, *RawUsageBuilder]{
			{"version two", "version", "clamped", func(_ *RawUsageBuilder, r *RawUsage) { r.Version = 2 }},
			{"version zero", "version", "clamped", func(_ *RawUsageBuilder, r *RawUsage) { r.Version = 0 }},
			{"no records", "records", "absent_required", func(_ *RawUsageBuilder, r *RawUsage) { r.Records.Count = 0 }},
			{"a record's harness not muse", "harness", "malformed", func(b *RawUsageBuilder, r *RawUsage) { setS(&b.Main, &r.Records.At(1, &b.Arena).Harness, "x") }},
		})
		// The code bounds the envelope by bytes, not by records (raw.go:21,
		// :72, :80): find the most records whose JSON fits MaxRawBytes, pad the
		// last producer_version to exactly MaxRawBytes, and test the rule at its
		// boundary through the code.
		n := 1
		for rawEnvelopeBytes(t, rawEnvelope(n+1, 0)) <= cardcost.MaxRawBytes {
			n++
		}
		t.Logf("%d records fill an envelope of at most %d bytes", n, cardcost.MaxRawBytes)
		atCap := rawEnvelope(n, cardcost.MaxRawBytes-rawEnvelopeBytes(t, rawEnvelope(n, 0)))
		require.Equal(t, cardcost.MaxRawBytes, rawEnvelopeBytes(t, atCap))
		text, err := cardcost.EncodeRaw(atCap)
		require.NoError(t, err, "an envelope of exactly MaxRawBytes is accepted")
		back, err := cardcost.DecodeRaw(text)
		require.NoError(t, err)
		assert.Equal(t, atCap, back)
		over := rawEnvelope(n, cardcost.MaxRawBytes-rawEnvelopeBytes(t, rawEnvelope(n, 0))+1)
		require.Equal(t, cardcost.MaxRawBytes+1, rawEnvelopeBytes(t, over))
		_, err = cardcost.EncodeRaw(over)
		require.ErrorContains(t, err, "exceeds", "one byte more than MaxRawBytes is refused")
		overJSON, err := json.Marshal(over)
		require.NoError(t, err)
		_, err = cardcost.DecodeRaw(base64.RawURLEncoding.EncodeToString(overJSON))
		require.ErrorContains(t, err, "too large", "DecodeRaw refuses one byte more")
		// The table holds no count: the envelope at the cap, the same records
		// past it and a thousand all round-trip in every form.
		rd := schemaread.NewReader(raw.codec())
		for _, u := range []*cardcost.RawUsage{atCap, over, rawEnvelope(1000, 0)} {
			w := raw.fresh()
			fillRawUsageFrom(&w.Main, w.GetRoot(), u)
			for _, form := range allForms {
				image, err := rd.Write(&w, form)
				require.NoError(t, err, "%d records as %s", len(u.Records), form)
				got, _, err := rd.Read(image)
				require.NoError(t, err, "%d records as %s", len(u.Records), form)
				root := raw.root(got)
				require.EqualValues(t, len(u.Records), root.Records.Count)
				last := root.Records.At(root.Records.Count-1, &got.Arena)
				assert.Equal(t, u.Records[len(u.Records)-1].ProducerVersion, string(TableStringAt(&last.ProducerVersion, &got.Arena).Data))
			}
		}
	})
	t.Run("StagePath", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, stagePath, func(b *StagePathBuilder, r *StagePath) { setS(&b.Main, &r.Path, "schema/recipe.md") }, []vbound[StagePath, *StagePathBuilder]{
			{"path empty", "path", "absent_required", func(_ *StagePathBuilder, r *StagePath) { r.Path = 0 }},
		})
	})
	t.Run("Frame", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, frame, func(b *FrameBuilder, r *Frame) { fillFrame(&b.Main, r, 1, 40) }, []vbound[Frame, *FrameBuilder]{
			{"kind none", "kind", "clamped", func(_ *FrameBuilder, r *Frame) { r.Kind = FrameKindNone }},
			{"card empty", "card", "absent_required", func(_ *FrameBuilder, r *Frame) { r.Card = 0 }},
			{"why not UTF-8", "why", "malformed", func(b *FrameBuilder, r *Frame) { TableStringAt(&r.Why, &b.Arena).Data[0] = 0xff }},
			{"a staged path empty", "path", "absent_required", func(b *FrameBuilder, r *Frame) { r.Stage.At(0, &b.Arena).Path = 0 }},
		})
		// The code bounds no length, count or number (contract.go:24-142): a
		// 200 KB why, 100 staged recipes and a negative attempt all round-trip.
		rd := schemaread.NewReader(frame.codec())
		w := frame.fresh()
		fillFrame(&w.Main, w.GetRoot(), 100, 200<<10)
		w.GetRoot().Attempt, w.GetRoot().PrevAttempt = -2, 1<<40
		for _, form := range allForms {
			image, err := rd.Write(&w, form)
			require.NoError(t, err, form.String())
			got, _, err := rd.Read(image)
			require.NoError(t, err, form.String())
			root := frame.root(got)
			assert.EqualValues(t, 100, root.Stage.Count)
			assert.Len(t, TableStringAt(&root.Why, &got.Arena).Data, 200<<10)
			assert.EqualValues(t, -2, root.Attempt)
			assert.EqualValues(t, int64(1)<<40, root.PrevAttempt)
		}
	})

	t.Run("Criterion", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, criterion, func(b *CriterionBuilder, r *Criterion) { fillCriterion(&b.Main, r, 0) }, []vbound[Criterion, *CriterionBuilder]{
			{"option not UTF-8", "option", "malformed", func(b *CriterionBuilder, r *Criterion) { TableStringAt(&r.Option, &b.Arena).Data[0] = 0xff }},
			{"meaning not UTF-8", "meaning", "malformed", func(b *CriterionBuilder, r *Criterion) { TableStringAt(&r.Meaning, &b.Arena).Data[0] = 0xff }},
		})
	})
	t.Run("Question", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, question, func(b *QuestionBuilder, r *Question) { fillQuestion(&b.Main, r, AnswerTypeChoice, 3, 60) }, []vbound[Question, *QuestionBuilder]{
			{"type none", "kind", "clamped", func(_ *QuestionBuilder, r *Question) { r.Kind = AnswerTypeNone }},
			{"instructions empty", "instructions", "absent_required", func(_ *QuestionBuilder, r *Question) { r.Instructions = 0 }},
			{"instructions blank", "instructions", "absent_required", func(b *QuestionBuilder, r *Question) { setS(&b.Main, &r.Instructions, " \t\n") }},
			{"a choice with one option", "criteria", "clamped", func(b *QuestionBuilder, r *Question) { eraseCriteria(&b.Arena, r, 2) }},
			{"a choice with none", "criteria", "clamped", func(b *QuestionBuilder, r *Question) { eraseCriteria(&b.Arena, r, 3) }},
			{"a noul with options", "criteria", "clamped", func(_ *QuestionBuilder, r *Question) { r.Kind = AnswerTypeNoul }},
			{"an option twice", "option", "malformed", func(b *QuestionBuilder, r *Question) { setS(&b.Main, &r.Criteria.At(1, &b.Arena).Option, "option-0") }},
			{"instructions not UTF-8", "instructions", "malformed", func(b *QuestionBuilder, r *Question) { TableStringAt(&r.Instructions, &b.Arena).Data[0] = 0xff }},
		})
		// A noul names no option, and a choice any number from two (decide.go:101).
		for _, c := range []struct {
			kind    AnswerType
			options int
		}{{AnswerTypeNoul, 0}, {AnswerTypeChoice, 2}, {AnswerTypeChoice, 200}} {
			wholeIn(t, question, fmt.Sprintf("kind %d with %d options", c.kind, c.options), func(b *QuestionBuilder, r *Question) {
				fillQuestion(&b.Main, r, c.kind, c.options, 64<<10)
			}, func(t *testing.T, got *QuestionBuilder, r *Question) {
				assert.EqualValues(t, c.options, r.Criteria.Count)
				assert.Len(t, strAt(&r.Instructions, &got.Arena), 64<<10)
			})
		}
	})
	t.Run("NamedQuestion", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, namedQuestion, func(b *NamedQuestionBuilder, r *NamedQuestion) {
			fillNamedQuestion(&b.Main, r, "class", AnswerTypeChoice, 2, 40)
		}, []vbound[NamedQuestion, *NamedQuestionBuilder]{
			{"name not UTF-8", "name", "malformed", func(b *NamedQuestionBuilder, r *NamedQuestion) { TableStringAt(&r.Name, &b.Arena).Data[0] = 0xff }},
			{"the question's type none", "kind", "clamped", func(_ *NamedQuestionBuilder, r *NamedQuestion) { r.Question.Kind = AnswerTypeNone }},
			{"the question's instructions empty", "instructions", "absent_required", func(_ *NamedQuestionBuilder, r *NamedQuestion) { r.Question.Instructions = 0 }},
		})
	})
	t.Run("DecideSchema", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, decideSchema, func(b *DecideSchemaBuilder, r *DecideSchema) { fillDecideSchema(&b.Main, r, 2, 3, 60) }, []vbound[DecideSchema, *DecideSchemaBuilder]{
			{"name empty", "name", "absent_required", func(_ *DecideSchemaBuilder, r *DecideSchema) { r.Name = 0 }},
			{"name blank", "name", "absent_required", func(b *DecideSchemaBuilder, r *DecideSchema) { setS(&b.Main, &r.Name, "  ") }},
			{"name not UTF-8", "name", "malformed", func(b *DecideSchemaBuilder, r *DecideSchema) { TableStringAt(&r.Name, &b.Arena).Data[0] = 0xff }},
			{"no questions", "questions", "absent_required", func(_ *DecideSchemaBuilder, r *DecideSchema) { r.Questions.Count = 0 }},
			{"a question's type none", "kind", "clamped", func(b *DecideSchemaBuilder, r *DecideSchema) {
				r.Questions.At(0, &b.Arena).Question.Kind = AnswerTypeNone
			}},
			{"a question without instructions", "instructions", "absent_required", func(b *DecideSchemaBuilder, r *DecideSchema) {
				r.Questions.At(1, &b.Arena).Question.Instructions = 0
			}},
			{"a choice with one option", "criteria", "clamped", func(b *DecideSchemaBuilder, r *DecideSchema) {
				eraseCriteria(&b.Arena, &r.Questions.At(0, &b.Arena).Question, 2)
			}},
			{"a noul with an option", "criteria", "clamped", func(b *DecideSchemaBuilder, r *DecideSchema) {
				fillCriterion(&b.Main, QuestionCriteriaAdd(&b.Main, &r.Questions.At(2, &b.Arena).Question.Criteria), 0)
			}},
			{"a question name twice", "name", "malformed", func(b *DecideSchemaBuilder, r *DecideSchema) {
				setS(&b.Main, &r.Questions.At(1, &b.Arena).Name, "choice-0")
			}},
		})
		// A schema of any size: one question, two hundred, two hundred options to
		// one, and instructions of 1 MiB (the code bounds none: decide.go:101).
		for _, c := range []struct{ choices, options, instr int }{{1, 2, 40}, {199, 3, 100}, {1, 200, 64 << 10}, {3, 10, 1 << 20}} {
			wholeIn(t, decideSchema, fmt.Sprintf("%d choices of %d options, instructions %d", c.choices, c.options, c.instr), func(b *DecideSchemaBuilder, r *DecideSchema) {
				fillDecideSchema(&b.Main, r, c.choices, c.options, c.instr)
			}, func(t *testing.T, got *DecideSchemaBuilder, r *DecideSchema) {
				require.EqualValues(t, c.choices+1, r.Questions.Count)
				first := r.Questions.At(0, &got.Arena)
				assert.EqualValues(t, c.options, first.Question.Criteria.Count)
				assert.Len(t, strAt(&first.Question.Instructions, &got.Arena), c.instr)
				last := r.Questions.At(r.Questions.Count-1, &got.Arena)
				assert.Equal(t, "noul-0", strAt(&last.Name, &got.Arena))
				assert.EqualValues(t, 0, last.Question.Criteria.Count)
			})
		}
	})
	t.Run("JevRequest", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, jevRequest, func(b *JevRequestBuilder, r *JevRequest) { fillJevRequest(&b.Main, r, 2, 3, 80) }, []vbound[JevRequest, *JevRequestBuilder]{
			{"state empty", "state", "absent_required", func(_ *JevRequestBuilder, r *JevRequest) { r.State = 0 }},
			{"state blank", "state", "absent_required", func(b *JevRequestBuilder, r *JevRequest) { setS(&b.Main, &r.State, "\n") }},
			{"model not UTF-8", "model", "malformed", func(b *JevRequestBuilder, r *JevRequest) { TableStringAt(&r.Model, &b.Arena).Data[0] = 0xff }},
			{"a question's type none", "kind", "clamped", func(b *JevRequestBuilder, r *JevRequest) { r.Questions.At(0, &b.Arena).Question.Kind = AnswerTypeNone }},
			{"a question name twice", "name", "malformed", func(b *JevRequestBuilder, r *JevRequest) {
				setS(&b.Main, &r.Questions.At(1, &b.Arena).Name, "choice-0")
			}},
		})
		for _, c := range []struct{ choices, options, state int }{{1, 2, 40}, {199, 3, 1 << 10}, {1, 200, 1 << 20}} {
			wholeIn(t, jevRequest, fmt.Sprintf("%d choices of %d options, state %d", c.choices, c.options, c.state), func(b *JevRequestBuilder, r *JevRequest) {
				fillJevRequest(&b.Main, r, c.choices, c.options, c.state)
			}, func(t *testing.T, got *JevRequestBuilder, r *JevRequest) {
				require.EqualValues(t, c.choices+1, r.Questions.Count)
				assert.EqualValues(t, c.options, r.Questions.At(0, &got.Arena).Question.Criteria.Count)
				assert.Len(t, strAt(&r.State, &got.Arena), c.state)
			})
		}
	})
	t.Run("JevAnswer", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, jevAnswer, func(b *JevAnswerBuilder, r *JevAnswer) { fillJevAnswer(&b.Main, r, 3) }, []vbound[JevAnswer, *JevAnswerBuilder]{
			{"type not UTF-8", "kind", "malformed", func(b *JevAnswerBuilder, r *JevAnswer) { TableStringAt(&r.Kind, &b.Arena).Data[0] = 0xff }},
			{"choice not UTF-8", "choice", "malformed", func(b *JevAnswerBuilder, r *JevAnswer) { TableStringAt(&r.Choice, &b.Arena).Data[0] = 0xff }},
			{"a probability above one", "p", "clamped", func(b *JevAnswerBuilder, r *JevAnswer) { r.Probabilities.At(1, &b.Arena).P = 2 }},
			{"a noul above one", "noul", "clamped", func(_ *JevAnswerBuilder, r *JevAnswer) { r.Noul = 1.5 }},
			{"a noul not a number", "noul", "malformed", func(_ *JevAnswerBuilder, r *JevAnswer) { r.Noul = math.NaN() }},
		})
		fill := func(b *JevAnswerBuilder, r *JevAnswer) { fillJevAnswer(&b.Main, r, 2) }
		optionalFloats(t, jevAnswer, fill, func(r *JevAnswer, present bool) *bool { r.NoulPresent = present; r.Noul = 0; return &r.NoulPresent })
		optionalFloats(t, jevAnswer, fill, func(r *JevAnswer, present bool) *bool {
			r.ConfidencePresent = present
			r.Confidence = 0
			return &r.ConfidencePresent
		})
		for _, probs := range []int{0, 2, 200} {
			wholeIn(t, jevAnswer, fmt.Sprintf("%d probabilities", probs), func(b *JevAnswerBuilder, r *JevAnswer) { fillJevAnswer(&b.Main, r, probs) },
				func(t *testing.T, _ *JevAnswerBuilder, r *JevAnswer) {
					assert.EqualValues(t, probs, r.Probabilities.Count)
				})
		}
	})
	t.Run("NamedJevAnswer", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, namedJevAnswer, func(b *NamedJevAnswerBuilder, r *NamedJevAnswer) { fillNamedJevAnswer(&b.Main, r, "class", 2) }, []vbound[NamedJevAnswer, *NamedJevAnswerBuilder]{
			{"question not UTF-8", "question", "malformed", func(b *NamedJevAnswerBuilder, r *NamedJevAnswer) { TableStringAt(&r.Question, &b.Arena).Data[0] = 0xff }},
			{"the answer's noul above one", "noul", "clamped", func(_ *NamedJevAnswerBuilder, r *NamedJevAnswer) { r.Answer.Noul = 1.5 }},
		})
	})
	t.Run("JevResponse", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, jevResponse, func(b *JevResponseBuilder, r *JevResponse) { fillJevResponse(&b.Main, r, 2, 3) }, []vbound[JevResponse, *JevResponseBuilder]{
			{"an answer's probability above one", "p", "clamped", func(b *JevResponseBuilder, r *JevResponse) {
				r.Answers.At(1, &b.Arena).Answer.Probabilities.At(0, &b.Arena).P = 1.2
			}},
			{"an answer's question not UTF-8", "question", "malformed", func(b *JevResponseBuilder, r *JevResponse) {
				TableStringAt(&r.Answers.At(0, &b.Arena).Question, &b.Arena).Data[0] = 0xff
			}},
		})
		// The backend answers any number of questions of any number of options.
		for _, c := range []struct{ answers, probs int }{{0, 0}, {1, 2}, {200, 3}, {3, 200}} {
			wholeIn(t, jevResponse, fmt.Sprintf("%d answers of %d probabilities", c.answers, c.probs), func(b *JevResponseBuilder, r *JevResponse) {
				fillJevResponse(&b.Main, r, c.answers, c.probs)
			}, func(t *testing.T, got *JevResponseBuilder, r *JevResponse) {
				require.EqualValues(t, c.answers, r.Answers.Count)
				assert.Equal(t, sampleUsage(), r.Usage)
				if c.answers > 0 {
					assert.EqualValues(t, c.probs, r.Answers.At(r.Answers.Count-1, &got.Arena).Answer.Probabilities.Count)
				}
			})
		}
	})
	t.Run("FixedAnswer", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, fixedAnswer, func(b *FixedAnswerBuilder, r *FixedAnswer) { fillFixedAnswer(&b.Main, r, 3) }, []vbound[FixedAnswer, *FixedAnswerBuilder]{
			{"choice not UTF-8", "choice", "malformed", func(b *FixedAnswerBuilder, r *FixedAnswer) { TableStringAt(&r.Choice, &b.Arena).Data[0] = 0xff }},
			{"an option empty", "option", "absent_required", func(b *FixedAnswerBuilder, r *FixedAnswer) { r.P.At(0, &b.Arena).Option = 0 }},
			{"a probability above one", "p", "clamped", func(b *FixedAnswerBuilder, r *FixedAnswer) { r.P.At(2, &b.Arena).P = 1.1 }},
			{"a noul below zero", "noul", "clamped", func(_ *FixedAnswerBuilder, r *FixedAnswer) { r.Noul = -0.5 }},
			{"a noul not a number", "noul", "malformed", func(_ *FixedAnswerBuilder, r *FixedAnswer) { r.Noul = math.NaN() }},
		})
		optionalFloats(t, fixedAnswer, func(b *FixedAnswerBuilder, r *FixedAnswer) { fillFixedAnswer(&b.Main, r, 2) },
			func(r *FixedAnswer, present bool) *bool { r.NoulPresent = present; r.Noul = 0; return &r.NoulPresent })
		for _, options := range []int{0, 2, 200} {
			wholeIn(t, fixedAnswer, fmt.Sprintf("%d options", options), func(b *FixedAnswerBuilder, r *FixedAnswer) { fillFixedAnswer(&b.Main, r, options) },
				func(t *testing.T, _ *FixedAnswerBuilder, r *FixedAnswer) { assert.EqualValues(t, options, r.P.Count) })
		}
	})
	t.Run("NamedFixedAnswer", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, namedFixedAnswer, func(b *NamedFixedAnswerBuilder, r *NamedFixedAnswer) { fillNamedFixedAnswer(&b.Main, r, 2) }, []vbound[NamedFixedAnswer, *NamedFixedAnswerBuilder]{
			{"question not UTF-8", "question", "malformed", func(b *NamedFixedAnswerBuilder, r *NamedFixedAnswer) {
				TableStringAt(&r.Question, &b.Arena).Data[0] = 0xff
			}},
			{"the row's noul above one", "noul", "clamped", func(_ *NamedFixedAnswerBuilder, r *NamedFixedAnswer) { r.Answer.Noul = 2 }},
		})
	})
	t.Run("FixedTable", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, fixedTable, func(b *FixedTableBuilder, r *FixedTable) { fillFixedTable(&b.Main, r, 2, 2) }, []vbound[FixedTable, *FixedTableBuilder]{
			{"a row's probability above one", "p", "clamped", func(b *FixedTableBuilder, r *FixedTable) { r.Answers.At(1, &b.Arena).Answer.P.At(0, &b.Arena).P = 3 }},
		})
		for _, c := range []struct{ answers, options int }{{0, 0}, {1, 2}, {200, 3}, {2, 200}} {
			wholeIn(t, fixedTable, fmt.Sprintf("%d rows of %d options", c.answers, c.options), func(b *FixedTableBuilder, r *FixedTable) {
				fillFixedTable(&b.Main, r, c.answers, c.options)
			}, func(t *testing.T, got *FixedTableBuilder, r *FixedTable) {
				require.EqualValues(t, c.answers, r.Answers.Count)
				if c.answers > 0 {
					assert.EqualValues(t, c.options, r.Answers.At(r.Answers.Count-1, &got.Arena).Answer.P.Count)
				}
			})
		}
	})
	t.Run("Example", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, example, func(b *ExampleBuilder, r *Example) { fillExample(&b.Main, r, 60) }, []vbound[Example, *ExampleBuilder]{
			{"card empty", "card", "absent_required", func(_ *ExampleBuilder, r *Example) { r.Card = 0 }},
			{"heading empty", "heading", "absent_required", func(_ *ExampleBuilder, r *Example) { r.Heading = 0 }},
			{"label none", "label", "clamped", func(_ *ExampleBuilder, r *Example) { r.Label = ExampleLabelNone }},
			{"label past heavy", "label", "clamped", func(_ *ExampleBuilder, r *Example) { r.Label = ExampleLabel(4) }},
			{"paths not UTF-8", "paths", "malformed", func(b *ExampleBuilder, r *Example) { TableStringAt(&r.Paths, &b.Arena).Data[0] = 0xff }},
		})
		for _, label := range []ExampleLabel{ExampleLabelFlash, ExampleLabelPro, ExampleLabelHeavy} {
			wholeIn(t, example, fmt.Sprintf("label %d", label), func(b *ExampleBuilder, r *Example) {
				fillExample(&b.Main, r, 1<<20)
				r.Label = label
			}, func(t *testing.T, got *ExampleBuilder, r *Example) {
				assert.Equal(t, label, r.Label)
				assert.Len(t, strAt(&r.Paths, &got.Arena), 1<<20)
			})
		}
	})
	t.Run("SetEntry", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, setEntry, func(b *SetEntryBuilder, r *SetEntry) { fillSetEntry(&b.Main, r, 0, 40) }, []vbound[SetEntry, *SetEntryBuilder]{
			{"key not UTF-8", "key", "malformed", func(b *SetEntryBuilder, r *SetEntry) { TableStringAt(&r.Key, &b.Arena).Data[0] = 0xff }},
			{"value not UTF-8", "value", "malformed", func(b *SetEntryBuilder, r *SetEntry) { TableStringAt(&r.Value, &b.Arena).Data[0] = 0xff }},
		})
	})
	t.Run("LogLine", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, logLine, func(b *LogLineBuilder, r *LogLine) { fillLogLine(&b.Main, r, 3, 40) }, []vbound[LogLine, *LogLineBuilder]{
			{"card not UTF-8", "card", "malformed", func(b *LogLineBuilder, r *LogLine) { TableStringAt(&r.Card, &b.Arena).Data[0] = 0xff }},
			{"table not UTF-8", "table_name", "malformed", func(b *LogLineBuilder, r *LogLine) { TableStringAt(&r.TableName, &b.Arena).Data[0] = 0xff }},
			{"a set key not UTF-8", "key", "malformed", func(b *LogLineBuilder, r *LogLine) {
				TableStringAt(&r.Set.At(2, &b.Arena).Key, &b.Arena).Data[0] = 0xff
			}},
		})
		// Primary empty and removed false are lines the code reads (ReadLog skips the
		// first); a line with no set, 200 and one 1 MiB value all round-trip.
		for _, c := range []struct{ sets, value int }{{0, 0}, {1, 1 << 20}, {200, 30}} {
			wholeIn(t, logLine, fmt.Sprintf("%d set entries of %d bytes", c.sets, c.value), func(b *LogLineBuilder, r *LogLine) {
				fillLogLine(&b.Main, r, c.sets, c.value)
				r.Primary, r.Removed = 0, false
			}, func(t *testing.T, got *LogLineBuilder, r *LogLine) {
				require.EqualValues(t, c.sets, r.Set.Count)
				assert.False(t, r.Removed)
				assert.Zero(t, r.Primary)
				if c.sets > 0 {
					assert.Len(t, strAt(&r.Set.At(r.Set.Count-1, &got.Arena).Value, &got.Arena), c.value)
				}
			})
		}
	})
	t.Run("LogExport", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, logExport, func(b *LogExportBuilder, r *LogExport) { fillLogExport(&b.Main, r, 2, 2, 40) }, []vbound[LogExport, *LogExportBuilder]{
			{"a line's card not UTF-8", "card", "malformed", func(b *LogExportBuilder, r *LogExport) {
				TableStringAt(&r.Lines.At(1, &b.Arena).Card, &b.Arena).Data[0] = 0xff
			}},
		})
		for _, c := range []struct{ lines, sets, value int }{{0, 0, 0}, {1, 1, 1 << 20}, {200, 3, 100}, {2, 200, 30}} {
			wholeIn(t, logExport, fmt.Sprintf("%d lines of %d set entries", c.lines, c.sets), func(b *LogExportBuilder, r *LogExport) {
				fillLogExport(&b.Main, r, c.lines, c.sets, c.value)
			}, func(t *testing.T, got *LogExportBuilder, r *LogExport) {
				require.EqualValues(t, c.lines, r.Lines.Count)
				if c.lines > 0 {
					last := r.Lines.At(r.Lines.Count-1, &got.Arena)
					assert.EqualValues(t, c.sets, last.Set.Count)
				}
			})
		}
	})
	t.Run("ImportExport", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, importExport, func(b *ImportExportBuilder, r *ImportExport) { fillImportExport(&b.Main, r, 2, 2, 0) }, []vbound[ImportExport, *ImportExportBuilder]{
			{"a line's verb not UTF-8", "verb", "malformed", func(b *ImportExportBuilder, r *ImportExport) {
				TableStringAt(&r.Lines.At(1, &b.Arena).Verb, &b.Arena).Data[0] = 0xff
			}},
			{"a line's answer id empty", "id", "absent_required", func(b *ImportExportBuilder, r *ImportExport) { r.Lines.At(0, &b.Arena).Answers.At(1, &b.Arena).Id = 0 }},
		})
		// The import takes every line of the export and every id of a line
		// (import.go:161-176): none, one, two hundred lines, two hundred ids to a
		// line and a verb of 1 MiB round-trip in every form.
		for _, c := range []struct{ lines, ids, verb int }{{0, 0, 0}, {1, 1, 0}, {200, 3, 0}, {2, 200, 0}, {1, 1, 1 << 20}} {
			wholeIn(t, importExport, fmt.Sprintf("%d lines of %d ids", c.lines, c.ids), func(b *ImportExportBuilder, r *ImportExport) {
				fillImportExport(&b.Main, r, c.lines, c.ids, c.verb)
			}, func(t *testing.T, got *ImportExportBuilder, r *ImportExport) {
				require.EqualValues(t, c.lines, r.Lines.Count)
				if c.lines > 0 {
					last := r.Lines.At(r.Lines.Count-1, &got.Arena)
					assert.EqualValues(t, c.ids, last.Answers.Count)
					if c.verb > 0 {
						assert.Len(t, strAt(&last.Verb, &got.Arena), c.verb)
					}
				}
			})
		}
	})

	t.Run("Bars", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, bars, func(_ *plain[Bars], r *Bars) { *r = Bars{Bounce: 0.8, Review: 0.3} }, plainBounds([]pbound[Bars]{
			{"bounce above one", "bounce", "clamped", func(v *Bars) { v.Bounce = 1.5 }},
			{"review below zero", "review", "clamped", func(v *Bars) { v.Review = -0.1 }},
			{"review not a number", "review", "malformed", func(v *Bars) { v.Review = math.NaN() }},
			{"review above bounce", "review", "clamped", func(v *Bars) { v.Review = 0.9 }},
		}))
		for _, c := range []Bars{{0, 0}, {1, 1}, {0.5, 0.5}} {
			wholeIn(t, bars, fmt.Sprintf("%v", c), func(_ *plain[Bars], r *Bars) { *r = c },
				func(t *testing.T, _ *plain[Bars], r *Bars) { assert.Equal(t, c, *r) })
		}
	})
	t.Run("GateBars", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, gateBars, func(_ *plain[GateBars], r *GateBars) { *r = GateBars{Flaky: 0.6, PreExisting: 0.6} }, plainBounds([]pbound[GateBars]{
			{"flaky above one and not Unset", "flaky", "clamped", func(v *GateBars) { v.Flaky = 1.5 }},
			{"pre-existing below zero", "pre_existing", "clamped", func(v *GateBars) { v.PreExisting = -0.2 }},
			{"flaky not a number", "flaky", "malformed", func(v *GateBars) { v.Flaky = math.NaN() }},
			{"two set bars summing to one", "flaky", "clamped", func(v *GateBars) { v.Flaky, v.PreExisting = 0.5, 0.5 }},
		}))
		// An Unset bar (2.0, gate.go) is a bar the sprint row leaves empty: allowed
		// with a set bar of any value and with another Unset.
		for _, c := range []GateBars{{gateBarUnset, gateBarUnset}, {gateBarUnset, 0.4}, {0.1, gateBarUnset}, {0.9, 0.9}} {
			wholeIn(t, gateBars, fmt.Sprintf("%v", c), func(_ *plain[GateBars], r *GateBars) { *r = c },
				func(t *testing.T, _ *plain[GateBars], r *GateBars) { assert.Equal(t, c, *r) })
		}
	})
	t.Run("GateLine", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, gateLine, func(b *GateLineBuilder, r *GateLine) { fillGateLine(&b.Main, r, 0, 40) }, []vbound[GateLine, *GateLineBuilder]{
			{"text not UTF-8", "text", "malformed", func(b *GateLineBuilder, r *GateLine) { TableStringAt(&r.Text, &b.Arena).Data[0] = 0xff }},
		})
	})
	t.Run("Failure", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, failure, func(b *FailureBuilder, r *Failure) { fillFailure(&b.Main, r, 3, 60) }, []vbound[Failure, *FailureBuilder]{
			{"package not UTF-8", "pkg", "malformed", func(b *FailureBuilder, r *Failure) { TableStringAt(&r.Pkg, &b.Arena).Data[0] = 0xff }},
			{"test not UTF-8", "test", "malformed", func(b *FailureBuilder, r *Failure) { TableStringAt(&r.Test, &b.Arena).Data[0] = 0xff }},
			{"a line not UTF-8", "text", "malformed", func(b *FailureBuilder, r *Failure) {
				TableStringAt(&r.Lines.At(2, &b.Arena).Text, &b.Arena).Data[0] = 0xff
			}},
		})
		// A build failure has no test and no lines; a failure keeps any number of
		// lines of any length (gate.go:66 bounds none).
		for _, c := range []struct{ lines, bytes int }{{0, 0}, {1, 1 << 20}, {200, 80}} {
			wholeIn(t, failure, fmt.Sprintf("%d lines of %d bytes", c.lines, c.bytes), func(b *FailureBuilder, r *Failure) {
				fillFailure(&b.Main, r, c.lines, c.bytes)
				if c.lines == 0 {
					r.Test = 0
				}
			}, func(t *testing.T, got *FailureBuilder, r *Failure) {
				require.EqualValues(t, c.lines, r.Lines.Count)
				if c.lines > 0 {
					assert.Len(t, strAt(&r.Lines.At(r.Lines.Count-1, &got.Arena).Text, &got.Arena), c.bytes)
				} else {
					assert.Zero(t, r.Test)
				}
			})
		}
	})
	t.Run("Chosen", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, chosen, func(b *ChosenBuilder, r *Chosen) { fillChosen(&b.Main, r, 60) }, []vbound[Chosen, *ChosenBuilder]{
			{"verb not UTF-8", "verb", "malformed", func(b *ChosenBuilder, r *Chosen) { TableStringAt(&r.Verb, &b.Arena).Data[0] = 0xff }},
			{"why not UTF-8", "why", "malformed", func(b *ChosenBuilder, r *Chosen) { TableStringAt(&r.Why, &b.Arena).Data[0] = 0xff }},
		})
		// An applied choice carries no why, and a drop no fix: absent text and a long
		// why both round-trip in every form.
		wholeIn(t, chosen, "an applied choice", func(b *ChosenBuilder, r *Chosen) {
			setS(&b.Main, &r.Verb, "accept")
			r.P = 1
			setS(&b.Main, &r.Act, "apply")
		}, func(t *testing.T, got *ChosenBuilder, r *Chosen) {
			assert.Equal(t, "apply", strAt(&r.Act, &got.Arena))
			assert.Zero(t, r.Fix)
			assert.Zero(t, r.Why)
		})
		wholeIn(t, chosen, "a listed choice with a 1 MiB why", func(b *ChosenBuilder, r *Chosen) { fillChosen(&b.Main, r, 1<<20) },
			func(t *testing.T, got *ChosenBuilder, r *Chosen) {
				assert.Len(t, strAt(&r.Why, &got.Arena), 1<<20)
				assert.Equal(t, 0.875, r.P)
			})
	})
	t.Run("ClusterCard", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, clusterCard, func(b *ClusterCardBuilder, r *ClusterCard) { setS(&b.Main, &r.Card, "card-0001") }, []vbound[ClusterCard, *ClusterCardBuilder]{
			{"card not UTF-8", "card", "malformed", func(b *ClusterCardBuilder, r *ClusterCard) { TableStringAt(&r.Card, &b.Arena).Data[0] = 0xff }},
		})
	})
	t.Run("Cluster", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, cluster, func(b *ClusterBuilder, r *Cluster) { fillCluster(&b.Main, r, 3) }, []vbound[Cluster, *ClusterBuilder]{
			{"class not UTF-8", "class_name", "malformed", func(b *ClusterBuilder, r *Cluster) { TableStringAt(&r.ClassName, &b.Arena).Data[0] = 0xff }},
			{"a card not UTF-8", "card", "malformed", func(b *ClusterBuilder, r *Cluster) {
				TableStringAt(&r.Cards.At(1, &b.Arena).Card, &b.Arena).Data[0] = 0xff
			}},
		})
		for _, n := range []int{0, 1, 200} {
			wholeIn(t, cluster, fmt.Sprintf("%d cards", n), func(b *ClusterBuilder, r *Cluster) { fillCluster(&b.Main, r, n) },
				func(t *testing.T, got *ClusterBuilder, r *Cluster) {
					require.EqualValues(t, n, r.Cards.Count)
					assert.EqualValues(t, n, r.Count)
					if n > 0 {
						assert.Equal(t, fmt.Sprintf("card-%04d", n-1), strAt(&r.Cards.At(r.Cards.Count-1, &got.Arena).Card, &got.Arena))
					}
				})
		}
	})

	t.Run("the committed shape is pinned", func(t *testing.T) {
		t.Parallel()
		// tables.baseline holds every table's shape and schema.lock the fixed
		// layouts: a changed table changes their hashes, and these lines change
		// with it, so a shape change is never made without this test knowing.
		for file, want := range map[string]string{"tables.baseline": "5fb14fcd2269a95b7d2b2b08da2c82ce56fe3d172a5cd3a0e55d72c263ed7625", "schema.lock": "49f05a6811d1150adb11c93cd41f2e816af07ed2b6a3f00192d8fcc7bfcc9425"} {
			raw, err := os.ReadFile(filepath.Join("..", "..", "schemas", "decide", file))
			require.NoError(t, err)
			sum := sha256.Sum256(raw)
			assert.Equal(t, want, hex.EncodeToString(sum[:]), file)
		}
	})
}

// measureWire is the generated measure-with-reason over the wire, when the
// table has one (a table read through a builder does).
func (f varFns[R, B, PB]) measureWire() func([]byte) error {
	if f.measureReason == nil {
		return nil
	}
	return func(wire []byte) error {
		_, err := f.measureReason(wire)
		return err
	}
}

// plain is the holder of a table read and written by value (a fixed-class
// table has no builder): it gives such a table the shape exerciseVar drives.
type plain[R any] struct{ v R }

func (p *plain[R]) Init(...TableAllocator) bool { return true }
func (p *plain[R]) GetRoot() *R                 { return &p.v }
func (p *plain[R]) AsConst() *R                 { return &p.v }
func (p *plain[R]) Lock() bool                  { return true }
func (p *plain[R]) Shutdown()                   {}

// plainFns are the generated by-value functions of one fixed-class table.
func plainFns[R any](name string,
	load, fromJSON func(*R, []byte, *TableReport) bool,
	measure func(*R) int64, save func(*R, []byte) int64,
	jsonMeasure func(*R) int64, toJSON func(*R, []byte) int64,
	rules func(*R, chk)) varFns[R, plain[R], *plain[R]] {
	return varFns[R, plain[R], *plain[R]]{
		name:        name,
		load:        func(b *plain[R], wire []byte, rep *TableReport) bool { return load(&b.v, wire, rep) },
		fromJSON:    func(b *plain[R], text []byte, rep *TableReport) bool { return fromJSON(&b.v, text, rep) },
		measure:     func(r *R, _ ...TableWriteContext) int64 { return measure(r) },
		save:        func(r *R, buf []byte, _ ...TableWriteContext) int64 { return save(r, buf) },
		jsonMeasure: func(r *R, _ ...TableAllocator) int64 { return jsonMeasure(r) },
		toJSON:      func(r *R, buf []byte, _ ...TableAllocator) int64 { return toJSON(r, buf) },
		rules:       func(_ *plain[R], r *R, k chk) { rules(r, k) },
	}
}

// pbound is a value past one declared bound of a by-value table.
type pbound[R any] struct {
	name, field, reason string
	mutate              func(*R)
}

func plainBounds[R any](bs []pbound[R]) []vbound[R, *plain[R]] {
	out := make([]vbound[R, *plain[R]], len(bs))
	for i, b := range bs {
		m := b.mutate
		out[i] = vbound[R, *plain[R]]{b.name, b.field, b.reason, func(_ *plain[R], r *R) { m(r) }}
	}
	return out
}
