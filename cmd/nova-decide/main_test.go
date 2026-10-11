package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/decide"
	"github.com/mas-bandwidth/nova-tools/pkg/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testWorld is the tool's world in a test: a fixed clock, the key given (or
// none), and a Jev transport that answers reply and counts its calls. No test
// opens a socket.
func testWorld(key string, calls *atomic.Int32, reply func(body []byte) ([]byte, error)) world {
	return world{
		now:      func() time.Time { return time.Date(2026, 10, 2, 21, 0, 0, 0, time.UTC) },
		getenv:   func(name string) string { return map[string]string{decide.JevSecret: key}[name] },
		deadline: decide.BriefDeadline,
		send: func(got string, _ time.Duration) decide.Send {
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
		{Args: []string{"read", "--card", td + "card.md", "--diff", td + "card.diff", "--backend", "jev", "--record", rec}, Code: 2,
			Says: "REFUSED reason=key_absent: JEV_API_KEY is absent"},
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
		{Args: []string{"gate", "--output", td + "card.diff", "--card", td + "card.md", "--backend", "fixed", "--answers", td + "gate-answers.json", "--record", rec}, Code: 2,
			Says: "holds no go test failure (no `--- FAIL:` or `FAIL <pkg>` line)"},
		{Args: []string{"gate", "--output", td + "gate-output.txt", "--card", td + "card.md", "--backend", "fixed", "--answers", td + "gate-answers.json", "--record", rec, "--bars", "0.5,0.5"}, Code: 2,
			Says: "sum to at most 1"},
		{Args: []string{"gate", "--output", td + "gate-output.txt", "--card", td + "card.md", "--backend", "fixed", "--answers", td + "gate-answers.json", "--record", rec, "--bars", "0.8"}, Code: 2,
			Says: "wants two probabilities, the flaky and the pre-existing bar"},
		{Args: []string{"gate", "--output", td + "nope.txt", "--card", td + "nope.md", "--backend", "fixed", "--answers", td + "gate-answers.json", "--record", rec}, Code: 2,
			Says: "nope.md"},
		// a dry run whose backend cannot be made is refused as a dry run, never as a verb that may have written
		{Args: []string{"gate", "--output", td + "gate-output.txt", "--card", td + "card.md", "--backend", "fixed", "--answers", td + "nope.json", "--record", rec, "--dry-run"}, Code: 2,
			Says: "nope.json: no such file"},
		{Args: []string{"read", "--card", td + "card.md", "--diff", td + "card.diff", "--backend", "fixed", "--answers", td + "nope.json", "--record", rec, "--dry-run"}, Code: 2,
			Says: "nope.json: no such file"},
		{Args: []string{"ask", "--schema", td + "schema.json", "--state", td + "state.txt", "--backend", "fixed", "--answers", td + "nope.json", "--record", rec, "--dry-run"}, Code: 2,
			Says: "nope.json: no such file"},
	})
	r := cli.Do(t, "ask", "--schema", td+"schema.json", "--state", td+"state.txt", "--backend", "fixed", "--answers", td+"read-answers.json", "--record", rec)
	r.Exit(2)
	assert.Contains(t, r.Stderr, "ASK FAILED id=reply-")
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

// A backend that fails is FAILED at exit 2 with nothing recorded; a backend
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
			assert.Contains(t, r.Stderr, "READ FAILED ")
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

// TestFindingsDoesNotMergeACardIDWithACommaIntoTwoCards pins one-token card fields.
func TestFindingsDoesNotMergeACardIDWithACommaIntoTwoCards(t *testing.T) {
	t.Parallel()
	calls := new(atomic.Int32)
	jev := testkit.Main(decideTool(testWorld("k-test", calls, jevScoreReply(0.83))).Run)
	rec := filepath.Join(t.TempDir(), "decisions.jsonl")
	score := []string{"score", "--card", td + "card.md", "--diff", td + "card.diff", "--backend", "jev", "--record", rec, "--op", ""}
	for _, op := range []string{"card,a@landed@0123456789ab", `other card\tag@landed@0123456789ac`} {
		args := append([]string(nil), score...)
		args[len(args)-1] = op
		jev.Do(t, args...).Exit(0)
	}
	out := jev.Do(t, "findings", "--record", rec).Exit(0)
	const want = `FINDINGS FINDING class=stranded_fragment count=2 cards=card\x2ca,other\x20card\tag`
	out.Out(want)
	fields := strings.Fields(strings.SplitN(out.Stdout, "FINDINGS FINDING ", 2)[1])
	var cards string
	var count int
	for _, field := range fields {
		if value, ok := strings.CutPrefix(field, "cards="); ok {
			cards = value
		}
		if value, ok := strings.CutPrefix(field, "count="); ok {
			count, _ = strconv.Atoi(value)
		}
	}
	assert.Equal(t, count, len(strings.Split(cards, ",")), "escaped card IDs split into exactly the reported number of cards")
}

// A score decision whose at is not RFC 3339 cannot be placed in the window, so findings
// skips it, and says so: the note names its id and the count. A decision of another kind
// with a bad at is not a score and is not mentioned (security#79 finding 3, second shape).
func TestFindingsNamesAScoreDecisionWhoseAtDoesNotParse(t *testing.T) {
	t.Parallel()
	rec := filepath.Join(t.TempDir(), "decisions.jsonl")
	for _, d := range []decide.Decision{
		{ID: "c1@landed@aaaaaaaaaaaa", Decision: decide.ScoreName, At: "2026-10-03T00:00:00Z"},
		{ID: "c2@landed@bbbbbbbbbbbb", Decision: decide.ScoreName, At: "yesterday"},
		{ID: "c3@read@cccccccccccc", Decision: "read", At: "last week"},
	} {
		_, err := decide.Append(rec, d)
		require.NoError(t, err)
	}
	jev := testkit.Main(decideTool(testWorld("k-test", new(atomic.Int32), jevScoreReply(0.83))).Run)
	jev.Do(t, "findings", "--record", rec, "--since", "2026-10-02").Exit(0).Out(
		"FINDINGS OK scored=1 ",
		"FINDINGS NOTE 1 score decisions skipped: at is not RFC 3339: c2@landed@bbbbbbbbbbbb",
	).NotOut("c3@read")
}

// jevChoice answers a one-choice decision (attempt, grade) with the option at p, the rest of
// the mass on the other options.
func jevChoice(question, option string, p float64) func([]byte) ([]byte, error) {
	return func(body []byte) ([]byte, error) {
		var req struct{ Questions map[string]decide.Question }
		if err := json.Unmarshal(body, &req); err != nil || len(req.Questions) != 1 {
			return nil, errors.New("not a one-question request")
		}
		probs := map[string]float64{}
		rest := (1 - p) / float64(len(req.Questions[question].Criteria)-1)
		for o := range req.Questions[question].Criteria {
			probs[o] = rest
		}
		probs[option] = p
		return json.Marshal(map[string]any{"answers": map[string]any{question: map[string]any{"type": "choice", "choice": option, "probabilities": probs}},
			"usage": map[string]int{"input_tokens": 400, "output_tokens": 0}})
	}
}

// The attempt and grade verbs ask their decisions through the backend and record them, the
// same op id again answered from the record; their states are the brief, the result and the
// reason (attempt), the brief alone (grade); a result that is not given is said as none; a
// dry run asks nothing; the refusals name every missing input at once.
func TestAttemptAndGradeAreAskedThroughJevAndRecordedOnce(t *testing.T) {
	t.Parallel()
	calls := new(atomic.Int32)
	rec := filepath.Join(t.TempDir(), "decisions.jsonl")
	attempt := testkit.Main(decideTool(testWorld("k-test", calls, jevChoice(decide.AttemptQuestion, decide.ClassNoResult, 0.91))).Run)
	args := []string{"attempt", "--brief", td + "card.md", "--reason", "budget: no RESULT.md shape", "--backend", "jev", "--record", rec, "--op", "c1@1"}
	attempt.Do(t, append(args, "--dry-run")...).Exit(0).Out("ATTEMPT OK id=c1@1 decision=attempt backend=jev:jev-latest questions=1", "recorded=no")
	assert.Zero(t, calls.Load())
	attempt.Do(t, args...).Exit(0).Out("ATTEMPT OK id=c1@1 decision=attempt backend=jev:jev-latest class=no-result p=0.91 tokens_in=400 tokens_out=0 recorded=new")
	attempt.Do(t, args...).Exit(0).Out("recorded=existing")
	assert.Equal(t, int32(1), calls.Load())

	grade := testkit.Main(decideTool(testWorld("k-test", calls, jevChoice(decide.GradeQuestion, decide.GradeScript, 0.8))).Run)
	grade.Do(t, "grade", "--brief", td+"card.md", "--backend", "jev", "--record", rec, "--op", "c1@grade").Exit(0).Out("GRADE OK id=c1@grade decision=grade backend=jev:jev-latest grade=script p=0.8")
	ds, err := decide.Load(rec)
	require.NoError(t, err)
	require.Len(t, ds, 2)
	assert.Contains(t, ds[0].State, "RESULT (the child's RESULT.md):\n(none: the child wrote no RESULT.md)\n\nREASON (the member's line for the take's end):\nbudget: no RESULT.md shape\n")
	assert.Equal(t, "budget: no RESULT.md shape", ds[0].Inputs["reason"])
	assert.True(t, strings.HasPrefix(ds[1].State, "CARD (the whole task a worker will be given):\n"), ds[1].State)

	testkit.Refusals(t, cli, []testkit.Refusal{
		{Args: []string{"attempt", "--backend", "fixed", "--answers", td + "attempt-answers.json", "--record", rec}, Code: 2, Says: "--brief is required"},
		{Args: []string{"attempt", "--backend", "fixed", "--answers", td + "attempt-answers.json", "--record", rec}, Code: 2, Says: "--reason is required"},
		{Args: []string{"attempt", "--brief", td + "nope.md", "--result", td + "nope-result.md", "--reason", "r", "--backend", "fixed", "--answers", td + "attempt-answers.json", "--record", rec}, Code: 2, Says: "nope-result.md"},
		{Args: []string{"grade", "--backend", "fixed", "--answers", td + "grade-answers.json", "--record", rec}, Code: 2, Says: "--brief is required"},
	})
	r := cli.Do(t, "grade", "--brief", td+"card.md", "--backend", "fixed", "--answers", td+"attempt-answers.json", "--record", rec, "--op", "g2")
	r.Exit(2)
	assert.Contains(t, r.Stderr, "GRADE FAILED id=g2 backend=fixed: the answers file has no answer to grade", "an attempt's answers do not answer a grade")
}

// gateReply answers each failure of the gate decision by the test its state names:
// TestPortInUse flaky, every other caused.
func gateReply(body []byte) ([]byte, error) {
	var req struct {
		State     string
		Questions map[string]any
	}
	if err := json.Unmarshal(body, &req); err != nil || len(req.Questions) != 1 {
		return nil, errors.New("not a gate request")
	}
	p := map[string]float64{"caused": 0.9, "flaky": 0.05, "pre-existing": 0.05}
	if strings.Contains(req.State, "TestPortInUse in") {
		p = map[string]float64{"caused": 0.05, "flaky": 0.85, "pre-existing": 0.1}
	}
	return json.Marshal(map[string]any{"answers": map[string]any{"class": map[string]any{"type": "choice", "choice": "caused", "probabilities": p}},
		"usage": map[string]int{"input_tokens": 400, "output_tokens": 0}})
}

// gate asks each failing test of the output once through the backend, one decision under
// <op>/<pkg>.<Test>, and prints each failure's class, p and route and the gate's route: with
// no --bars (unset, the sprint row's default) every failure routes caused, and --bars routes
// it; the same op again asks nothing; --base-red names the tests red at the base in each
// state, and the record keeps no key. A dry run asks nothing and says which failures the
// record holds already. With no --op its id is the decision's name and a hash.
func TestGateAsksEachFailureOnceAndRoutesTheGate(t *testing.T) {
	t.Parallel()
	calls := new(atomic.Int32)
	jev := testkit.Main(decideTool(testWorld("k-test", calls, gateReply)).Run)
	rec := filepath.Join(t.TempDir(), "decisions.jsonl")
	gate := []string{"gate", "--output", td + "gate-output.txt", "--card", td + "card.md", "--diff", td + "card.diff", "--base-red", "TestPortInUse", "--backend", "jev", "--record", rec}
	jev.Do(t, append(gate, "--op", "c1@1@gate", "--dry-run")...).Exit(0).Out("GATE OK op=c1@1@gate decision=gate backend=jev:jev-latest failures=2 recorded=no dry_run=true",
		"GATE FAILURE key=example/tools/internal/serve.TestPortInUse id=c1@1@gate/example/tools/internal/serve.TestPortInUse state_bytes=")
	assert.Zero(t, calls.Load())
	jev.Do(t, append(gate, "--op", "c1@1@gate")...).Exit(0).Out(
		"GATE OK op=c1@1@gate decision=gate backend=jev:jev-latest failures=2 route=caused",
		"GATE FAILURE key=example/tools/internal/serve.TestPortInUse id=c1@1@gate/example/tools/internal/serve.TestPortInUse class=caused p=caused:0.05,flaky:0.85,pre-existing:0.1 route=caused recorded=new",
		"GATE FAILURE key=example/tools/internal/greet.TestGreetNamesTheReader id=c1@1@gate/example/tools/internal/greet.TestGreetNamesTheReader class=caused p=caused:0.9,flaky:0.05,pre-existing:0.05 route=caused recorded=new")
	assert.Equal(t, int32(2), calls.Load())
	jev.Do(t, append(gate, "--op", "c1@1@gate", "--dry-run")...).Exit(0).Out("GATE OK op=c1@1@gate decision=gate backend=jev:jev-latest failures=2 recorded=existing dry_run=true",
		"GATE FAILURE key=example/tools/internal/serve.TestPortInUse id=c1@1@gate/example/tools/internal/serve.TestPortInUse state_bytes=", "class=caused recorded=existing")
	r := jev.Do(t, "gate", "--output", td+"gate-output.txt", "--card", td+"card.md", "--diff", td+"card.diff", "--backend", "jev", "--record", rec, "--op", "c1@1@gate", "--dry-run")
	assert.Equal(t, 2, r.Code, "the record holds the op over another state (no --base-red): the dry run refuses it, as the run would")
	assert.Contains(t, r.Stderr, "the op id c1@1@gate/example/tools/internal/serve.TestPortInUse is recorded for another decision, schema or state")
	assert.Equal(t, int32(2), calls.Load(), "a dry run asks nothing")
	jev.Do(t, append(gate, "--op", "c1@1@gate", "--bars", "0.8,0.8")...).Exit(0).Out("route=flaky recorded=existing")
	jev.Do(t, append(gate, "--op", "c1@1@gate", "--bars", "0.8,")...).Exit(0).Out("route=flaky recorded=existing")
	assert.Equal(t, int32(2), calls.Load(), "the same op asks nothing")
	jev.Do(t, append(gate, "--op", "c1@1@gate", "--bars", "0.9,0.8")...).Exit(0).Out("GATE OK op=c1@1@gate", "route=caused recorded=existing")
	ds, err := decide.Load(rec)
	require.NoError(t, err)
	require.Len(t, ds, 2)
	assert.Contains(t, ds[0].State, "AT THE BASE (the same test on the commit the card started from): red\n")
	assert.Contains(t, ds[1].State, "AT THE BASE (the same test on the commit the card started from): green\n")
	assert.Contains(t, ds[0].State, "CARD PATHS (the files the card may change): internal/greet/greet_test.go\n")
	raw, err := os.ReadFile(rec)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), "k-test")
	r = jev.Do(t, "gate", "--output", td+"gate-output.txt", "--card", td+"card.md", "--backend", "jev", "--record", rec, "--json")
	r.Exit(0)
	var got struct {
		Facts map[string]any `json:"facts"`
	}
	require.NoError(t, json.Unmarshal([]byte(r.Stdout), &got), r.Stdout)
	assert.Regexp(t, `^gate-[0-9a-f]{12}$`, got.Facts["op"])
}

