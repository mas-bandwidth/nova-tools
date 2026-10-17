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
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

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

// rawUsageRules: version 1 and at least one record (raw.go:47), at most 270
// (MaxRawBytes, raw.go:21, over a 120-byte smallest record).
func rawUsageRules(v *RawUsage, k chk) {
	k.span("version", int64(v.Version), 1, 1)
	k.c.Required("records", v.RecordsCount > 0)
	k.span("records", int64(v.RecordsCount), 0, int64(len(v.Records)))
	for i := 0; i < int(v.RecordsCount) && i < len(v.Records); i++ {
		rawRecordRules(&v.Records[i], k)
	}
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
		fillRawRecord(w, &r.Records[i])
	}
	r.RecordsCount = int32(records)
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
			{"no records", "records", "absent_required", func(_ *RawUsageBuilder, r *RawUsage) { r.RecordsCount = 0 }},
			{"271 records", "records", "clamped", func(_ *RawUsageBuilder, r *RawUsage) { r.RecordsCount = 271 }},
			{"a record's harness not muse", "harness", "malformed", func(b *RawUsageBuilder, r *RawUsage) { setS(&b.Main, &r.Records[1].Harness, "x") }},
		})
		// The most records the envelope's byte cap allows.
		rd := schemaread.NewReader(raw.codec())
		w := raw.fresh()
		fillRawUsage(&w.Main, w.GetRoot(), 270)
		for _, form := range allForms {
			image, err := rd.Write(&w, form)
			require.NoError(t, err)
			got, _, err := rd.Read(image)
			require.NoError(t, err)
			assert.EqualValues(t, 270, raw.root(got).RecordsCount)
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

	t.Run("the committed shape is pinned", func(t *testing.T) {
		t.Parallel()
		// tables.baseline holds every table's shape and schema.lock the fixed
		// layouts: a changed table changes their hashes, and these lines change
		// with it, so a shape change is never made without this test knowing.
		for file, want := range map[string]string{"tables.baseline": "42dd57dbd57407a33ce51aa7a64acecc951fcad16e5c1ed0e8f4ab8a5a071a3b", "schema.lock": "43d79da38b3bcd806ae25159d28acb6b90571e085490541906fd840e43e73274"} {
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
