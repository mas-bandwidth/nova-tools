package decidewire

// TestDecideRecordsGolden is the golden test of the decide definitions
// (schemas/decide): every table the area declares round-trips as binary wire,
// as padded standard base64 of that wire and as the JSON text form, each read
// strictly through pkg/schemaread (S1, S2). The small records (Usage, Outcome,
// Act, Decided, Frame and the raw cost records) are FIXED tables, read and
// written by value; the documents (Decision, Answer and their lists,
// RecordLine, RawUsage) are VARIABLE tables, held in an arena. A value past
// each declared bound, and a damaged image of each form, is refused by name and
// never clamped; the reader counts each read on the form it arrived in; and the
// unit's protocol id, the hash of its wire shape, is pinned.
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
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/schemaread"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const decideHelp = "nova-tools decide -h"

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
	max := len(buf) - 1 // the generated buffer is one byte longer than the bound
	k.c.Bound(field, int64(n), 0, int64(max))
	k.c.Text(field, textOf(buf, n), max)
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

func decidedRules(v *Decided, k chk) {
	k.str("value", v.Value[:], v.ValueLength)
	k.req("value", v.ValueLength)
	k.bare("value", s(v.Value[:], v.ValueLength))
	k.prob("p", v.P)
	k.str("op", v.Op[:], v.OpLength)
	k.req("op", v.OpLength)
	k.bare("op", s(v.Op[:], v.OpLength))
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

func sampleDecided() Decided {
	var v Decided
	put(v.Value[:], &v.ValueLength, "done")
	v.P = 0.931
	put(v.Op[:], &v.OpLength, "a13-defs-decide@1.0123456789ab")
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

func TestDecideRecordsGolden(t *testing.T) {
	t.Parallel()

	// The small records are FIXED tables: structs read and written by value.
	// In a unit that also declares variable tables the compiler gives them the
	// fixed class (by-value storage, cook, block form) on the variable form's
	// wire, so they read and write in the same three forms.
	t.Run("Usage", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, plainFns("Usage", UsageLoad, UsageFromJson, UsageMeasure, UsageSave, UsageToJsonMeasure, UsageToJson, func(r *Usage, k chk) { usageRules(r, k) }),
			func(_ *plain[Usage], r *Usage) { *r = sampleUsage() }, plainBounds([]pbound[Usage]{
				{"input tokens below zero", "input_tokens", "clamped", func(v *Usage) { v.InputTokens = -1 }},
				{"output tokens below zero", "output_tokens", "clamped", func(v *Usage) { v.OutputTokens = -1 }},
			}))
	})
	t.Run("Decided", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, plainFns("Decided", DecidedLoad, DecidedFromJson, DecidedMeasure, DecidedSave, DecidedToJsonMeasure, DecidedToJson, func(r *Decided, k chk) { decidedRules(r, k) }),
			func(_ *plain[Decided], r *Decided) { *r = sampleDecided() }, plainBounds([]pbound[Decided]{
				{"probability above one", "p", "clamped", func(v *Decided) { v.P = 1.5 }},
				{"op past 256 bytes", "op", "clamped", func(v *Decided) { v.OpLength = 257 }},
				{"value with a space", "value", "malformed", func(v *Decided) { v.Value[1] = ' ' }},
				{"op empty", "op", "absent_required", func(v *Decided) { v.OpLength = 0 }},
			}))
	})
	t.Run("Outcome", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, plainFns("Outcome", OutcomeLoad, OutcomeFromJson, OutcomeMeasure, OutcomeSave, OutcomeToJsonMeasure, OutcomeToJson, func(r *Outcome, k chk) { outcomeRules(r, k) }),
			func(_ *plain[Outcome], r *Outcome) { *r = sampleOutcome() }, plainBounds([]pbound[Outcome]{
				{"label past 64 bytes", "label", "clamped", func(v *Outcome) { v.LabelLength = 65 }},
				{"note past 1024 bytes", "note", "clamped", func(v *Outcome) { v.NoteLength = 1025 }},
				{"label empty", "label", "absent_required", func(v *Outcome) { v.LabelLength = 0 }},
				{"id empty", "id", "absent_required", func(v *Outcome) { v.IdLength = 0 }},
			}))
	})
	t.Run("Act", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, plainFns("Act", ActLoad, ActFromJson, ActMeasure, ActSave, ActToJsonMeasure, ActToJson, func(r *Act, k chk) { actRules(r, k) }),
			func(_ *plain[Act], r *Act) { *r = sampleAct() }, plainBounds([]pbound[Act]{
				{"step past 16 bytes", "step", "clamped", func(v *Act) { v.StepLength = 17 }},
				{"step empty", "step", "absent_required", func(v *Act) { v.StepLength = 0 }},
				{"op past 256 bytes", "op", "clamped", func(v *Act) { v.OpLength = 257 }},
			}))
	})
	t.Run("JudgmentId", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, plainFns("JudgmentId", JudgmentIdLoad, JudgmentIdFromJson, JudgmentIdMeasure, JudgmentIdSave, JudgmentIdToJsonMeasure, JudgmentIdToJson, func(r *JudgmentId, k chk) { judgmentIdRules(r, k) }),
			func(_ *plain[JudgmentId], r *JudgmentId) { *r = sampleJudgmentId() }, plainBounds([]pbound[JudgmentId]{
				{"id past 128 bytes", "id", "clamped", func(v *JudgmentId) { v.IdLength = 129 }},
				{"id empty", "id", "absent_required", func(v *JudgmentId) { v.IdLength = 0 }},
			}))
	})
	t.Run("ImportLogLine", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, plainFns("ImportLogLine", ImportLogLineLoad, ImportLogLineFromJson, ImportLogLineMeasure, ImportLogLineSave, ImportLogLineToJsonMeasure, ImportLogLineToJson, func(r *ImportLogLine, k chk) { importLogLineRules(r, k) }),
			func(_ *plain[ImportLogLine], r *ImportLogLine) { *r = sampleImportLogLine() }, plainBounds([]pbound[ImportLogLine]{
				{"verb past 64 bytes", "verb", "clamped", func(v *ImportLogLine) { v.VerbLength = 65 }},
				{"nine answers", "answers", "clamped", func(v *ImportLogLine) { v.AnswersCount = 9 }},
				{"an answer id empty", "id", "absent_required", func(v *ImportLogLine) { v.Answers[0].IdLength = 0 }},
			}))
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
	t.Run("RawRecord", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, plainFns("RawRecord", RawRecordLoad, RawRecordFromJson, RawRecordMeasure, RawRecordSave, RawRecordToJsonMeasure, RawRecordToJson, func(r *RawRecord, k chk) { rawRecordRules(r, k) }),
			func(_ *plain[RawRecord], r *RawRecord) { *r = sampleRawRecord() }, plainBounds([]pbound[RawRecord]{
				{"harness not muse", "harness", "malformed", func(v *RawRecord) { put(v.Harness[:], &v.HarnessLength, "codex") }},
				{"execution not fleet", "execution", "malformed", func(v *RawRecord) { put(v.Execution[:], &v.ExecutionLength, "local") }},
				{"source schema not muse-jsonl-v1", "source_schema", "malformed", func(v *RawRecord) { put(v.SourceSchema[:], &v.SourceSchemaLength, "muse-jsonl-v2") }},
				{"producer version empty", "producer_version", "absent_required", func(v *RawRecord) { v.ProducerVersionLength = 0 }},
				{"producer version past 64", "producer_version", "clamped", func(v *RawRecord) { v.ProducerVersionLength = 65 }},
				{"billing none", "billing", "clamped", func(v *RawRecord) { v.Billing = BillingNone }},
				{"a negative counter", "input_tokens", "clamped", func(v *RawRecord) { v.Tokens.InputTokens = -1 }},
			}))
	})
	t.Run("StagePath", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, plainFns("StagePath", StagePathLoad, StagePathFromJson, StagePathMeasure, StagePathSave, StagePathToJsonMeasure, StagePathToJson, func(r *StagePath, k chk) { stagePathRules(r, k) }),
			func(_ *plain[StagePath], r *StagePath) { *r = sampleStagePath() }, plainBounds([]pbound[StagePath]{
				{"path past 256 bytes", "path", "clamped", func(v *StagePath) { v.PathLength = 257 }},
				{"path empty", "path", "absent_required", func(v *StagePath) { v.PathLength = 0 }},
			}))
	})
	t.Run("Frame", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, plainFns("Frame", FrameLoad, FrameFromJson, FrameMeasure, FrameSave, FrameToJsonMeasure, FrameToJson, func(r *Frame, k chk) { frameRules(r, k) }),
			func(_ *plain[Frame], r *Frame) { *r = sampleFrame() }, plainBounds([]pbound[Frame]{
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
			}))
	})

	variableTables(t)

	t.Run("the wire shape is pinned", func(t *testing.T) {
		t.Parallel()
		// The protocol id is the hash of the unit's wire shape (schema.lock holds
		// the fixed layouts): a changed table is a changed id, and this line
		// changes with it.
		assert.EqualValues(t, uint64(0x43d04f03b7c3856d), ProtocolId)
	})

}

