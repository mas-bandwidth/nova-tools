package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testWorld is the tool's world in a test: a fixed clock, the key given (or
// none), and a Jev transport that answers reply and counts its calls. No test
// opens a socket.
func testWorld(key string, calls *atomic.Int32, reply func(body []byte) ([]byte, error)) world {
	return world{
		now:    func() time.Time { return time.Date(2026, 10, 2, 21, 0, 0, 0, time.UTC) },
		getenv: func(name string) string { return map[string]string{decide.JevSecret: key}[name] },
		send: func(got string) decide.Send {
			return func(_ context.Context, body []byte) ([]byte, error) {
				if got != key {
					return nil, errors.New("the transport was handed another key")
				}
				calls.Add(1)
				return reply(body)
			}
		},
	}
}

// cli is the tool with no key and a transport that is never reached.
var cli = testkit.Main(decideTool(testWorld("", new(atomic.Int32), func([]byte) ([]byte, error) {
	return nil, errors.New("no test without a key reaches the backend")
})).Run)

// jevReply is a read answered by the model: defect p, verdict BOUNCE.
func jevReply(defect float64) func([]byte) ([]byte, error) {
	return func(body []byte) ([]byte, error) {
		var req struct {
			State, Model string
			Questions    map[string]any
		}
		if err := json.Unmarshal(body, &req); err != nil || req.Model != decide.JevModel || len(req.Questions) != 5 {
			return nil, errors.New("not a read request")
		}
		return json.Marshal(map[string]any{
			"answers": map[string]any{
				"does_task": map[string]any{"type": "noul", "noul": 0.4}, "lines_changed": map[string]any{"type": "noul", "noul": 0.9},
				"inside_paths": map[string]any{"type": "noul", "noul": 0.95}, "defect": map[string]any{"type": "noul", "noul": defect},
				"verdict": map[string]any{"type": "choice", "choice": "BOUNCE", "probabilities": map[string]float64{"BOUNCE": 0.7, "LAND": 0.2, "UNSURE": 0.1}},
			},
			"usage": map[string]int{"input_tokens": 900, "output_tokens": 30},
		})
	}
}

const td = "testdata/"

func TestDecideToolMeetsTheStandard(t *testing.T) {
	t.Parallel()
	assert.Empty(t, decideTool(realWorld()).Problems())
}