// jevBrief is a brief answered by the model: p(converges) 0.3 for a card that says
// "vague" and 0.8 for any other; a card that says "down" is an HTTP failure.
func jevBrief(body []byte) ([]byte, error) {
	var req struct {
		State     string
		Questions map[string]any
	}
	if err := json.Unmarshal(body, &req); err != nil || len(req.Questions) != 9 {
		return nil, errors.New("not a brief request")
	}
	if strings.Contains(req.State, "down") {
		return nil, errors.New("the backend answered HTTP 503")
	}
	conv := 0.8
	if strings.Contains(req.State, "vague") {
		conv = 0.3
	}
	answers := map[string]any{
		"converges":      map[string]any{"type": "noul", "noul": conv},
		"ambiguous_step": map[string]any{"type": "choice", "choice": "none", "confidence": 0.9},
		"minutes":        map[string]any{"type": "choice", "choice": "10-20", "probabilities": map[string]float64{"10-20": 0.7, "20-45": 0.3}},
	}
	for _, q := range []string{"repo_branch", "files_named", "gate_stated", "commit_stated", "report_stated", "one_thing"} {
		answers[q] = map[string]any{"type": "noul", "noul": 0.9}
	}
	return json.Marshal(map[string]any{"answers": answers, "usage": map[string]int{"input_tokens": 700, "output_tokens": 20}})
}