// ---- the variable tables: the documents ----

func optionPRules(v *OptionP, k chk) {
	k.str("option", v.Option[:], v.OptionLength)
	k.req("option", v.OptionLength)
	k.prob("p", v.P)
}

// arenaOf is the arena a read of a table's lists passes: before the arena is
// locked a reference is an arena offset; after it, in the packed region, a
// reference is relative to its own slot and no arena is passed.
func arenaOf(a *TableArena) []*TableArena {
	if a.Locked {
		return nil
	}
	return []*TableArena{a}
}

// answerRules: the answer's kind is choice or noul, its options an open-ended
// list (decide.go ParseSchema wants two and Check sets no ceiling), each
// probability in [0, 1] (decide.go Check).
func answerRules(a *TableArena, v *Answer, k chk) {
	k.span("kind", int64(v.Kind), 1, 2)
	k.str("value", v.Value[:], v.ValueLength)
	k.req("value", v.ValueLength)
	AnswerPEach(&v.P, func(o *OptionP) bool { optionPRules(o, k); return true }, arenaOf(a)...)
	k.str("method", v.Method[:], v.MethodLength)
}

func namedAnswerRules(a *TableArena, v *NamedAnswer, k chk) {
	k.str("question", v.Question[:], v.QuestionLength)
	k.req("question", v.QuestionLength)
	answerRules(a, &v.Answer, k)
}