// Every refusal names every problem of the invocation at once and the remedy.
func TestRefusalsNameEveryProblemAtOnce(t *testing.T) {
	t.Parallel()
	rec := filepath.Join(t.TempDir(), "decisions.jsonl")
	testkit.Refusals(t, cli, []testkit.Refusal{
		{Args: []string{"read"}, Code: 2, Says: "--card is required"},
		{Args: []string{"read"}, Code: 2, Says: "--backend is required"},
		{Args: []string{"read"}, Code: 2, Says: "--record is required"},
		{Args: []string{"read", "--card", td + "card.md", "--diff", td + "card.diff", "--backend", "jev", "--record", rec}, Code: 2,
			Says: "JEV_API_KEY is absent from this environment; run under `nova-secrets exec --only JEV_API_KEY"},
		{Args: []string{"read", "--card", td + "card.md", "--diff", td + "card.diff", "--backend", "fixed", "--record", rec}, Code: 2,
			Says: "--backend fixed answers from --answers <file>"},
		{Args: []string{"read", "--card", td + "card.md", "--diff", td + "card.diff", "--backend", "oracle", "--record", rec, "--timeout", "0s"}, Code: 2,
			Says: `--backend "oracle" is no backend`},
		{Args: []string{"read", "--card", td + "card.md", "--diff", td + "card.diff", "--backend", "oracle", "--record", rec, "--timeout", "0s"}, Code: 2,
			Says: "--timeout must be above zero"},
		{Args: []string{"read", "--card", td + "nope.md", "--diff", td + "nope.diff", "--backend", "fixed", "--answers", td + "read-answers.json", "--record", rec}, Code: 2,
			Says: "nope.diff"},
		{Args: []string{"ask", "--schema", td + "card.md", "--state", td + "state.txt", "--backend", "fixed", "--answers", td + "answers.json", "--record", rec}, Code: 2,
			Says: "the schema is not JSON"},
		{Args: []string{"outcome", "--record", rec, "--id", "x", "--label", "not ok"}, Code: 2, Says: "is not one word"},
		{Args: []string{"outcome", "--record", td + "record.jsonl", "--id", "nobody", "--label", "ok", "--dry-run"}, Code: 2, Says: "nobody: no decision with that id"},
		{Args: []string{"calibrate", "--record", td + "record.jsonl", "--decision", "read", "--question", "defect", "--positive", "wrong", "--negative", "ok", "--bars", "0.5,2"}, Code: 2,
			Says: `holds "2"; it wants probabilities from 0 to 1`},
		{Args: []string{"calibrate", "--record", td + "record.jsonl", "--decision", "read", "--question", "defect", "--positive", "lost", "--negative", "ok"}, Code: 2,
			Says: "a calibration wants at least one of each"},
		{Args: []string{"calibrate", "--record", td + "record.jsonl", "--decision", "read", "--question", "verdict=BOUNCEE", "--positive", "wrong", "--negative", "ok"}, Code: 2,
			Says: "chose verdict=BOUNCEE or gave it a probability"},
		{Args: []string{"ask", "--schema", td + "nope.json", "--state", td + "nope.txt", "--backend", "fixed", "--answers", td + "answers.json", "--record", rec}, Code: 2,
			Says: "nope.json"},
		{Args: []string{"ask", "--schema", td + "nope.json", "--state", td + "nope.txt", "--backend", "fixed", "--answers", td + "answers.json", "--record", rec}, Code: 2,
			Says: "nope.txt"},
	})
	r := cli.Do(t, "ask", "--schema", td+"schema.json", "--state", td+"state.txt", "--backend", "fixed", "--answers", td+"read-answers.json", "--record", rec)
	r.Exit(2)
	assert.Contains(t, r.Stderr, "ASK FAIL id=reply-")
	assert.Contains(t, r.Stderr, "the answers file has no answer to asks_something, kind; run: make --answers answer every question of the schema")
	assert.NoFileExists(t, rec, "no refusal and no failure wrote the record")
}

// A read through Jev is recorded with its usage; the same op id again returns
// the recorded decision and asks nothing; an outcome attaches once; calibrate
// reads it back.
func TestReadThroughJevIsRecordedOnceAndCalibrated(t *testing.T) {
	t.Parallel()
	calls := new(atomic.Int32)
	jev := testkit.Main(decideTool(testWorld("k-test", calls, jevReply(0.8))).Run)
	rec := filepath.Join(t.TempDir(), "decisions.jsonl")
	read := []string{"read", "--card", td + "card.md", "--diff", td + "card.diff", "--backend", "jev", "--record", rec}

	jev.Do(t, append(read, "--op", "c1", "--dry-run")...).Exit(0).Out("READ OK id=c1 decision=read backend=jev:jev-latest questions=5", "recorded=no dry_run=true")
	assert.Zero(t, calls.Load(), "a dry run asks nothing")
	assert.NoFileExists(t, rec, "a dry run writes nothing")

	jev.Do(t, append(read, "--op", "c1")...).Exit(0).Out(
		"READ OK id=c1 decision=read backend=jev:jev-latest verdict=BOUNCE p=0.7 tokens_in=900 tokens_out=30 recorded=new",
		"READ ANSWER question=defect type=noul value=yes p=yes:0.8",
		"READ ANSWER question=verdict type=choice value=BOUNCE p=BOUNCE:0.7,LAND:0.2,UNSURE:0.1")
	jev.Do(t, append(read, "--op", "c1")...).Exit(0).Out("recorded=existing")
	assert.Equal(t, int32(1), calls.Load(), "the op id retried asked nothing")
	jev.Do(t, append(read, "--op", "c1", "--rule", td+"state.txt")...).Refused("the op id c1 is recorded for another decision, schema or state")

	jev.Do(t, append(read, "--op", "c2")...).Exit(0)
	jev.Do(t, "outcome", "--record", rec, "--id", "c1", "--label", "wrong", "--note", "a fragment").Exit(0).Out("OUTCOME OK id=c1 decision=read label=wrong changed=true")
	jev.Do(t, "outcome", "--record", rec, "--id", "c1", "--label", "wrong").Exit(0).Out("changed=false")
	jev.Do(t, "outcome", "--record", rec, "--id", "c1", "--label", "ok").Exit(1)
	jev.Do(t, "outcome", "--record", rec, "--id", "c2", "--label", "ok").Exit(0)
	jev.Do(t, "calibrate", "--record", rec, "--decision", "read", "--question", "defect", "--positive", "wrong", "--negative", "ok").Exit(0).Out(
		"CALIBRATE OK decision=read", "positives=1 negatives=1 skipped=0 auc=0.5",
		"CALIBRATE BAR at=0.7 caught=1 of=1 bounced=1 of_negatives=1",
		"CALIBRATE CATCH-ALL at=0.8 caught=1 of=1 bounced=1 of_negatives=1")

	ds, err := decide.Load(rec)
	require.NoError(t, err)
	require.Len(t, ds, 2)
	assert.Equal(t, decide.Usage{InputTokens: 900, OutputTokens: 30}, ds[0].Usage)
	assert.Equal(t, "2026-10-02T21:00:00Z", ds[0].At)
	assert.Contains(t, ds[0].State, "CARD (the whole task the worker was given):\nREPO: example/tools")
	assert.Equal(t, "a fragment", ds[0].Outcome.Note)
	assert.Len(t, ds[0].Inputs["diff_sha256"], 64)
	raw, err := os.ReadFile(rec)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "k-test", "the key is never in the record")
}

