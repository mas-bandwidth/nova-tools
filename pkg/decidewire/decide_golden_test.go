package decidewire

// TestDecideRecordsGolden is the golden test of the decide definitions
// (schemas/decide): every table the area declares round-trips as the fixed
// form's binary wire and as padded standard base64 of that wire, each read
// strictly through pkg/schemaread (S1, S2); a value past each declared bound,
// and a damaged image of each form, is refused by name and never clamped; the
// reader counts each read on the form it arrived in; and the wire's layout
// hash of every table is pinned, so a change to a table's shape is a change to
// this file.
//
// The facade here is the strict read and write over the generated functions.
// The generated fixed writer refuses a length past its capacity but checks no
// range, and the generated reader clamps and counts: the facade refuses every
// clamp and states each rule the Go struct enforces today once, for the reader
// and the writer (UNION.md section 4.6). The rules and their sources are the
// comments of schemas/decide/decide.schema.

import (
	"bytes"
	"encoding/base64"
	"math"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const decideHelp = "nova-tools decide -h"

// fixedFns are the generated functions of one fixed table and the rules its
// facade holds.
type fixedFns[T any] struct {
	name    string
	reset   func(*T)
	measure func(int64) int64
	save    func([]T, []byte) int64
	load    func([]T, []byte, []TableFixedEntry, *TableReport) int64
	planMax int32
	body    int
	hash    uint64
	rules   func(*T, chk)
}

// codec is the strict read and write over the generated fixed functions
// (S2): the reader refuses what the permissive load only counts.
func (f fixedFns[T]) codec() *schemaread.Codec[T] {
	return &schemaread.Codec[T]{
		Name:  strings.ToUpper(f.name),
		Help:  decideHelp,
		Reset: f.reset,
		Forms: []byte{TableFixedForm},
		LoadWire: func(v *T, wire []byte) schemaread.Report {
			vals := make([]T, 1)
			plan := make([]TableFixedEntry, f.planMax)
			var rep TableReport
			n := f.load(vals, wire, plan, &rep)
			if n != 1 {
				reason := rep.Reason
				if reason == "" {
					reason = schemaread.ReasonWireDamaged
				}
				return schemaread.Report{Reason: reason}
			}
			*v = vals[0]
			return schemaread.Report{Unknown: int(rep.Unknown), KindMismatch: int(rep.KindMismatch), Clamped: int(rep.Clamped), Malformed: rep.Malformed}
		},
		Constraints: func(v *T, c *schemaread.Checker) { f.rules(v, chk{c}) },
		SaveWire: func(v *T) []byte {
			buf := make([]byte, f.measure(1))
			if f.save([]T{*v}, buf) != int64(len(buf)) {
				return nil
			}
			return buf
		},
	}
}

// chk states the facade's rules over a schemaread.Checker.
type chk struct{ c *schemaread.Checker }

// textOf is a counted string field's text; a length past the buffer is read
// whole so the bound check sees it.
func textOf(buf []byte, n int32) string {
	if n < 0 {
		return ""
	}
	if int(n) > len(buf) {
		return string(buf) + strings.Repeat("?", int(n)-len(buf))
	}
	return string(buf[:n])
}

// str holds a counted string to its declared capacity (the bound), UTF-8 and
// no NUL (S2: refused by name).
func (k chk) str(field string, buf []byte, n int32) {
	k.c.Bound(field, int64(n), 0, int64(len(buf)))
	k.c.Text(field, textOf(buf, n), len(buf))
}

// req refuses an empty counted string where the struct's reader refuses it.
func (k chk) req(field string, n int32) { k.c.Required(field, n > 0) }

// count holds a counted array to its declared bound.
func (k chk) count(field string, n int32, max int) { k.c.Bound(field, int64(n), 0, int64(max)) }

// span holds a value to [lo, hi].
func (k chk) span(field string, v, lo, hi int64) { k.c.Bound(field, v, lo, hi) }

// prob holds a probability to [0, 1]: pkg/decide Schema.Check and
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

// is refuses a field that is not the one value the code accepts (raw.go
// Validate).
func (k chk) is(field, got, want string) {
	if got != want {
		k.c.Text(field, "\x00", 1)
	}
}

// bare refuses a text holding whitespace: ParseDecided splits on it.
func (k chk) bare(field, s string) {
	if strings.ContainsAny(s, " \t\r\n") {
		k.c.Text(field, "\x00", 1)
	}
}

func s(buf []byte, n int32) string { return textOf(buf, n) }

func put(dst []byte, n *int32, text string) { *n = int32(copy(dst, text)) }

// ---- the rules, one function a table ----

func usageRules(v *Usage, k chk) {
	k.span("input_tokens", int64(v.InputTokens), 0, math.MaxInt32)
	k.span("output_tokens", int64(v.OutputTokens), 0, math.MaxInt32)
}

func optionPRules(v *OptionP, k chk) {
	k.str("option", v.Option[:], v.OptionLength)
	k.req("option", v.OptionLength)
	k.prob("p", v.P)
}

func answerRules(v *Answer, k chk) {
	k.span("kind", int64(v.Kind), 1, 2)
	k.str("value", v.Value[:], v.ValueLength)
	k.req("value", v.ValueLength)
	k.count("p", v.PCount, len(v.P))
	for i := 0; i < int(v.PCount) && i < len(v.P); i++ {
		optionPRules(&v.P[i], k)
	}
	k.str("method", v.Method[:], v.MethodLength)
}

func namedAnswerRules(v *NamedAnswer, k chk) {
	k.str("question", v.Question[:], v.QuestionLength)
	k.req("question", v.QuestionLength)
	answerRules(&v.Answer, k)
}

func inputRules(v *Input, k chk) {
	k.str("key", v.Key[:], v.KeyLength)
	k.req("key", v.KeyLength)
	k.str("value", v.Value[:], v.ValueLength)
}

func decidedRules(v *Decided, k chk) {
	k.str("value", v.Value[:], v.ValueLength)
	k.req("value", v.ValueLength)
	k.bare("value", s(v.Value[:], v.ValueLength))
	k.prob("p", v.P)
	k.str("op", v.Op[:], v.OpLength)
	k.req("op", v.OpLength)
	k.bare("op", s(v.Op[:], v.OpLength))
}

func decisionRules(v *Decision, k chk) {
	k.str("id", v.Id[:], v.IdLength)
	k.req("id", v.IdLength)
	k.str("decision_name", v.DecisionName[:], v.DecisionNameLength)
	k.req("decision_name", v.DecisionNameLength)
	k.str("schema", v.Schema[:], v.SchemaLength)
	k.str("backend", v.Backend[:], v.BackendLength)
	k.str("at", v.At[:], v.AtLength)
	k.count("inputs", v.InputsCount, len(v.Inputs))
	for i := 0; i < int(v.InputsCount) && i < len(v.Inputs); i++ {
		inputRules(&v.Inputs[i], k)
	}
	k.str("state", v.State[:], v.StateLength)
	k.count("answers", v.AnswersCount, len(v.Answers))
	for i := 0; i < int(v.AnswersCount) && i < len(v.Answers); i++ {
		namedAnswerRules(&v.Answers[i], k)
	}
	usageRules(&v.Usage, k)
}

func outcomeRules(v *Outcome, k chk) {
	k.str("id", v.Id[:], v.IdLength)
	k.req("id", v.IdLength)
	k.str("label", v.Label[:], v.LabelLength)
	k.req("label", v.LabelLength)
	k.str("note", v.Note[:], v.NoteLength)
	k.str("at", v.At[:], v.AtLength)
}

func actRules(v *Act, k chk) {
	k.str("id", v.Id[:], v.IdLength)
	k.req("id", v.IdLength)
	k.str("step", v.Step[:], v.StepLength)
	k.req("step", v.StepLength)
	k.str("op", v.Op[:], v.OpLength)
	k.str("at", v.At[:], v.AtLength)
}

// recordLineRules: a line is one record (record.go parse), the one kind names.
func recordLineRules(v *RecordLine, k chk) {
	k.span("kind", int64(v.Kind), 1, 3)
	present := map[LineKind]bool{LineKindDecision: v.AsDecisionPresent, LineKindOutcome: v.AsOutcomePresent, LineKindAct: v.AsActPresent}
	k.c.Required("as_"+strings.ToLower(v.Kind.name()), present[v.Kind])
	for kind, p := range present {
		if p && kind != v.Kind {
			k.c.Text("as_"+strings.ToLower(kind.name()), "\x00", 1)
		}
	}
	if v.AsDecisionPresent {
		decisionRules(&v.AsDecision, k)
	}
	if v.AsOutcomePresent {
		outcomeRules(&v.AsOutcome, k)
	}
	if v.AsActPresent {
		actRules(&v.AsAct, k)
	}
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

func judgmentIdRules(v *JudgmentId, k chk) {
	k.str("id", v.Id[:], v.IdLength)
	k.req("id", v.IdLength)
}

func importLogLineRules(v *ImportLogLine, k chk) {
	k.str("verb", v.Verb[:], v.VerbLength)
	k.count("answers", v.AnswersCount, len(v.Answers))
	for i := 0; i < int(v.AnswersCount) && i < len(v.Answers); i++ {
		judgmentIdRules(&v.Answers[i], k)
	}
}

// rawTokensRules: a count is a non-negative int64 (raw.go Validate).
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

// rawRecordRules are raw.go Validate's provenance rules.
func rawRecordRules(v *RawRecord, k chk) {
	k.str("harness", v.Harness[:], v.HarnessLength)
	k.is("harness", s(v.Harness[:], v.HarnessLength), "muse")
	k.str("execution", v.Execution[:], v.ExecutionLength)
	k.is("execution", s(v.Execution[:], v.ExecutionLength), "fleet")
	k.str("source_schema", v.SourceSchema[:], v.SourceSchemaLength)
	k.is("source_schema", s(v.SourceSchema[:], v.SourceSchemaLength), "muse-jsonl-v1")
	k.str("producer_version", v.ProducerVersion[:], v.ProducerVersionLength)
	k.req("producer_version", v.ProducerVersionLength)
	k.span("billing", int64(v.Billing), 1, 3)
	rawTokensRules(&v.Tokens, k)
}

// rawUsageRules: version 1 and at least one record (raw.go Validate).
func rawUsageRules(v *RawUsage, k chk) {
	k.span("version", int64(v.Version), 1, 1)
	k.c.Required("records", v.RecordsCount > 0)
	k.count("records", v.RecordsCount, len(v.Records))
	for i := 0; i < int(v.RecordsCount) && i < len(v.Records); i++ {
		rawRecordRules(&v.Records[i], k)
	}
}

func stagePathRules(v *StagePath, k chk) {
	k.str("path", v.Path[:], v.PathLength)
	k.req("path", v.PathLength)
}

// frameRules: ReadFrame refuses a kind that is not work or read and an empty
// card (contract.go).
func frameRules(v *Frame, k chk) {
	k.span("kind", int64(v.Kind), 1, 2)
	k.str("card", v.Card[:], v.CardLength)
	k.req("card", v.CardLength)
	k.span("attempt", int64(v.Attempt), 0, math.MaxInt32)
	k.str("tier", v.Tier[:], v.TierLength)
	k.str("model", v.Model[:], v.ModelLength)
	k.str("repo", v.Repo[:], v.RepoLength)
	k.str("base_ref", v.BaseRef[:], v.BaseRefLength)
	k.str("stage_sha", v.StageSha[:], v.StageShaLength)
	k.str("branch", v.Branch[:], v.BranchLength)
	k.str("prev_head", v.PrevHead[:], v.PrevHeadLength)
	k.span("prev_attempt", int64(v.PrevAttempt), 0, math.MaxInt32)
	k.str("why", v.Why[:], v.WhyLength)
	k.str("finding", v.Finding[:], v.FindingLength)
	k.str("fix", v.Fix[:], v.FixLength)
	k.str("review_base", v.ReviewBase[:], v.ReviewBaseLength)
	k.count("stage", v.StageCount, len(v.Stage))
	for i := 0; i < int(v.StageCount) && i < len(v.Stage); i++ {
		stagePathRules(&v.Stage[i], k)
	}
	k.str("recipes", v.Recipes[:], v.RecipesLength)
	k.str("decide_bounce", v.DecideBounce[:], v.DecideBounceLength)
	k.str("decide_review", v.DecideReview[:], v.DecideReviewLength)
	k.str("decide_gate_flaky", v.DecideGateFlaky[:], v.DecideGateFlakyLength)
	k.str("decide_gate_preexisting", v.DecideGatePreexisting[:], v.DecideGatePreexistingLength)
}

// ---- samples: a value of each table that holds every rule ----

func sampleUsage() Usage { return Usage{InputTokens: 1200, OutputTokens: 85} }

func sampleOptionP() OptionP {
	var v OptionP
	put(v.Option[:], &v.OptionLength, "done")
	v.P = 0.875
	return v
}

func sampleAnswer() Answer {
	var v Answer
	v.Kind = AnswerTypeChoice
	put(v.Value[:], &v.ValueLength, "done")
	v.P[0] = sampleOptionP()
	v.P[1] = OptionP{P: 0.125}
	put(v.P[1].Option[:], &v.P[1].OptionLength, "no-result")
	v.PCount = 2
	v.Confidence = 0.5
	put(v.Method[:], &v.MethodLength, "coordinator")
	return v
}

func sampleNamedAnswer() NamedAnswer {
	var v NamedAnswer
	put(v.Question[:], &v.QuestionLength, "class")
	v.Answer = sampleAnswer()
	return v
}

func sampleInput() Input {
	var v Input
	put(v.Key[:], &v.KeyLength, "reason")
	put(v.Value[:], &v.ValueLength, "gate green; pushed the head")
	return v
}

func sampleDecided() Decided {
	var v Decided
	put(v.Value[:], &v.ValueLength, "done")
	v.P = 0.931
	put(v.Op[:], &v.OpLength, "a13-defs-decide@1.0123456789ab")
	return v
}

func sampleDecision() Decision {
	var v Decision
	put(v.Id[:], &v.IdLength, "a13-defs-decide@1.0123456789ab")
	put(v.DecisionName[:], &v.DecisionNameLength, "attempt")
	put(v.Schema[:], &v.SchemaLength, "0123456789abcdef")
	put(v.Backend[:], &v.BackendLength, "jev")
	put(v.At[:], &v.AtLength, "2026-10-17T04:07:16Z")
	v.Inputs[0] = sampleInput()
	v.InputsCount = 1
	put(v.State[:], &v.StateLength, "CARD (the whole task the worker was given):\nthe brief\n\nRESULT:\n(none)\n")
	v.Answers[0] = sampleNamedAnswer()
	v.AnswersCount = 1
	v.Usage = sampleUsage()
	return v
}

func sampleOutcome() Outcome {
	var v Outcome
	put(v.Id[:], &v.IdLength, "a13-defs-decide@1.0123456789ab")
	put(v.Label[:], &v.LabelLength, "landed")
	put(v.Note[:], &v.NoteLength, "/work/VERDICT.md")
	put(v.At[:], &v.AtLength, "2026-10-17T04:09:00Z")
	return v
}

func sampleAct() Act {
	var v Act
	put(v.Id[:], &v.IdLength, "a13-defs-decide@1.0123456789ab")
	put(v.Step[:], &v.StepLength, "applying")
	put(v.Op[:], &v.OpLength, "answer-0042")
	put(v.At[:], &v.AtLength, "2026-10-17T04:09:30Z")
	return v
}

func sampleRecordLine() RecordLine {
	return RecordLine{Kind: LineKindDecision, AsDecision: sampleDecision(), AsDecisionPresent: true}
}

func sampleJudgmentId() JudgmentId {
	var v JudgmentId
	put(v.Id[:], &v.IdLength, "judgment-0007")
	return v
}

func sampleImportLogLine() ImportLogLine {
	var v ImportLogLine
	put(v.Verb[:], &v.VerbLength, "rework")
	v.Answers[0] = sampleJudgmentId()
	v.AnswersCount = 1
	return v
}

func sampleRawTokens() RawTokens {
	// Absence and zero differ: output is present at zero, cached is absent.
	return RawTokens{InputTokens: 4096, InputTokensPresent: true, OutputTokens: 0, OutputTokensPresent: true, ReasoningTokens: 17, ReasoningTokensPresent: true}
}

func sampleRawRecord() RawRecord {
	var v RawRecord
	put(v.Harness[:], &v.HarnessLength, "muse")
	put(v.Execution[:], &v.ExecutionLength, "fleet")
	put(v.SourceSchema[:], &v.SourceSchemaLength, "muse-jsonl-v1")
	put(v.ProducerVersion[:], &v.ProducerVersionLength, "1.4.2")
	v.Billing = BillingPlan
	v.Tokens = sampleRawTokens()
	return v
}

func sampleRawUsage() RawUsage {
	v := RawUsage{Version: 1}
	v.Records[0] = sampleRawRecord()
	v.Records[1] = sampleRawRecord()
	v.Records[1].Billing = BillingSubscription
	v.RecordsCount = 2
	return v
}

func sampleStagePath() StagePath {
	var v StagePath
	put(v.Path[:], &v.PathLength, "schema/recipe.md")
	return v
}

func sampleFrame() Frame {
	var v Frame
	v.Kind = FrameKindWork
	put(v.Card[:], &v.CardLength, "a13-defs-decide")
	v.Attempt = 1
	put(v.Tier[:], &v.TierLength, "pro")
	put(v.Model[:], &v.ModelLength, "anthropic/claude-opus")
	put(v.Repo[:], &v.RepoLength, "/work/nova-tools")
	put(v.BaseRef[:], &v.BaseRefLength, "sprint/v15-schema-tables")
	put(v.StageSha[:], &v.StageShaLength, "d03635a82bde0000000000000000000000000000")
	put(v.Branch[:], &v.BranchLength, "sprint/a13-defs-decide.w1.g1.e21")
	v.Stage[0] = sampleStagePath()
	v.StageCount = 1
	put(v.Recipes[:], &v.RecipesLength, "/work/recipes")
	put(v.DecideBounce[:], &v.DecideBounceLength, "0.9")
	return v
}

// ---- the exercise of one table ----

// bound is a value past one declared bound, and the refusal it earns.
type bound[T any] struct {
	name, field, reason string
	mutate              func(*T)
}

// exercise round-trips good in both forms, refuses each bound, and refuses a
// damaged image of each form, all by name.
func exercise[T any](t *testing.T, f fixedFns[T], good T, bounds []bound[T]) {
	t.Helper()
	r := schemaread.NewReader(f.codec())

	t.Run("round trips in both forms", func(t *testing.T) {
		for _, form := range []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64} {
			image, err := r.Write(&good, form)
			require.NoError(t, err, form.String())
			got, res, err := r.Read(image)
			require.NoError(t, err, form.String())
			assert.Equal(t, form, res.Form)
			assert.Equal(t, good, got, "%s through %s", f.name, form)
		}
		assert.EqualValues(t, 1, r.Counts().Reads(schemaread.FormBinary))
		assert.EqualValues(t, 1, r.Counts().Reads(schemaread.FormBase64))
		wire, err := r.Write(&good, schemaread.FormBinary)
		require.NoError(t, err)
		assert.Equal(t, int(f.measure(1)), len(wire), "one record's image is the layout and the record")
		text, err := r.Write(&good, schemaread.FormBase64)
		require.NoError(t, err)
		assert.Equal(t, base64.StdEncoding.EncodeToString(wire), string(text), "text is padded standard base64 of the wire")
		assert.Zero(t, len(text)%4)
	})

	t.Run("a value past a bound is refused by name", func(t *testing.T) {
		for _, b := range bounds {
			v := good
			b.mutate(&v)
			_, err := r.Write(&v, schemaread.FormBinary)
			var ref *schemaread.Refusal
			require.ErrorAs(t, err, &ref, b.name)
			assert.Equal(t, b.reason, ref.Reason, b.name)
			assert.Equal(t, b.field, ref.Field, b.name)
			assert.Contains(t, ref.Error(), "REFUSED: "+b.reason, b.name)
			// The writer wrote nothing, and the reader never clamps: the same
			// value, written by the generated writer as it stands, is refused
			// when read back.
			if f.measure != nil {
				buf := make([]byte, f.measure(1))
				if f.save([]T{v}, buf) == int64(len(buf)) {
					_, _, rerr := r.Read(buf)
					require.ErrorAs(t, rerr, &ref, b.name+" read back")
					assert.Equal(t, b.reason, ref.Reason, b.name+" read back")
				}
			}
		}
	})

	t.Run("a damaged image is refused by name", func(t *testing.T) {
		wire, err := r.Write(&good, schemaread.FormBinary)
		require.NoError(t, err)
		layout := int(uint32(wire[16]) | uint32(wire[17])<<8 | uint32(wire[18])<<16 | uint32(wire[19])<<24)
		record := 20 + layout
		patch := func(at int, to byte) []byte {
			out := bytes.Clone(wire)
			out[at] = to
			return out
		}
		damage := []struct {
			name, reason string
			image        []byte
		}{
			{"truncated one byte", "truncated", wire[:len(wire)-1]},
			{"truncated to the header", "truncated", wire[:10]},
			{"a trailing byte", "truncated", append(bytes.Clone(wire), 0)},
			{"the variable form byte", "unknown_form", patch(0, 1)},
			{"the message form byte", "unknown_form", patch(0, 2)},
			{"a newer form byte", "unknown_form", patch(0, 9)},
			{"a reserved byte set", "reserved_not_zero", patch(3, 1)},
			{"the layout hash changed", "layout_unknown", patch(8, wire[8]^1)},
			{"the layout damaged", "layout_malformed", patch(20, wire[20]^1)},
			{"the record's hash changed", "no_layout", patch(record, wire[record]^1)},
		}
		for _, d := range damage {
			for _, form := range []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64} {
				in := d.image
				if form == schemaread.FormBase64 {
					in = []byte(base64.StdEncoding.EncodeToString(d.image))
				}
				_, res, err := r.Read(in)
				var ref *schemaread.Refusal
				require.ErrorAs(t, err, &ref, "%s as %s", d.name, form)
				assert.Equal(t, d.reason, ref.Reason, "%s as %s", d.name, form)
				assert.Equal(t, form, res.Form, d.name)
			}
		}
		text := base64.StdEncoding.EncodeToString(wire)
		for name, in := range map[string]string{
			"base64 with a stray character": text[:8] + "!" + text[9:],
			"base64 with the padding cut":   strings.TrimRight(text, "=")[:len(strings.TrimRight(text, "="))-1],
			"base64 of nothing":             "A===",
		} {
			_, _, err := r.Read([]byte(in))
			var ref *schemaread.Refusal
			require.ErrorAs(t, err, &ref, name)
			assert.Equal(t, schemaread.ReasonMalformed, ref.Reason, name)
		}
		_, _, err = r.Read([]byte(`{"id":"x"}`))
		var ref *schemaread.Refusal
		require.ErrorAs(t, err, &ref)
		assert.Equal(t, schemaread.ReasonUnknownForm, ref.Reason, "JSON text is not a form of a fixed table")
		_, _, err = r.Read(nil)
		require.ErrorAs(t, err, &ref)
		assert.Equal(t, schemaread.ReasonUnknownForm, ref.Reason)
		assert.Positive(t, r.Counts().Refused("truncated"))
	})

	t.Run("the layout is pinned", func(t *testing.T) {
		wire, err := r.Write(&good, schemaread.FormBinary)
		require.NoError(t, err)
		got := uint64(0)
		for i := 7; i >= 0; i-- {
			got = got<<8 | uint64(wire[8+i])
		}
		assert.Equal(t, f.hash, got, "the image's layout hash is the table's")
		assert.Equal(t, pinnedLayouts[f.name], got, "%s: a changed table is a changed wire; update the pin with the schema", f.name)
		layout := int(uint32(wire[16]) | uint32(wire[17])<<8 | uint32(wire[18])<<16 | uint32(wire[19])<<24)
		assert.Equal(t, f.body, len(wire)-(20+layout+8), "the record body is the declared size")
	})
}

