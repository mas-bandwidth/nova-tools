package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shadowBench keeps one caller-owned feed, truth and record; transports are the
// existing world seam and never open a socket (SPEC-NOVA-DECIDE section 15).
type shadowBench struct {
	dir, manifest, record string
	rows                  []shadowInput
}

func newShadowBench(t *testing.T, n int) *shadowBench {
	t.Helper()
	dir := t.TempDir()
	b := &shadowBench{dir: dir, manifest: filepath.Join(dir, "feed.json"), record: filepath.Join(dir, "record.jsonl")}
	for i := 0; i < n; i++ {
		b.rows = append(b.rows, shadowInput{Task: "task-" + string(rune('a'+i)), Head: strings.Repeat(string(rune('a'+i)), 40), PromptVersion: "read-v1", Card: "card.md", Diff: "card.diff"})
	}
	for _, name := range []string{"card.md", "card.diff"} {
		raw, err := os.ReadFile(td + name)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), raw, 0600))
	}
	b.write(t)
	return b
}
func (b *shadowBench) write(t *testing.T) {
	t.Helper()
	raw, err := json.Marshal(b.rows)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(b.manifest, raw, 0600))
}
func (b *shadowBench) args(extra ...string) []string {
	return append([]string{"shadow", "--manifest", b.manifest, "--backend", "jev", "--record", b.record, "--budget", "3"}, extra...)
}
func (b *shadowBench) journal(t *testing.T) shadowJournal {
	t.Helper()
	var j shadowJournal
	require.NoError(t, shadowJSON(b.record+".shadow.json", &j))
	return j
}

func TestShadowBudgetAndTruthNeverReachTheRequest(t *testing.T) {
	t.Parallel()
	b := newShadowBench(t, 2)
	calls := new(atomic.Int32)
	truth := filepath.Join(b.dir, "truth.json")
	raw, err := json.Marshal([]shadowTruth{{Task: b.rows[0].Task, Head: b.rows[0].Head, PromptVersion: "read-v1", Label: "wrong", Note: "SECRET_TRUTH_ONLY"}})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(truth, raw, 0600))
	cli := testkit.Main(decideTool(testWorld("key", calls, func(body []byte) ([]byte, error) {
		assert.NotContains(t, string(body), "SECRET_TRUTH_ONLY")
		// Truth is read after the call: replacing it now determines the attached label.
		raw, err := os.ReadFile(truth)
		require.NoError(t, err)
		raw = []byte(strings.Replace(string(raw), `"wrong"`, `"ok"`, 1))
		require.NoError(t, os.WriteFile(truth, raw, 0600))
		response, err := jevReply(0.2)(body)
		require.NoError(t, err)
		var wire map[string]any
		require.NoError(t, json.Unmarshal(response, &wire))
		wire["answers"].(map[string]any)["verdict"].(map[string]any)["confidence"] = 0.83
		return json.Marshal(wire)
	})).Run)
	cli.Do(t, b.args("--truth", truth, "--dry-run")...).Exit(0).Out("asked=0 spent=0 remaining=3")
	assert.Zero(t, calls.Load())
	assert.NoFileExists(t, b.record+".shadow.json")
	cli.Do(t, b.args("--truth", truth)...).Exit(0).Out("asked=1 spent=1 remaining=2 pending=1")
	ds, err := decide.Load(b.record)
	require.NoError(t, err)
	require.Len(t, ds, 1)
	require.NotNil(t, ds[0].Outcome)
	assert.Equal(t, "ok", ds[0].Outcome.Label)
	assert.NotContains(t, ds[0].State, "SECRET_TRUTH_ONLY")
	assert.Contains(t, b.journal(t).Rows[ds[0].ID].RawResponse, `"confidence":0.83`)
	assert.Equal(t, decide.Usage{InputTokens: 900, OutputTokens: 30}, ds[0].Usage)
	cli.Do(t, b.args("--truth", truth)...).Exit(0).Out("asked=1 spent=2 remaining=1 pending=0")
	cli.Do(t, b.args("--truth", truth)...).Exit(0).Out("asked=0 spent=2")
	assert.Equal(t, int32(2), calls.Load())
}