// brief asks every card of a directory as one batch (its *.md files as add --brief-dir
// reads them: a directory named *.md, and what is below, are no cards): one BRIEF CARD
// line per card in id order, each recorded under <card>@brief-<hex>; asked again it asks nothing; a card
// the backend failed is named on its line, nothing is recorded for it, the rest are,
// and the verb fails at exit 2 so a script stops and runs it again.
func TestBriefAsksEveryCardOfADirectoryOnce(t *testing.T) {
	t.Parallel()
	calls := new(atomic.Int32)
	jev := testkit.Main(decideTool(testWorld("k-test", calls, jevBrief)).Run)
	dir, rec := t.TempDir(), filepath.Join(t.TempDir(), "decisions.jsonl")
	greet, err := os.ReadFile(td + "greet.md")
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub.md"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub.md", "below.md"), greet, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a1.md"), greet, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a2.md"), []byte("STEP 1. Make it better, somehow (vague).\n"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not a card"), 0o600))
	brief := []string{"brief", "--card", dir, "--backend", "jev", "--record", rec}

	jev.Do(t, append(brief, "--dry-run")...).Exit(0).Out("BRIEF OK decision=brief backend=jev:jev-latest cards=2 recorded=0 to_ask=2")
	assert.Zero(t, calls.Load())
	op := decide.BriefOp("a1", strings.TrimSuffix(string(greet), "\n"))
	jev.Do(t, brief...).Exit(0).Out(
		"BRIEF OK decision=brief backend=jev:jev-latest cards=2 asked=2 existing=0 failed=0",
		"BRIEF CARD id=a1 op="+op+" p_converges=0.8 minutes=10-20 failed=- uncalibrated=true recorded=new",
		"BRIEF CARD id=a2 op=a2@brief-")
	jev.Do(t, brief...).Exit(0).Out("asked=0 existing=2 failed=0", "p_converges=0.3 minutes=10-20 failed=- uncalibrated=true recorded=existing")
	assert.Equal(t, int32(2), calls.Load(), "a recorded card asks nothing")

	require.NoError(t, os.WriteFile(filepath.Join(dir, "a3.md"), []byte("the backend is down for this one\n"), 0o600))
	r := jev.Do(t, brief...)
	r.Exit(2)
	assert.Contains(t, r.Stdout+r.Stderr, "BRIEF FAILED ")
	assert.Contains(t, r.Stdout+r.Stderr, "the backend answered 2 of 3 cards")
	assert.Contains(t, r.Stdout+r.Stderr, `BRIEF CARD id=a3 op=a3@brief-`)
	assert.Contains(t, r.Stdout+r.Stderr, `error="the backend answered HTTP 503"`)
	ds, err := decide.Load(rec)
	require.NoError(t, err)
	assert.Len(t, ds, 2, "nothing is recorded for the card the backend failed")
	assert.Equal(t, op, ds[0].ID)

	testkit.Refusals(t, jev, []testkit.Refusal{
		{Args: []string{"brief", "--card", td + "nope", "--backend", "jev", "--record", rec}, Code: 2, Says: "nope: no such file"},
		{Args: []string{"brief", "--card", dir, "--backend", "jev", "--record", rec, "--width", "0"}, Code: 2, Says: "--width must be at least 1"},
		{Args: []string{"brief", "--card", t.TempDir(), "--backend", "jev", "--record", rec, "--dry-run"}, Code: 2, Says: "holds no *.md card file"},
	})
}