// pinnedLayouts are the layout hashes of the committed definitions
// (schemas/decide/schema.lock): the wire of each table, named once more here.
var pinnedLayouts = map[string]uint64{
	"Usage":         0xa668236e25aacf72,
	"OptionP":       0x53a11d4cc48e7d1e,
	"Answer":        0x495aace12b070bdd,
	"NamedAnswer":   0xe1a4ae9e23ccff37,
	"Input":         0x6abf8289e6411829,
	"Decided":       0xe7ec870d001faa5d,
	"Decision":      0xbd0631f9e0e0317b,
	"Outcome":       0x9ce1f114d5c1305d,
	"Act":           0x02b0edb53528ef07,
	"RecordLine":    0xb47e4ee21a748ab6,
	"JudgmentId":    0x42e7cbbb661cb901,
	"ImportLogLine": 0xfe1f819f56be3f55,
	"RawTokens":     0x521b10632b5348dd,
	"RawRecord":     0x0b29229910ea7c70,
	"RawUsage":      0x3fbd6dcb7d897241,
	"StagePath":     0x5d78fb3de6833178,
	"Frame":         0x9b0002fff723f881,
}

func wholeText(n int, c byte) []byte { return bytes.Repeat([]byte{c}, n) }

func TestDecideRecordsGolden(t *testing.T) {
	t.Parallel()

	t.Run("Usage", func(t *testing.T) {
		t.Parallel()
		exercise(t, fixedFns[Usage]{"Usage", UsageReset, UsageFixedMeasure, UsageFixedSave, UsageFixedLoad, UsageFixedPlanMax, UsageFixedBodyBytes, UsageFixedHash, usageRules},
			sampleUsage(), []bound[Usage]{
				{"input tokens below zero", "input_tokens", "clamped", func(v *Usage) { v.InputTokens = -1 }},
				{"output tokens below zero", "output_tokens", "clamped", func(v *Usage) { v.OutputTokens = -1 }},
			})
	})
	t.Run("OptionP", func(t *testing.T) {
		t.Parallel()
		exercise(t, fixedFns[OptionP]{"OptionP", OptionPReset, OptionPFixedMeasure, OptionPFixedSave, OptionPFixedLoad, OptionPFixedPlanMax, OptionPFixedBodyBytes, OptionPFixedHash, optionPRules},
			sampleOptionP(), []bound[OptionP]{
				{"option past 48 bytes", "option", "clamped", func(v *OptionP) { v.OptionLength = 49 }},
				{"option empty", "option", "absent_required", func(v *OptionP) { v.OptionLength = 0 }},
				{"probability above one", "p", "clamped", func(v *OptionP) { v.P = 1.0000001 }},
				{"probability below zero", "p", "clamped", func(v *OptionP) { v.P = -0.1 }},
				{"probability not a number", "p", "malformed", func(v *OptionP) { v.P = math.NaN() }},
				{"option not UTF-8", "option", "malformed", func(v *OptionP) { v.Option[0] = 0xff }},
			})
	})
	t.Run("Answer", func(t *testing.T) {
		t.Parallel()
		exercise(t, fixedFns[Answer]{"Answer", AnswerReset, AnswerFixedMeasure, AnswerFixedSave, AnswerFixedLoad, AnswerFixedPlanMax, AnswerFixedBodyBytes, AnswerFixedHash, answerRules},
			sampleAnswer(), []bound[Answer]{
				{"kind none", "kind", "clamped", func(v *Answer) { v.Kind = AnswerTypeNone }},
				{"nine options", "p", "clamped", func(v *Answer) { v.PCount = 9 }},
				{"a probability above one", "p", "clamped", func(v *Answer) { v.P[1].P = 2 }},
				{"value empty", "value", "absent_required", func(v *Answer) { v.ValueLength = 0 }},
				{"method past 24 bytes", "method", "clamped", func(v *Answer) { v.MethodLength = 25 }},
			})
	})
	t.Run("NamedAnswer", func(t *testing.T) {
		t.Parallel()
		exercise(t, fixedFns[NamedAnswer]{"NamedAnswer", NamedAnswerReset, NamedAnswerFixedMeasure, NamedAnswerFixedSave, NamedAnswerFixedLoad, NamedAnswerFixedPlanMax, NamedAnswerFixedBodyBytes, NamedAnswerFixedHash, namedAnswerRules},
			sampleNamedAnswer(), []bound[NamedAnswer]{
				{"question past 32 bytes", "question", "clamped", func(v *NamedAnswer) { v.QuestionLength = 33 }},
				{"question empty", "question", "absent_required", func(v *NamedAnswer) { v.QuestionLength = 0 }},
				{"the answer's kind none", "kind", "clamped", func(v *NamedAnswer) { v.Answer.Kind = AnswerTypeNone }},
			})
	})
	t.Run("Input", func(t *testing.T) {
		t.Parallel()
		exercise(t, fixedFns[Input]{"Input", InputReset, InputFixedMeasure, InputFixedSave, InputFixedLoad, InputFixedPlanMax, InputFixedBodyBytes, InputFixedHash, inputRules},
			sampleInput(), []bound[Input]{
				{"key past 32 bytes", "key", "clamped", func(v *Input) { v.KeyLength = 33 }},
				{"value past 1024 bytes", "value", "clamped", func(v *Input) { v.ValueLength = 1025 }},
				{"key empty", "key", "absent_required", func(v *Input) { v.KeyLength = 0 }},
			})
	})
	t.Run("Decided", func(t *testing.T) {
		t.Parallel()
		exercise(t, fixedFns[Decided]{"Decided", DecidedReset, DecidedFixedMeasure, DecidedFixedSave, DecidedFixedLoad, DecidedFixedPlanMax, DecidedFixedBodyBytes, DecidedFixedHash, decidedRules},
			sampleDecided(), []bound[Decided]{
				{"probability above one", "p", "clamped", func(v *Decided) { v.P = 1.5 }},
				{"op past 256 bytes", "op", "clamped", func(v *Decided) { v.OpLength = 257 }},
				{"value with a space", "value", "malformed", func(v *Decided) { v.Value[1] = ' ' }},
				{"op empty", "op", "absent_required", func(v *Decided) { v.OpLength = 0 }},
			})
	})
	t.Run("Decision", func(t *testing.T) {
		t.Parallel()
		exercise(t, fixedFns[Decision]{"Decision", DecisionReset, DecisionFixedMeasure, DecisionFixedSave, DecisionFixedLoad, DecisionFixedPlanMax, DecisionFixedBodyBytes, DecisionFixedHash, decisionRules},
			sampleDecision(), []bound[Decision]{
				{"id past 256 bytes", "id", "clamped", func(v *Decision) { v.IdLength = 257 }},
				{"id empty", "id", "absent_required", func(v *Decision) { v.IdLength = 0 }},
				{"decision name empty", "decision_name", "absent_required", func(v *Decision) { v.DecisionNameLength = 0 }},
				{"nine inputs", "inputs", "clamped", func(v *Decision) { v.InputsCount = 9 }},
				{"state past 40960 bytes", "state", "clamped", func(v *Decision) { v.StateLength = 40961 }},
				{"eleven answers", "answers", "clamped", func(v *Decision) { v.AnswersCount = 11 }},
				{"an answer above one", "p", "clamped", func(v *Decision) { v.Answers[0].Answer.P[0].P = 1.01 }},
				{"usage below zero", "output_tokens", "clamped", func(v *Decision) { v.Usage.OutputTokens = -5 }},
			})
		// The largest state a card carries: the brief and a result cut at 16 KiB
		// each fit, and the whole image still reads.
		r := schemaread.NewReader(fixedFns[Decision]{"Decision", DecisionReset, DecisionFixedMeasure, DecisionFixedSave, DecisionFixedLoad, DecisionFixedPlanMax, DecisionFixedBodyBytes, DecisionFixedHash, decisionRules}.codec())
		big := sampleDecision()
		put(big.State[:], &big.StateLength, string(wholeText(2*16<<10+1024, 'x')))
		image, err := r.Write(&big, schemaread.FormBase64)
		require.NoError(t, err)
		got, _, err := r.Read(image)
		require.NoError(t, err)
		assert.Equal(t, big, got)
	})
	t.Run("Outcome", func(t *testing.T) {
		t.Parallel()
		exercise(t, fixedFns[Outcome]{"Outcome", OutcomeReset, OutcomeFixedMeasure, OutcomeFixedSave, OutcomeFixedLoad, OutcomeFixedPlanMax, OutcomeFixedBodyBytes, OutcomeFixedHash, outcomeRules},
			sampleOutcome(), []bound[Outcome]{
				{"label past 64 bytes", "label", "clamped", func(v *Outcome) { v.LabelLength = 65 }},
				{"note past 1024 bytes", "note", "clamped", func(v *Outcome) { v.NoteLength = 1025 }},
				{"label empty", "label", "absent_required", func(v *Outcome) { v.LabelLength = 0 }},
				{"id empty", "id", "absent_required", func(v *Outcome) { v.IdLength = 0 }},
			})
	})
	t.Run("Act", func(t *testing.T) {
		t.Parallel()
		exercise(t, fixedFns[Act]{"Act", ActReset, ActFixedMeasure, ActFixedSave, ActFixedLoad, ActFixedPlanMax, ActFixedBodyBytes, ActFixedHash, actRules},
			sampleAct(), []bound[Act]{
				{"step past 16 bytes", "step", "clamped", func(v *Act) { v.StepLength = 17 }},
				{"step empty", "step", "absent_required", func(v *Act) { v.StepLength = 0 }},
				{"op past 256 bytes", "op", "clamped", func(v *Act) { v.OpLength = 257 }},
			})
	})
	t.Run("RecordLine", func(t *testing.T) {
		t.Parallel()
		outcome, act := sampleOutcome(), sampleAct()
		exercise(t, fixedFns[RecordLine]{"RecordLine", RecordLineReset, RecordLineFixedMeasure, RecordLineFixedSave, RecordLineFixedLoad, RecordLineFixedPlanMax, RecordLineFixedBodyBytes, RecordLineFixedHash, recordLineRules},
			sampleRecordLine(), []bound[RecordLine]{
				{"kind none", "kind", "clamped", func(v *RecordLine) { v.Kind = LineKindNone }},
				{"the named record absent", "as_decision", "absent_required", func(v *RecordLine) { v.AsDecisionPresent = false }},
				{"an outcome beside the decision", "as_outcome", "malformed", func(v *RecordLine) { v.AsOutcome, v.AsOutcomePresent = outcome, true }},
				{"an act beside the decision", "as_act", "malformed", func(v *RecordLine) { v.AsAct, v.AsActPresent = act, true }},
				{"the decision's id past 256", "id", "clamped", func(v *RecordLine) { v.AsDecision.IdLength = 300 }},
			})
		// The other two kinds round-trip as lines of their own.
		r := schemaread.NewReader(fixedFns[RecordLine]{"RecordLine", RecordLineReset, RecordLineFixedMeasure, RecordLineFixedSave, RecordLineFixedLoad, RecordLineFixedPlanMax, RecordLineFixedBodyBytes, RecordLineFixedHash, recordLineRules}.codec())
		for _, line := range []RecordLine{{Kind: LineKindOutcome, AsOutcome: outcome, AsOutcomePresent: true}, {Kind: LineKindAct, AsAct: act, AsActPresent: true}} {
			image, err := r.Write(&line, schemaread.FormBinary)
			require.NoError(t, err)
			got, _, err := r.Read(image)
			require.NoError(t, err)
			assert.Equal(t, line, got)
		}
	})
	t.Run("JudgmentId", func(t *testing.T) {
		t.Parallel()
		exercise(t, fixedFns[JudgmentId]{"JudgmentId", JudgmentIdReset, JudgmentIdFixedMeasure, JudgmentIdFixedSave, JudgmentIdFixedLoad, JudgmentIdFixedPlanMax, JudgmentIdFixedBodyBytes, JudgmentIdFixedHash, judgmentIdRules},
			sampleJudgmentId(), []bound[JudgmentId]{
				{"id past 128 bytes", "id", "clamped", func(v *JudgmentId) { v.IdLength = 129 }},
				{"id empty", "id", "absent_required", func(v *JudgmentId) { v.IdLength = 0 }},
			})
	})
	t.Run("ImportLogLine", func(t *testing.T) {
		t.Parallel()
		exercise(t, fixedFns[ImportLogLine]{"ImportLogLine", ImportLogLineReset, ImportLogLineFixedMeasure, ImportLogLineFixedSave, ImportLogLineFixedLoad, ImportLogLineFixedPlanMax, ImportLogLineFixedBodyBytes, ImportLogLineFixedHash, importLogLineRules},
			sampleImportLogLine(), []bound[ImportLogLine]{
				{"verb past 64 bytes", "verb", "clamped", func(v *ImportLogLine) { v.VerbLength = 65 }},
				{"nine answers", "answers", "clamped", func(v *ImportLogLine) { v.AnswersCount = 9 }},
				{"an answer id empty", "id", "absent_required", func(v *ImportLogLine) { v.Answers[0].IdLength = 0 }},
			})
	})
	t.Run("RawTokens", func(t *testing.T) {
		t.Parallel()
		exercise(t, fixedFns[RawTokens]{"RawTokens", RawTokensReset, RawTokensFixedMeasure, RawTokensFixedSave, RawTokensFixedLoad, RawTokensFixedPlanMax, RawTokensFixedBodyBytes, RawTokensFixedHash, rawTokensRules},
			sampleRawTokens(), []bound[RawTokens]{
				{"input below zero", "input_tokens", "clamped", func(v *RawTokens) { v.InputTokens = -1 }},
				{"cached below zero", "cached_tokens", "clamped", func(v *RawTokens) { v.CachedTokens, v.CachedTokensPresent = -7, true }},
				{"reasoning below zero", "reasoning_tokens", "clamped", func(v *RawTokens) { v.ReasoningTokens = math.MinInt64 }},
			})
		// Absence and zero differ (raw.go): the image keeps which is which.
		r := schemaread.NewReader(fixedFns[RawTokens]{"RawTokens", RawTokensReset, RawTokensFixedMeasure, RawTokensFixedSave, RawTokensFixedLoad, RawTokensFixedPlanMax, RawTokensFixedBodyBytes, RawTokensFixedHash, rawTokensRules}.codec())
		image, err := r.Write(&RawTokens{}, schemaread.FormBinary)
		require.NoError(t, err)
		none, _, err := r.Read(image)
		require.NoError(t, err)
		assert.False(t, none.OutputTokensPresent)
		zero := RawTokens{OutputTokensPresent: true}
		image, err = r.Write(&zero, schemaread.FormBinary)
		require.NoError(t, err)
		got, _, err := r.Read(image)
		require.NoError(t, err)
		assert.True(t, got.OutputTokensPresent)
		assert.Zero(t, got.OutputTokens)
	})
	t.Run("RawRecord", func(t *testing.T) {
		t.Parallel()
		exercise(t, fixedFns[RawRecord]{"RawRecord", RawRecordReset, RawRecordFixedMeasure, RawRecordFixedSave, RawRecordFixedLoad, RawRecordFixedPlanMax, RawRecordFixedBodyBytes, RawRecordFixedHash, rawRecordRules},
			sampleRawRecord(), []bound[RawRecord]{
				{"harness not muse", "harness", "malformed", func(v *RawRecord) { put(v.Harness[:], &v.HarnessLength, "codex") }},
				{"execution not fleet", "execution", "malformed", func(v *RawRecord) { put(v.Execution[:], &v.ExecutionLength, "local") }},
				{"source schema not muse-jsonl-v1", "source_schema", "malformed", func(v *RawRecord) { put(v.SourceSchema[:], &v.SourceSchemaLength, "muse-jsonl-v2") }},
				{"producer version empty", "producer_version", "absent_required", func(v *RawRecord) { v.ProducerVersionLength = 0 }},
				{"producer version past 64", "producer_version", "clamped", func(v *RawRecord) { v.ProducerVersionLength = 65 }},
				{"billing none", "billing", "clamped", func(v *RawRecord) { v.Billing = BillingNone }},
				{"a negative counter", "input_tokens", "clamped", func(v *RawRecord) { v.Tokens.InputTokens = -1 }},
			})
	})
	t.Run("RawUsage", func(t *testing.T) {
		t.Parallel()
		exercise(t, fixedFns[RawUsage]{"RawUsage", RawUsageReset, RawUsageFixedMeasure, RawUsageFixedSave, RawUsageFixedLoad, RawUsageFixedPlanMax, RawUsageFixedBodyBytes, RawUsageFixedHash, rawUsageRules},
			sampleRawUsage(), []bound[RawUsage]{
				{"version two", "version", "clamped", func(v *RawUsage) { v.Version = 2 }},
				{"version zero", "version", "clamped", func(v *RawUsage) { v.Version = 0 }},
				{"no records", "records", "absent_required", func(v *RawUsage) { v.RecordsCount = 0 }},
				{"241 records", "records", "clamped", func(v *RawUsage) { v.RecordsCount = 241 }},
				{"a record's harness not muse", "harness", "malformed", func(v *RawUsage) { put(v.Records[1].Harness[:], &v.Records[1].HarnessLength, "x") }},
			})
		// The most records the fixed form carries.
		r := schemaread.NewReader(fixedFns[RawUsage]{"RawUsage", RawUsageReset, RawUsageFixedMeasure, RawUsageFixedSave, RawUsageFixedLoad, RawUsageFixedPlanMax, RawUsageFixedBodyBytes, RawUsageFixedHash, rawUsageRules}.codec())
		full := sampleRawUsage()
		for i := range full.Records {
			full.Records[i] = sampleRawRecord()
		}
		full.RecordsCount = int32(len(full.Records))
		image, err := r.Write(&full, schemaread.FormBase64)
		require.NoError(t, err)
		got, _, err := r.Read(image)
		require.NoError(t, err)
		assert.Equal(t, full, got)
	})
	t.Run("StagePath", func(t *testing.T) {
		t.Parallel()
		exercise(t, fixedFns[StagePath]{"StagePath", StagePathReset, StagePathFixedMeasure, StagePathFixedSave, StagePathFixedLoad, StagePathFixedPlanMax, StagePathFixedBodyBytes, StagePathFixedHash, stagePathRules},
			sampleStagePath(), []bound[StagePath]{
				{"path past 256 bytes", "path", "clamped", func(v *StagePath) { v.PathLength = 257 }},
				{"path empty", "path", "absent_required", func(v *StagePath) { v.PathLength = 0 }},
			})
	})
	t.Run("Frame", func(t *testing.T) {
		t.Parallel()
		exercise(t, fixedFns[Frame]{"Frame", FrameReset, FrameFixedMeasure, FrameFixedSave, FrameFixedLoad, FrameFixedPlanMax, FrameFixedBodyBytes, FrameFixedHash, frameRules},
			sampleFrame(), []bound[Frame]{
				{"kind none", "kind", "clamped", func(v *Frame) { v.Kind = FrameKindNone }},
				{"card empty", "card", "absent_required", func(v *Frame) { v.CardLength = 0 }},
				{"card past 256 bytes", "card", "clamped", func(v *Frame) { v.CardLength = 257 }},
				{"attempt below zero", "attempt", "clamped", func(v *Frame) { v.Attempt = -1 }},
				{"prev attempt below zero", "prev_attempt", "clamped", func(v *Frame) { v.PrevAttempt = -1 }},
				{"repo past 1024 bytes", "repo", "clamped", func(v *Frame) { v.RepoLength = 1025 }},
				{"why past 4096 bytes", "why", "clamped", func(v *Frame) { v.WhyLength = 4097 }},
				{"33 staged recipes", "stage", "clamped", func(v *Frame) { v.StageCount = 33 }},
				{"a staged path empty", "path", "absent_required", func(v *Frame) { v.Stage[0].PathLength = 0 }},
				{"a gate bar past 16 bytes", "decide_gate_flaky", "clamped", func(v *Frame) { v.DecideGateFlakyLength = 17 }},
			})
	})

	t.Run("a stored value forged past a bound is refused, never clamped", func(t *testing.T) {
		t.Parallel()
		// A writer that is not this build's can put a value past a declared
		// range on the wire. The generated reader clamps and counts it; the
		// facade refuses the count by name.
		r := schemaread.NewReader(fixedFns[Usage]{"Usage", UsageReset, UsageFixedMeasure, UsageFixedSave, UsageFixedLoad, UsageFixedPlanMax, UsageFixedBodyBytes, UsageFixedHash, usageRules}.codec())
		v := sampleUsage()
		image, err := r.Write(&v, schemaread.FormBinary)
		require.NoError(t, err)
		body := len(image) - UsageFixedBodyBytes
		for i := 0; i < 4; i++ {
			image[body+i] = 0xff // input_tokens = -1, below its declared minimum 0
		}
		for _, in := range [][]byte{image, []byte(base64.StdEncoding.EncodeToString(image))} {
			got, _, err := r.Read(in)
			var ref *schemaread.Refusal
			require.ErrorAs(t, err, &ref)
			assert.Equal(t, schemaread.ReasonClamped, ref.Reason)
			assert.Equal(t, Usage{}, got, "a refused read returns nothing, not the clamped value")
		}
		assert.EqualValues(t, 2, r.Counts().Refused(schemaread.ReasonClamped))

		// A text length forged past its capacity.
		o := schemaread.NewReader(fixedFns[OptionP]{"OptionP", OptionPReset, OptionPFixedMeasure, OptionPFixedSave, OptionPFixedLoad, OptionPFixedPlanMax, OptionPFixedBodyBytes, OptionPFixedHash, optionPRules}.codec())
		p := sampleOptionP()
		image, err = o.Write(&p, schemaread.FormBinary)
		require.NoError(t, err)
		image[len(image)-OptionPFixedBodyBytes] = 49
		_, _, err = o.Read(image)
		var ref *schemaread.Refusal
		require.ErrorAs(t, err, &ref)
		assert.NotEmpty(t, ref.Reason)
	})
}