// An op id replayed under another schema of the same name, over the same
// state, is refused: the recorded answers are to other questions.
func TestAnOpIDUnderAnotherSchemaIsRefused(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rec := filepath.Join(dir, "decisions.jsonl")
	other := filepath.Join(dir, "schema.json")
	require.NoError(t, os.WriteFile(other, []byte(`{"name":"reply","questions":{
		"asks_something":{"type":"noul","instructions":"The message asks for something."},
		"kind":{"type":"choice","instructions":"What it is.","criteria":{"question":"q","report":"r","request":"a"}}}}`), 0o600))
	ask := func(schema string) testkit.Ran {
		return cli.Do(t, "ask", "--schema", schema, "--state", td+"state.txt", "--backend", "fixed", "--answers", td+"answers.json", "--record", rec, "--op", "x")
	}
	ask(td + "schema.json").Exit(0).Out("recorded=new")
	ask(td + "schema.json").Exit(0).Out("recorded=existing")
	ask(other).Refused("the op id x is recorded for another decision, schema or state")
}

// A backend that fails is FAIL at exit 2 with nothing recorded; a backend
// that answers outside the schema is the same.
func TestABackendFailureRecordsNothing(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		reply func([]byte) ([]byte, error)
		says  string
	}{
		{"http", func([]byte) ([]byte, error) { return nil, errors.New("the backend answered HTTP 402") }, "HTTP 402"},
		{"short", func([]byte) ([]byte, error) { return []byte(`{"answers":{}}`), nil }, "no answer to defect"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := filepath.Join(t.TempDir(), "decisions.jsonl")
			m := testkit.Main(decideTool(testWorld("k", new(atomic.Int32), tc.reply)).Run)
			r := m.Do(t, "read", "--card", td+"card.md", "--diff", td+"card.diff", "--backend", "jev", "--record", rec)
			r.Exit(2)
			assert.Contains(t, r.Stderr, "READ FAIL")
			assert.Contains(t, r.Stderr, tc.says)
			assert.NoFileExists(t, rec)
		})
	}
}

// ask reads its state from stdin with --state -, and an id it makes up is the
// decision's name and a hash.
func TestAskReadsStdinAndNamesItsDecision(t *testing.T) {
	t.Parallel()
	rec := filepath.Join(t.TempDir(), "decisions.jsonl")
	r := cli.DoIn(t, "Please rerun the check.\n", "ask", "--schema", td+"schema.json", "--state", "-", "--backend", "fixed",
		"--answers", td+"answers.json", "--record", rec, "--json")
	r.Exit(0)
	var got struct {
		Facts map[string]any `json:"facts"`
		Items []map[string]any
	}
	require.NoError(t, json.Unmarshal([]byte(r.Stdout), &got), r.Stdout)
	assert.Regexp(t, `^reply-[0-9a-f]{12}$`, got.Facts["id"])
	assert.Equal(t, "fixed", got.Facts["backend"])
	ds, err := decide.Load(rec)
	require.NoError(t, err)
	require.Len(t, ds, 1)
	assert.Equal(t, "Please rerun the check.\n", ds[0].State)
	assert.Equal(t, "-", ds[0].Inputs["state"])
}