func inputRules(v *Input, k chk) {
	k.str("key", v.Key[:], v.KeyLength)
	k.req("key", v.KeyLength)
	k.str("value", v.Value[:], v.ValueLength)
}

// decisionRules: the lists are open-ended (the code fixes neither count), the
// state is held at its own size up to the record reader's 64 MiB line.
func decisionRules(a *TableArena, v *Decision, k chk) {
	// Before the arena is locked a reference is an arena offset; after it, in
	// the packed region, a reference is relative to its own slot.
	var arena []*TableArena
	if !a.Locked {
		arena = []*TableArena{a}
	}
	k.str("id", v.Id[:], v.IdLength)
	k.req("id", v.IdLength)
	k.str("decision_name", v.DecisionName[:], v.DecisionNameLength)
	k.req("decision_name", v.DecisionNameLength)
	k.str("schema", v.Schema[:], v.SchemaLength)
	k.str("backend", v.Backend[:], v.BackendLength)
	k.str("at", v.At[:], v.AtLength)
	DecisionInputsEach(&v.Inputs, func(in *Input) bool { inputRules(in, k); return true }, arena...)
	k.c.Text("state", string(TableStringAt(&v.State, arena...).Data), 64<<20)
	DecisionAnswersEach(&v.Answers, func(n *NamedAnswer) bool { namedAnswerRules(a, n, k); return true }, arena...)
	usageRules(&v.Usage, k)
}