func TestShadowRefusesDuplicatesChangedInputsAndLabelsBeforeCalling(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"duplicate", "label", "changed"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			b := newShadowBench(t, 1)
			calls := new(atomic.Int32)
			cli := testkit.Main(decideTool(testWorld("key", calls, jevReply(0.2))).Run)
			switch kind {
			case "duplicate":
				b.rows = append(b.rows, b.rows[0])
				b.write(t)
				cli.Do(t, b.args()...).Refused("duplicate task/head/prompt_version")
				assert.Zero(t, calls.Load())
			case "label":
				raw, err := os.ReadFile(b.manifest)
				require.NoError(t, err)
				raw = []byte(strings.Replace(string(raw), `"task":`, `"label":"ok","task":`, 1))
				require.NoError(t, os.WriteFile(b.manifest, raw, 0600))
				cli.Do(t, b.args()...).Refused(`unknown field "label"`)
				assert.Zero(t, calls.Load())
			case "changed":
				cli.Do(t, b.args()...).Exit(0)
				require.NoError(t, os.WriteFile(filepath.Join(b.dir, "card.md"), []byte("changed evidence"), 0600))
				cli.Do(t, b.args()...).Refused("shadow input changed")
				assert.Equal(t, int32(1), calls.Load())
			}
		})
	}
}

func TestShadowRestartsWithoutRepeatingUncertainOrCapturedCalls(t *testing.T) {
	t.Parallel()
	for _, stage := range []string{"reserved", "response", "failed"} {
		t.Run(stage, func(t *testing.T) {
			t.Parallel()
			b := newShadowBench(t, 1)
			calls := new(atomic.Int32)
			works, err := shadowWorks(b.manifest)
			require.NoError(t, err)
			v := works[0]
			body, err := json.Marshal(map[string]any{"model": decide.JevModel, "questions": v.SchemaValue.Questions, "state": v.StateValue})
			require.NoError(t, err)
			raw, err := jevReply(0.2)(body)
			require.NoError(t, err)
			r := shadowRow{Task: v.Task, Head: v.Head, PromptVersion: v.PromptVersion, Schema: v.SchemaValue.Hash(), StateHash: decide.Sum([]byte(v.StateValue)), Backend: "jev:jev-latest", Stage: stage, At: "2026-10-03T00:00:00Z"}
			if stage == "response" {
				r.RawResponse = string(raw)
			}
			j := shadowJournal{Budget: 3, Rows: map[string]shadowRow{v.ID: r}}
			require.NoError(t, j.save(b.record+".shadow.json"))
			cli := testkit.Main(decideTool(testWorld("key", calls, func([]byte) ([]byte, error) { return nil, errors.New("must never call the provider on restart") })).Run)
			if stage == "response" {
				cli.Do(t, b.args()...).Exit(0).Out("asked=0 spent=1")
				ds, err := decide.Load(b.record)
				require.NoError(t, err)
				require.Len(t, ds, 1)
				assert.Equal(t, "recorded", b.journal(t).Rows[v.ID].Stage)
			} else {
				cli.Do(t, b.args()...).Exit(2).Err("reservations are retained")
				assert.NoFileExists(t, b.record)
			}
			assert.Zero(t, calls.Load())
		})
	}
}