// jevScoreReply is a score answered by the model: every question asked, every class low
// but stranded_fragment at frag.
func jevScoreReply(frag float64) func([]byte) ([]byte, error) {
	return func(body []byte) ([]byte, error) {
		var req struct {
			Questions map[string]struct{ Type string }
		}
		if err := json.Unmarshal(body, &req); err != nil || len(req.Questions) != len(decide.ScoreSchema().Questions) {
			return nil, errors.New("not a score request")
		}
		answers := map[string]any{}
		for name, q := range req.Questions {
			switch {
			case q.Type == decide.Choice:
				answers[name] = map[string]any{"type": "choice", "choice": "BOUNCE", "probabilities": map[string]float64{"BOUNCE": 0.6, "LAND": 0.3, "UNSURE": 0.1}}
			case name == "stranded_fragment":
				answers[name] = map[string]any{"type": "noul", "noul": frag}
			case name == "inside_paths":
				answers[name] = map[string]any{"type": "noul", "noul": 0.97}
			default:
				answers[name] = map[string]any{"type": "noul", "noul": 0.1}
			}
		}
		return json.Marshal(map[string]any{"answers": answers, "usage": map[string]int{"input_tokens": 1200, "output_tokens": 40}})
	}
}

// A landed diff scored through Jev names its top class and p, is recorded under its
// landed op once, and findings clusters it; a score outside the window or below the bar
// counts nothing.
func TestScoreThroughJevNamesTheTopClassAndFindingsClustersIt(t *testing.T) {
	t.Parallel()
	calls := new(atomic.Int32)
	jev := testkit.Main(decideTool(testWorld("k-test", calls, jevScoreReply(0.83))).Run)
	rec := filepath.Join(t.TempDir(), "decisions.jsonl")
	score := []string{"score", "--card", td + "card.md", "--diff", td + "card.diff", "--backend", "jev", "--record", rec, "--op", "c1@landed@0123456789ab"}
	jev.Do(t, score...).Exit(0).Out(
		"SCORE OK id=c1@landed@0123456789ab decision=score backend=jev:jev-latest top=stranded_fragment p=0.83 tokens_in=1200 tokens_out=40 recorded=new",
		"SCORE ANSWER question=stranded_fragment type=noul value=yes p=yes:0.83")
	jev.Do(t, score...).Exit(0).Out("recorded=existing")
	assert.Equal(t, int32(1), calls.Load(), "the landed op retried asked nothing")
	jev.Do(t, "findings", "--record", rec, "--since", "2026-10-02").Exit(0).Out(
		"FINDINGS OK scored=1 classes=1 bar=0.5 since=2026-10-02T00:00:00Z",
		"FINDINGS FINDING class=stranded_fragment count=1 cards=c1")
	jev.Do(t, "findings", "--record", rec).Exit(0).Out("FINDINGS OK scored=1 classes=1 bar=0.5 since=2026-09-25T21:00:00Z")
	jev.Do(t, "findings", "--record", rec, "--bar", "0.9").Exit(0).Out("FINDINGS OK scored=1 classes=0 bar=0.9")
	jev.Do(t, "findings", "--record", rec, "--since", "2026-10-03").Exit(0).Out("FINDINGS OK scored=0 classes=0")
	testkit.Refusals(t, cli, []testkit.Refusal{
		{Args: []string{"findings", "--record", rec, "--since", "yesterday"}, Code: 2, Says: `--since "yesterday" is not an RFC 3339 time or a date`},
		{Args: []string{"findings", "--record", rec, "--bar", "0.5,0.7"}, Code: 2, Says: `--bar "0.5,0.7" wants one probability from 0 to 1`},
		{Args: []string{"score", "--card", td + "card.md", "--diff", td + "nope.diff", "--backend", "fixed", "--answers", td + "score-answers.json", "--record", rec}, Code: 2, Says: "nope.diff"},
	})
}