// brief's whole batch has one deadline (the world's, decide.BriefDeadline, as nova-sprint
// add's), not only --timeout per card: a backend that never answers holds the verb that
// long and no longer, every card unanswered is named on its line, nothing is recorded,
// and the verb fails at exit 2. The bubble's clock is synctest's: no real time.
func TestBriefEndsAtTheBatchDeadline(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		w := testWorld("k-test", new(atomic.Int32), nil)
		w.send = func(string, time.Duration) decide.Send {
			return func(ctx context.Context, _ []byte) ([]byte, error) { <-ctx.Done(); return nil, ctx.Err() }
		}
		jev := testkit.Main(decideTool(w).Run)
		dir, rec := t.TempDir(), filepath.Join(t.TempDir(), "decisions.jsonl")
		for _, id := range []string{"a1", "a2", "a3"} {
			require.NoError(t, os.WriteFile(filepath.Join(dir, id+".md"), []byte("STEP 1. Fix "+id+".\n"), 0o600))
		}
		start := time.Now()
		r := jev.Do(t, "brief", "--card", dir, "--backend", "jev", "--record", rec, "--width", "1", "--timeout", "10m")
		assert.Equal(t, w.deadline, time.Since(start), "one deadline for the whole batch, under a longer --timeout")
		r.Exit(2)
		assert.Equal(t, 3, strings.Count(r.Stdout+r.Stderr, "context deadline exceeded"), "each card unanswered is named")
		assert.NoFileExists(t, rec, "nothing was recorded")
	})
}