func TestShadowAdoptsLegacyEvidenceAndCountsItAgainstTheAllowance(t *testing.T) {
	t.Parallel()
	b := newShadowBench(t, 2)
	b.rows[0].Op = "legacy-full-task-short-head"
	b.write(t)
	works, err := shadowWorks(b.manifest)
	require.NoError(t, err)
	fixedRaw, err := os.ReadFile(td + "read-answers.json")
	require.NoError(t, err)
	fixed, err := decide.ParseFixed(fixedRaw)
	require.NoError(t, err)
	// The historical Jev decision's input is byte-identical; its full identity is sealed by the manifest.
	answers, usage, err := decide.Ask(t.Context(), fixed, works[0].SchemaValue, works[0].StateValue)
	require.NoError(t, err)
	_, err = decide.Append(b.record, decide.Decision{ID: works[0].ID, Decision: decide.ReadName, Schema: works[0].SchemaValue.Hash(), State: works[0].StateValue, Backend: "jev:jev-latest", Answers: answers, Usage: usage})
	require.NoError(t, err)
	calls := new(atomic.Int32)
	cli := testkit.Main(decideTool(testWorld("key", calls, jevReply(0.2))).Run)
	b.rows[0].Op = ""
	b.write(t)
	cli.Do(t, b.args()...).Refused("shadow record has unmapped decision")
	assert.Zero(t, calls.Load())
	b.rows[0].Op = works[0].ID
	b.write(t)
	args := b.args()
	for i, v := range args {
		if v == "3" {
			args[i] = "1"
		}
	}
	cli.Do(t, args...).Exit(0).Out("asked=0 spent=1 remaining=0 pending=1")
	assert.Zero(t, calls.Load())
	assert.True(t, b.journal(t).Rows[works[0].ID].Imported)
	// Omitting the legacy op must not buy a second call for the same tuple.
	b.rows[0].Op = ""
	b.write(t)
	cli.Do(t, args...).Refused("shadow tuple already recorded as legacy-full-task-short-head")
	assert.Zero(t, calls.Load())
	// Distinct prompt versions and heads are separate, permitted identities.
	b.rows = b.rows[:1]
	b.rows[0].PromptVersion = "read-v2"
	b.write(t)
	cli.Do(t, b.args()...).Refused("journal's budget differs")
	// The original allowance is immutable; use a separately seeded 3-call journal.
	j := b.journal(t)
	j.Budget = 3
	require.NoError(t, j.save(b.record+".shadow.json"))
	cli.Do(t, b.args()...).Exit(0).Out("asked=1 spent=2 remaining=1")
	b.rows[0].Head = strings.Repeat("c", 40)
	b.write(t)
	cli.Do(t, b.args()...).Exit(0).Out("asked=1 spent=3 remaining=0")
	assert.Equal(t, int32(2), calls.Load())
}

func TestShadowConcurrentInvocationsSpendOneCall(t *testing.T) {
	t.Parallel()
	b := newShadowBench(t, 1)
	calls := new(atomic.Int32)
	cli := testkit.Main(decideTool(testWorld("key", calls, jevReply(0.2))).Run)
	done := make(chan struct{})
	go func() { defer close(done); cli.Do(t, b.args()...).Exit(0) }()
	cli.Do(t, b.args()...).Exit(0)
	<-done
	assert.Equal(t, int32(1), calls.Load())
	ds, err := decide.Load(b.record)
	require.NoError(t, err)
	require.Len(t, ds, 1)
}

func TestShadowRecordsProviderErrorsWithoutRetrying(t *testing.T) {
	t.Parallel()
	b := newShadowBench(t, 1)
	calls := new(atomic.Int32)
	cli := testkit.Main(decideTool(testWorld("key", calls, func([]byte) ([]byte, error) { return nil, errors.New("bounded provider failure") })).Run)
	cli.Do(t, b.args()...).Exit(2).Err("spent=1 remaining=2", "bounded provider failure")
	cli.Do(t, b.args()...).Exit(2)
	assert.Equal(t, int32(1), calls.Load())
	r := b.journal(t).Rows[shadowID(b.rows[0].Task, b.rows[0].Head, "read-v1")]
	assert.Equal(t, "failed", r.Stage)
	assert.Contains(t, r.Error, "bounded provider failure")
	require.NotNil(t, r.ElapsedNS)
	assert.Equal(t, int64(0), *r.ElapsedNS)
}