// recordLineRules: a line is one record (record.go parse), the one kind names.
func recordLineRules(a *TableArena, v *RecordLine, k chk) {
	k.span("kind", int64(v.Kind), 1, 3)
	var arena []*TableArena
	if !a.Locked {
		arena = []*TableArena{a}
	}
	d := DecisionAt(&v.AsDecision, arena...)
	present := map[LineKind]bool{LineKindDecision: d != nil, LineKindOutcome: v.AsOutcomePresent, LineKindAct: v.AsActPresent}
	k.c.Required("as_"+strings.ToLower(v.Kind.name()), present[v.Kind])
	for kind, p := range present {
		if p && kind != v.Kind {
			k.c.Text("as_"+strings.ToLower(kind.name()), "\x00", 1)
		}
	}
	if d != nil {
		decisionRules(a, d, k)
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

// rawUsageRules: version 1 and at least one record, at most 270 (raw.go
// Validate; MaxRawBytes 32768 over a 120-byte smallest record).
func rawUsageRules(v *RawUsage, k chk) {
	k.span("version", int64(v.Version), 1, 1)
	k.c.Required("records", v.RecordsCount > 0)
	k.count("records", v.RecordsCount, len(v.Records))
	for i := 0; i < int(v.RecordsCount) && i < len(v.Records); i++ {
		rawRecordRules(&v.Records[i], k)
	}
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
		Constraints: func(v *PB, c *schemaread.Checker) { f.rules(*v, f.root(*v), chk{c}) },
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

// ---- samples of the variable tables ----

func fillOptionP(r *OptionP, option string, p float64) {
	put(r.Option[:len(r.Option)-1], &r.OptionLength, option)
	r.P = p
}

// fillAnswer fills a choice with options options.
func fillAnswer(w *TableWorker, r *Answer, options int) {
	r.Kind = AnswerTypeChoice
	put(r.Value[:len(r.Value)-1], &r.ValueLength, "opt-0")
	for i := 0; i < options; i++ {
		fillOptionP(AnswerPAdd(w, &r.P), fmt.Sprintf("opt-%d", i), 1/float64(options))
	}
	r.Confidence = 0.5
	put(r.Method[:len(r.Method)-1], &r.MethodLength, "coordinator")
}

func fillNamedAnswer(w *TableWorker, r *NamedAnswer, options int) {
	put(r.Question[:len(r.Question)-1], &r.QuestionLength, "class")
	fillAnswer(w, &r.Answer, options)
}

func fillInput(r *Input, i int) {
	put(r.Key[:len(r.Key)-1], &r.KeyLength, fmt.Sprintf("key-%d", i))
	put(r.Value[:len(r.Value)-1], &r.ValueLength, "gate green; pushed the head")
}

// fillDecision fills a decision with inputs inputs, answers answers of options
// options each, and a state of stateBytes bytes.
func fillDecision(w *TableWorker, r *Decision, inputs, answers, options, stateBytes int) {
	put(r.Id[:len(r.Id)-1], &r.IdLength, "a13-defs-decide@1.0123456789ab")
	put(r.DecisionName[:len(r.DecisionName)-1], &r.DecisionNameLength, "attempt")
	put(r.Schema[:len(r.Schema)-1], &r.SchemaLength, "0123456789abcdef")
	put(r.Backend[:len(r.Backend)-1], &r.BackendLength, "jev")
	put(r.At[:len(r.At)-1], &r.AtLength, "2026-10-17T04:07:16Z")
	for i := 0; i < inputs; i++ {
		fillInput(DecisionInputsAdd(w, &r.Inputs), i)
	}
	TableStringEmplace(w, &r.State, bytes.Repeat([]byte("state "), stateBytes/6+1)[:stateBytes])
	for i := 0; i < answers; i++ {
		fillNamedAnswer(w, DecisionAnswersAdd(w, &r.Answers), options)
	}
	r.Usage = sampleUsage()
}

func fillRawUsage(r *RawUsage, records int) {
	r.Version = 1
	for i := 0; i < records; i++ {
		r.Records[i] = sampleRawRecord()
	}
	r.RecordsCount = int32(records)
}

func variableTables(t *testing.T) {
	optionP := varFns[OptionP, OptionPBuilder, *OptionPBuilder]{"OptionP", OptionPLoadBuilder, OptionPFromJson, OptionPLoadMeasureReason, OptionPMeasure, OptionPSave, OptionPToJsonMeasure, OptionPToJson, nil,
		func(_ *OptionPBuilder, r *OptionP, k chk) { optionPRules(r, k) }}
	answer := varFns[Answer, AnswerBuilder, *AnswerBuilder]{"Answer", AnswerLoadBuilder, AnswerFromJson, AnswerLoadMeasureReason, AnswerMeasure, AnswerSave, AnswerToJsonMeasure, AnswerToJson, nil,
		func(b *AnswerBuilder, r *Answer, k chk) { answerRules(&b.Arena, r, k) }}
	named := varFns[NamedAnswer, NamedAnswerBuilder, *NamedAnswerBuilder]{"NamedAnswer", NamedAnswerLoadBuilder, NamedAnswerFromJson, NamedAnswerLoadMeasureReason, NamedAnswerMeasure, NamedAnswerSave, NamedAnswerToJsonMeasure, NamedAnswerToJson, nil,
		func(b *NamedAnswerBuilder, r *NamedAnswer, k chk) { namedAnswerRules(&b.Arena, r, k) }}
	input := varFns[Input, InputBuilder, *InputBuilder]{"Input", InputLoadBuilder, InputFromJson, InputLoadMeasureReason, InputMeasure, InputSave, InputToJsonMeasure, InputToJson, nil,
		func(_ *InputBuilder, r *Input, k chk) { inputRules(r, k) }}
	decision := varFns[Decision, DecisionBuilder, *DecisionBuilder]{"Decision", DecisionLoadBuilder, DecisionFromJson, DecisionLoadMeasureReason, DecisionMeasure, DecisionSave, DecisionToJsonMeasure, DecisionToJson,
		func(b *DecisionBuilder) *TableArena { return &b.Arena },
		func(b *DecisionBuilder, r *Decision, k chk) { decisionRules(&b.Arena, r, k) }}
	line := varFns[RecordLine, RecordLineBuilder, *RecordLineBuilder]{"RecordLine", RecordLineLoadBuilder, RecordLineFromJson, RecordLineLoadMeasureReason, RecordLineMeasure, RecordLineSave, RecordLineToJsonMeasure, RecordLineToJson,
		func(b *RecordLineBuilder) *TableArena { return &b.Arena },
		func(b *RecordLineBuilder, r *RecordLine, k chk) { recordLineRules(&b.Arena, r, k) }}
	raw := varFns[RawUsage, RawUsageBuilder, *RawUsageBuilder]{"RawUsage", RawUsageLoadBuilder, RawUsageFromJson, RawUsageLoadMeasureReason, RawUsageMeasure, RawUsageSave, RawUsageToJsonMeasure, RawUsageToJson, nil, func(_ *RawUsageBuilder, r *RawUsage, k chk) { rawUsageRules(r, k) }}

	t.Run("OptionP", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, optionP, func(_ *OptionPBuilder, r *OptionP) { fillOptionP(r, "done", 0.875) }, []vbound[OptionP, *OptionPBuilder]{
			{"option past 48 bytes", "option", "clamped", func(_ *OptionPBuilder, r *OptionP) { r.OptionLength = 49 }},
			{"option empty", "option", "absent_required", func(_ *OptionPBuilder, r *OptionP) { r.OptionLength = 0 }},
			{"probability above one", "p", "clamped", func(_ *OptionPBuilder, r *OptionP) { r.P = 1.0000001 }},
			{"probability below zero", "p", "clamped", func(_ *OptionPBuilder, r *OptionP) { r.P = -0.1 }},
			{"probability not a number", "p", "malformed", func(_ *OptionPBuilder, r *OptionP) { r.P = math.NaN() }},
			{"option not UTF-8", "option", "malformed", func(_ *OptionPBuilder, r *OptionP) { r.Option[0] = 0xff }},
		})
	})
	t.Run("Answer", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, answer, func(b *AnswerBuilder, r *Answer) { fillAnswer(&b.Main, r, 3) }, []vbound[Answer, *AnswerBuilder]{
			{"kind none", "kind", "clamped", func(_ *AnswerBuilder, r *Answer) { r.Kind = AnswerTypeNone }},
			{"an option above one", "p", "clamped", func(b *AnswerBuilder, r *Answer) { r.P.At(1, &b.Arena).P = 2 }},
			{"value empty", "value", "absent_required", func(_ *AnswerBuilder, r *Answer) { r.ValueLength = 0 }},
			{"method past 24 bytes", "method", "clamped", func(_ *AnswerBuilder, r *Answer) { r.MethodLength = 25 }},
		})
		// A choice names any number of options (decide.go sets no ceiling):
		// two, fifteen and forty round-trip in every form, each option intact.
		r := schemaread.NewReader(answer.codec())
		for _, options := range []int{2, 15, 40} {
			w := answer.fresh()
			fillAnswer(&w.Main, w.GetRoot(), options)
			for _, form := range []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON} {
				image, err := r.Write(&w, form)
				require.NoError(t, err, "%d options as %s", options, form)
				got, _, err := r.Read(image)
				require.NoError(t, err, "%d options as %s", options, form)
				root := answer.root(got)
				require.EqualValues(t, options, root.P.Count)
				for i := 0; i < options; i++ {
					o := root.P.At(int32(i), &got.Arena)
					assert.Equal(t, fmt.Sprintf("opt-%d", i), string(o.Option[:o.OptionLength]))
					assert.Equal(t, 1/float64(options), o.P)
				}
			}
		}
	})
	t.Run("NamedAnswer", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, named, func(b *NamedAnswerBuilder, r *NamedAnswer) { fillNamedAnswer(&b.Main, r, 2) }, []vbound[NamedAnswer, *NamedAnswerBuilder]{
			{"question past 32 bytes", "question", "clamped", func(_ *NamedAnswerBuilder, r *NamedAnswer) { r.QuestionLength = 33 }},
			{"question empty", "question", "absent_required", func(_ *NamedAnswerBuilder, r *NamedAnswer) { r.QuestionLength = 0 }},
			{"the answer's kind none", "kind", "clamped", func(_ *NamedAnswerBuilder, r *NamedAnswer) { r.Answer.Kind = AnswerTypeNone }},
		})
	})
	t.Run("Input", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, input, func(_ *InputBuilder, r *Input) { fillInput(r, 0) }, []vbound[Input, *InputBuilder]{
			{"key past 32 bytes", "key", "clamped", func(_ *InputBuilder, r *Input) { r.KeyLength = 33 }},
			{"value past 1024 bytes", "value", "clamped", func(_ *InputBuilder, r *Input) { r.ValueLength = 1025 }},
			{"key empty", "key", "absent_required", func(_ *InputBuilder, r *Input) { r.KeyLength = 0 }},
		})
	})
	t.Run("Decision", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, decision, func(b *DecisionBuilder, r *Decision) { fillDecision(&b.Main, r, 3, 1, 2, 200) }, []vbound[Decision, *DecisionBuilder]{
			{"id past 256 bytes", "id", "clamped", func(_ *DecisionBuilder, r *Decision) { r.IdLength = 257 }},
			{"id empty", "id", "absent_required", func(_ *DecisionBuilder, r *Decision) { r.IdLength = 0 }},
			{"decision name empty", "decision_name", "absent_required", func(_ *DecisionBuilder, r *Decision) { r.DecisionNameLength = 0 }},
			{"state not UTF-8", "state", "malformed", func(b *DecisionBuilder, r *Decision) { TableStringAt(&r.State, &b.Arena).Data[0] = 0xff }},
			{"an input value past 1024", "value", "clamped", func(b *DecisionBuilder, r *Decision) { r.Inputs.At(0, &b.Arena).ValueLength = 1025 }},
			{"an answer above one", "p", "clamped", func(b *DecisionBuilder, r *Decision) { r.Answers.At(0, &b.Arena).Answer.P.At(0, &b.Arena).P = 1.01 }},
			{"usage below zero", "output_tokens", "clamped", func(_ *DecisionBuilder, r *Decision) { r.Usage.OutputTokens = -5 }},
		})
		// The widest record the code writes: ten inputs (judgment.go
		// AnswerDecision), nine questions of fourteen options (brief.go), and
		// the largest state a card carries, in all three forms.
		r := schemaread.NewReader(decision.codec())
		w := decision.fresh()
		fillDecision(&w.Main, w.GetRoot(), 10, 9, 14, 2*16<<10+1024)
		want, err := r.Write(&w, schemaread.FormBinary)
		require.NoError(t, err)
		for _, form := range []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON} {
			image, err := r.Write(&w, form)
			require.NoError(t, err, form.String())
			got, _, err := r.Read(image)
			require.NoError(t, err, form.String())
			root := decision.root(got)
			assert.EqualValues(t, 10, root.Inputs.Count)
			assert.EqualValues(t, 9, root.Answers.Count)
			assert.EqualValues(t, 14, root.Answers.At(8, &got.Arena).Answer.P.Count)
			assert.Len(t, TableStringAt(&root.State, &got.Arena).Data, 2*16<<10+1024)
			again, err := r.Write(&got, schemaread.FormBinary)
			require.NoError(t, err)
			assert.Equal(t, want, again, form.String())
		}
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
			{"an outcome beside the decision", "as_outcome", "malformed", func(_ *RecordLineBuilder, r *RecordLine) { r.AsOutcome, r.AsOutcomePresent = sampleOutcome(), true }},
			{"an act beside the decision", "as_act", "malformed", func(_ *RecordLineBuilder, r *RecordLine) { r.AsAct, r.AsActPresent = sampleAct(), true }},
			{"the decision's id past 256", "id", "clamped", func(b *RecordLineBuilder, r *RecordLine) { DecisionAt(&r.AsDecision, &b.Arena).IdLength = 300 }},
		})
		// The other two kinds are lines of their own.
		rd := schemaread.NewReader(line.codec())
		for _, fill := range []func(*RecordLine){
			func(r *RecordLine) { r.Kind, r.AsOutcome, r.AsOutcomePresent = LineKindOutcome, sampleOutcome(), true },
			func(r *RecordLine) { r.Kind, r.AsAct, r.AsActPresent = LineKindAct, sampleAct(), true },
		} {
			w := line.fresh()
			fill(w.GetRoot())
			for _, form := range []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON} {
				image, err := rd.Write(&w, form)
				require.NoError(t, err)
				got, _, err := rd.Read(image)
				require.NoError(t, err)
				assert.Equal(t, line.root(w).Kind, line.root(got).Kind)
			}
		}
	})
	t.Run("RawUsage", func(t *testing.T) {
		t.Parallel()
		exerciseVar(t, raw, func(_ *RawUsageBuilder, r *RawUsage) { fillRawUsage(r, 2) }, []vbound[RawUsage, *RawUsageBuilder]{
			{"version two", "version", "clamped", func(_ *RawUsageBuilder, r *RawUsage) { r.Version = 2 }},
			{"version zero", "version", "clamped", func(_ *RawUsageBuilder, r *RawUsage) { r.Version = 0 }},
			{"no records", "records", "absent_required", func(_ *RawUsageBuilder, r *RawUsage) { r.RecordsCount = 0 }},
			{"271 records", "records", "clamped", func(_ *RawUsageBuilder, r *RawUsage) { r.RecordsCount = 271 }},
			{"a record's harness not muse", "harness", "malformed", func(_ *RawUsageBuilder, r *RawUsage) {
				put(r.Records[1].Harness[:], &r.Records[1].HarnessLength, "x")
			}},
		})
		// The most records the envelope's byte cap allows.
		rd := schemaread.NewReader(raw.codec())
		w := raw.fresh()
		fillRawUsage(w.GetRoot(), 270)
		for _, form := range []schemaread.Form{schemaread.FormBinary, schemaread.FormBase64, schemaread.FormJSON} {
			image, err := rd.Write(&w, form)
			require.NoError(t, err)
			got, _, err := rd.Read(image)
			require.NoError(t, err)
			assert.EqualValues(t, 270, raw.root(got).RecordsCount)
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