// score-grades tables one UTC day's grade decisions against the sprint log: the columns,
// and the daily window (a grade at 23:59:59 the day before and one at midnight after are
// out; a card the fleet never dealt is left out of the tables). The record and the log are
// written here, so the test reads no file beyond its own (docs/SPEC-NOVA-DECIDE.md section 11).
func TestScoreGradesJevAgainstOutcomes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	rec, logf := filepath.Join(dir, "grades.jsonl"), filepath.Join(dir, "sprint-log.json")
	grades := []string{
		`{"decision": {"id": "c1@grade.0123456789ab", "decision": "grade", "schema": "s", "backend": "fixed", "at": "2026-10-03T09:00:00Z", "inputs": {}, "state": "x", "answers": {"grade": {"type": "choice", "value": "flash", "p": {"flash": 0.9, "pro": 0.05, "script": 0.05}}}, "usage": {}}}`,
		`{"decision": {"id": "c2@grade.0123456789ab", "decision": "grade", "schema": "s", "backend": "fixed", "at": "2026-10-03T10:00:00Z", "inputs": {}, "state": "x", "answers": {"grade": {"type": "choice", "value": "flash", "p": {"flash": 0.6, "pro": 0.3, "script": 0.1}}}, "usage": {}}}`,
		`{"decision": {"id": "c3@grade.0123456789ab", "decision": "grade", "schema": "s", "backend": "fixed", "at": "2026-10-03T11:00:00Z", "inputs": {}, "state": "x", "answers": {"grade": {"type": "choice", "value": "pro", "p": {"flash": 0.1, "pro": 0.8, "script": 0.1}}}, "usage": {}}}`,
		`{"decision": {"id": "c4@grade.0123456789ab", "decision": "grade", "schema": "s", "backend": "fixed", "at": "2026-10-02T23:59:59Z", "inputs": {}, "state": "x", "answers": {"grade": {"type": "choice", "value": "flash", "p": {"flash": 0.9, "pro": 0.05, "script": 0.05}}}, "usage": {}}}`,
		`{"decision": {"id": "c5@grade.0123456789ab", "decision": "grade", "schema": "s", "backend": "fixed", "at": "2026-10-04T00:00:00Z", "inputs": {}, "state": "x", "answers": {"grade": {"type": "choice", "value": "flash", "p": {"flash": 0.9, "pro": 0.05, "script": 0.05}}}, "usage": {}}}`,
		`{"decision": {"id": "c6@grade.0123456789ab", "decision": "grade", "schema": "s", "backend": "fixed", "at": "2026-10-03T12:00:00Z", "inputs": {}, "state": "x", "answers": {"grade": {"type": "choice", "value": "flash", "p": {"flash": 0.9, "pro": 0.05, "script": 0.05}}}, "usage": {}}}`,
		`{"decision": {"id": "c7@grade.0123456789ab", "decision": "grade", "schema": "s", "backend": "fixed", "at": "2026-10-03T13:00:00Z", "inputs": {}, "state": "x", "answers": {"grade": {"type": "choice", "value": "flash", "p": {"flash": 0.9, "pro": 0.05, "script": 0.05}}}, "usage": {}}}`,
	}
	lines := []string{
		`{"card": "c1", "primary": "c1", "set": {"cost_record:c1#1": "kind=work card=c1 attempt=1 on_route=r on_tier=flash end=ok"}}`,
		`{"card": "c1", "primary": "c1", "table": "work", "to": "work:landed"}`,
		`{"card": "c2", "primary": "c2", "set": {"cost_record:c2#1": "kind=work card=c2 attempt=1 on_route=r on_tier=flash end=failed"}}`,
		`{"card": "c2", "primary": "c2", "set": {"cost_record:c2#2": "kind=work card=c2 attempt=2 on_route=r on_tier=pro end=ok"}}`,
		`{"card": "c2", "primary": "c2", "set": {"cost_record:c2#3": "kind=work card=c2 attempt=3 on_route=r on_tier=pro end=ok"}}`,
		`{"card": "c2", "primary": "c2", "table": "work", "to": "work:landed"}`,
		`{"card": "c3", "primary": "c3", "set": {"cost_record:c3#1": "kind=work card=c3 attempt=1 on_route=r on_tier=pro end=ok"}}`,
		`{"card": "c3", "primary": "c3", "table": "work", "to": "work:working"}`,
		`{"card": "c4", "primary": "c4", "set": {"cost_record:c4#1": "kind=work card=c4 attempt=1 on_route=r on_tier=flash end=ok"}}`,
		`{"card": "c4", "primary": "c4", "table": "work", "to": "work:landed"}`,
		`{"card": "c5", "primary": "c5", "set": {"cost_record:c5#1": "kind=work card=c5 attempt=1 on_route=r on_tier=flash end=ok"}}`,
		`{"card": "c5", "primary": "c5", "table": "work", "to": "work:landed"}`,
		`{"card": "c6", "primary": "c6", "set": {"cost_record:c6#1": "kind=work card=c6 attempt=1 on_route=- on_tier=- end=ok"}}`,
		`{"card": "c6", "primary": "c6", "table": "work", "to": "work:landed"}`,
	}
	require.NoError(t, os.WriteFile(rec, []byte(strings.Join(grades, "\n")+"\n"), 0o600))
	require.NoError(t, os.WriteFile(logf, []byte(`{"lines": [`+strings.Join(lines, ",")+`]}`), 0o600))
	args := []string{"score-grades", "--record", rec, "--log", logf}
	cli.Do(t, append(args, "--day", "2026-10-03")...).Exit(0).Out(
		"SCORE-GRADES OK day=2026-10-03 decisions=5 cards=5 no_log=1",
		"GRADE jev=flash dealt=flash n=2 landed2=1 landed=2 to_pro=1 dropped=0 open=0",
		"GRADE jev=pro dealt=pro n=1 landed2=0 landed=0 to_pro=0 dropped=0 open=1",
		"BUCKET jev=flash p=<0.7 n=1 att1_failed=1 landed2=0",
		"BUCKET jev=flash p=0.85-0.95 n=1 att1_failed=0 landed2=1")
	// the default day is the day before the world's clock (2026-10-02 21:00Z)
	cli.Do(t, args...).Exit(0).Out("day=2026-10-01 decisions=0 cards=0 no_log=0")
	cli.Do(t, append(args, "--day", "2026-10-02")...).Exit(0).Out("decisions=1 cards=1 no_log=0")
	testkit.Refusals(t, cli, []testkit.Refusal{
		{Args: append(args, "--day", "tuesday"), Code: 2, Says: "--day \"tuesday\" is not a date"},
		{Args: []string{"score-grades", "--record", rec}, Code: 2, Says: "--log is required"},
		{Args: []string{"score-grades", "--record", rec, "--log", filepath.Join(dir, "nope.json")}, Code: 2, Says: "nope.json"},
	})
}
