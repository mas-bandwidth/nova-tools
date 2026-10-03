package decide

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func p(v float64) *float64 { return &v }

// A schema names every problem at once, so one fix is one turn.
func TestParseSchemaNamesEveryProblem(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, raw, says string }{
		{"no name", `{"questions":{"a":{"type":"noul","instructions":"x"}}}`, "it has no name"},
		{"no questions", `{"name":"d","questions":{}}`, "it has no questions"},
		{"one option", `{"name":"d","questions":{"c":{"type":"choice","instructions":"x","criteria":{"A":"a"}}}}`, "names 1 options"},
		{"noul with options", `{"name":"d","questions":{"n":{"type":"noul","instructions":"x","criteria":{"A":"a"}}}}`, "a noul is one statement"},
		{"unknown type", `{"name":"d","questions":{"s":{"type":"score","instructions":"x"}}}`, `type "score"; it wants choice or noul`},
		{"empty statement", `{"name":"d","questions":{"n":{"type":"noul","instructions":" "}}}`, "has no instructions"},
		{"unknown field", `{"name":"d","questions":{},"floor":0.9}`, "unknown field"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseSchema([]byte(tc.raw))
			assert.ErrorContains(t, err, tc.says)
		})
	}
	_, err := ParseSchema([]byte(`{"questions":{}}`))
	assert.ErrorContains(t, err, "it has no name; a decision is named so its record can be calibrated; it has no questions", "both problems in one error")
	s, err := ParseSchema([]byte(`{"name":"d","questions":{"n":{"type":"noul","instructions":"x"}}}`))
	require.NoError(t, err)
	assert.NotEqual(t, ReadSchema().Hash(), s.Hash(), "two schemas asking different questions have different hashes")
}

// The read schema is a schema like any other, and its verdict rule is pinned
// three ways: the three options, each option's rule as SPEC-NOVA-DECIDE section
// 6 states it word for word, and the schema hash the fixture record carries, so
// a reworded or deleted rule turns this red and a stale fixture is named.
func TestReadSchemaIsValid(t *testing.T) {
	t.Parallel()
	s := ReadSchema()
	assert.Empty(t, s.Problems())
	assert.Len(t, s.Questions, 5)
	verdict := s.Questions["verdict"]
	require.Equal(t, []string{Bounce, Land, Unsure}, slices.Sorted(maps.Keys(verdict.Criteria)))
	spec, err := os.ReadFile(filepath.Join("..", "..", "docs", "SPEC-NOVA-DECIDE.md"))
	require.NoError(t, err)
	for option, rule := range verdict.Criteria {
		row := "| `" + option + "` | " + rule + " |"
		assert.True(t, strings.Contains(string(spec), row), "SPEC-NOVA-DECIDE section 6 does not state the %s rule as the schema asks it; want the row %q", option, row)
	}
	fixture, err := Load(filepath.Join("..", "..", "cmd", "nova-decide", "testdata", "record.jsonl"))
	require.NoError(t, err)
	require.NotEmpty(t, fixture)
	assert.Equal(t, fixture[0].Schema, s.Hash(), "the read schema changed and cmd/nova-decide/testdata/record.jsonl (and its docs/TESTS.md transcript) still carry the old hash: regenerate the fixture with the new schema")
}

// A backend's answers are held to the schema; nothing is repaired.
func TestCheckRefusesAnswersThatDoNotFit(t *testing.T) {
	t.Parallel()
	s := ReadSchema()
	good := map[string]Answer{
		"does_task": noulAnswer(0.9), "lines_changed": noulAnswer(0.9), "inside_paths": noulAnswer(0.9), "defect": noulAnswer(0.1),
		"verdict": {Type: Choice, Value: Land, P: map[string]float64{Land: 0.9}},
	}
	require.NoError(t, s.Check(good))
	for _, tc := range []struct {
		name string
		edit func(map[string]Answer)
		says string
	}{
		{"missing", func(a map[string]Answer) { delete(a, "defect") }, "no answer to defect"},
		{"wrong type", func(a map[string]Answer) { a["defect"] = Answer{Type: Choice, Value: Land} }, "defect answered as choice, asked as noul"},
		{"unknown option", func(a map[string]Answer) { a["verdict"] = Answer{Type: Choice, Value: "MAYBE"} }, `verdict chose "MAYBE"`},
		{"probability out of range", func(a map[string]Answer) { a["defect"] = noulAnswer(1.5) }, "outside [0, 1]"},
		{"not asked", func(a map[string]Answer) { a["extra"] = noulAnswer(0.5) }, "an answer to extra, which was not asked"},
		{"choice without its p", func(a map[string]Answer) { a["verdict"] = Answer{Type: Choice, Value: Land} }, `gives its choice "LAND" no probability`},
		{"p of an unknown option", func(a map[string]Answer) {
			a["verdict"] = Answer{Type: Choice, Value: Land, P: map[string]float64{Land: 0.6, "MAYBE": 0.4}}
		}, `a probability to "MAYBE"`},
		{"noul without yes", func(a map[string]Answer) { a["defect"] = Answer{Type: Noul, Value: "no"} }, "defect is a noul and gives no probability of yes alone"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := map[string]Answer{}
			for k, v := range good {
				a[k] = v
			}
			tc.edit(a)
			assert.ErrorContains(t, s.Check(a), tc.says)
		})
	}
}

// The Jev backend sends {state, model, questions} and decodes the answers, the
// usage, and a choice that reports only its confidence; the transport is a fake.
func TestJevAsksThroughTheInjectedSend(t *testing.T) {
	t.Parallel()
	var sent map[string]any
	send := func(_ context.Context, body []byte) ([]byte, error) {
		require.NoError(t, json.Unmarshal(body, &sent))
		return []byte(`{"answers":{
			"does_task":{"type":"noul","noul":0.8},"lines_changed":{"type":"noul","noul":0.7},
			"inside_paths":{"type":"noul","noul":0.99},"defect":{"type":"noul","noul":0.35},
			"verdict":{"type":"choice","choice":"BOUNCE","confidence":0.6}},
			"usage":{"input_tokens":1200,"output_tokens":40}}`), nil
	}
	answers, usage, err := Ask(context.Background(), Jev{Model: JevModel, Send: send}, ReadSchema(), "CARD\nDIFF\n")
	require.NoError(t, err)
	assert.Equal(t, "CARD\nDIFF\n", sent["state"])
	assert.Equal(t, JevModel, sent["model"])
	assert.Len(t, sent["questions"], 5)
	assert.Equal(t, Usage{InputTokens: 1200, OutputTokens: 40}, usage)
	assert.Equal(t, "no", answers["defect"].Value)
	assert.InDelta(t, 0.35, answers["defect"].Prob("yes"), 1e-9)
	assert.Equal(t, Answer{Type: Choice, Value: Bounce, P: map[string]float64{Bounce: 0.6}}, answers["verdict"])
}

// What the transport or the backend gets wrong is an error, never an answer.
func TestJevErrorsAreErrors(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, reply, says string }{
		{"not json", `<html>`, "not the answers shape"},
		{"unknown type", `{"answers":{"defect":{"type":"score"}}}`, `type "score"`},
		{"short", `{"answers":{"defect":{"type":"noul","noul":0.1}}}`, "no answer to does_task"},
		{"no probability", `{"answers":{"defect":{"type":"noul"},"verdict":{"type":"choice","choice":"LAND"}}}`,
			"the backend answered defect, verdict with no probability; a missing number is never read as 0"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			send := func(context.Context, []byte) ([]byte, error) { return []byte(tc.reply), nil }
			_, _, err := Ask(context.Background(), Jev{Model: JevModel, Send: send}, ReadSchema(), "s")
			assert.ErrorContains(t, err, tc.says)
		})
	}
	down := errors.New("the backend did not answer")
	_, _, err := Ask(context.Background(), Jev{Send: func(context.Context, []byte) ([]byte, error) { return nil, down }}, ReadSchema(), "s")
	assert.ErrorIs(t, err, down)
	_, _, err = Ask(context.Background(), Fixed{}, ReadSchema(), "  ")
	assert.ErrorContains(t, err, "the state is empty")
}

// The fixed backend answers from its table and names every question it lacks.
func TestFixedAnswersFromItsTable(t *testing.T) {
	t.Parallel()
	f, err := ParseFixed([]byte(`{"defect":{"noul":0.2},"verdict":{"choice":"LAND","p":{"LAND":0.9}}}`))
	require.NoError(t, err)
	_, _, err = f.Ask(context.Background(), ReadSchema(), "s")
	assert.ErrorContains(t, err, "no answer to does_task, inside_paths, lines_changed")
	f.Table["does_task"], f.Table["inside_paths"], f.Table["lines_changed"] = FixedAnswer{Noul: p(1)}, FixedAnswer{Noul: p(1)}, FixedAnswer{Noul: p(0.5)}
	a, _, err := Ask(context.Background(), f, ReadSchema(), "s")
	require.NoError(t, err)
	assert.Equal(t, "yes", a["lines_changed"].Value, "0.5 is yes")
	_, err = ParseFixed([]byte(`{"defect":{"yes":1}}`))
	assert.ErrorContains(t, err, "unknown field")
}

// The read's state is the card, the rule when given, and the diff, in that order.
func TestReadStateHoldsTheCardTheRuleAndTheDiff(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "CARD (the whole task the worker was given):\nc\n\nDIFF (what the worker committed, unified):\nd\n", ReadState("c\n", "d\n", ""))
	assert.Contains(t, ReadState("c", "d", "r"), "c\n\nRULE (the reader holds the diff to this as well):\nr\n\nDIFF")
}

func decisionFor(id, state string) Decision {
	return Decision{ID: id, Decision: ReadName, Schema: ReadSchema().Hash(), State: state,
		Answers: map[string]Answer{"defect": noulAnswer(0.2)}}
}

// The record appends, refuses an id reused over another state, returns the
// recorded decision for an id retried, and folds outcomes in on load.
func TestTheRecordAppendsOnceAndFoldsOutcomes(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "decisions.jsonl")
	ds, err := Load(path)
	require.NoError(t, err)
	assert.Empty(t, ds, "a record not made yet is empty")

	have, err := Append(path, decisionFor("a", "one"))
	require.NoError(t, err)
	assert.Nil(t, have)
	have, err = Append(path, decisionFor("a", "one"))
	require.NoError(t, err)
	assert.Equal(t, "a", have.ID, "the same id over the same state is the recorded decision")
	_, err = Append(path, decisionFor("a", "two"))
	var conflict *ConflictError
	assert.ErrorAs(t, err, &conflict)
	other := decisionFor("a", "one")
	other.Schema = "another"
	_, err = Append(path, other)
	assert.ErrorAs(t, err, &conflict, "the same id and state under another schema is not a replay")

	d, changed, err := Attach(path, Outcome{ID: "a", Label: "ok", At: "t1"})
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, "ok", d.Outcome.Label)
	_, changed, err = Attach(path, Outcome{ID: "a", Label: "ok", At: "t2"})
	require.NoError(t, err)
	assert.False(t, changed, "the same label again changes nothing")
	_, _, err = Attach(path, Outcome{ID: "a", Label: "wrong"})
	assert.ErrorAs(t, err, &conflict)
	assert.ErrorContains(t, err, "labelled ok already (at t1), not wrong")
	_, _, err = Attach(path, Outcome{ID: "nobody", Label: "ok"})
	assert.ErrorIs(t, err, ErrUnknown)

	ds, err = Load(path)
	require.NoError(t, err)
	require.Len(t, ds, 1)
	assert.Equal(t, Outcome{ID: "a", Label: "ok", At: "t1"}, *ds[0].Outcome)
	raw, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, 2, bytes.Count(raw, []byte("\n")), "one decision line and one outcome line")
}

// A record that is not one is named by its file and line.
func TestLoadNamesTheBadLine(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, tc := range []struct{ name, body, says string }{
		{"not json", "{\"decision\":{\"id\":\"a\"}}\nnope\n", ":2 is not a record line"},
		{"neither", "{}\n", ":1 is neither a decision nor an outcome"},
		{"twice", "{\"decision\":{\"id\":\"a\"}}\n{\"decision\":{\"id\":\"a\"}}\n", ":2 records decision a a second time"},
		{"orphan outcome", "{\"outcome\":{\"id\":\"a\",\"label\":\"ok\"}}\n", "an outcome for a"},
		{"second outcome", "{\"decision\":{\"id\":\"a\"}}\n{\"outcome\":{\"id\":\"a\",\"label\":\"ok\"}}\n{\"outcome\":{\"id\":\"a\",\"label\":\"wrong\"}}\n",
			":3 records an outcome for a a second time"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name+".jsonl")
			require.NoError(t, os.WriteFile(path, []byte(tc.body), 0o600))
			_, err := Load(path)
			assert.ErrorContains(t, err, tc.says)
		})
	}
}

func labelled(id, schema string, defect float64, verdict, label string) Decision {
	d := Decision{ID: id, Decision: ReadName, Schema: schema, Answers: map[string]Answer{
		"defect":  noulAnswer(defect),
		"verdict": {Type: Choice, Value: verdict, P: map[string]float64{verdict: 0.8}},
	}}
	if label != "" {
		d.Outcome = &Outcome{ID: id, Label: label}
	}
	return d
}

// Calibration scores the labelled decisions of the newest schema, and reads
// the AUC, each bar and the catch-all bar from them.
func TestCalibrateReadsTheBarFromTheRecord(t *testing.T) {
	t.Parallel()
	ds := []Decision{
		labelled("old", "s0", 0.99, Bounce, "ok"), // another schema: never pooled
		labelled("a", "s1", 0.1, Land, "ok"),
		labelled("b", "s1", 0.6, Bounce, "ok"),
		labelled("c", "s1", 0.4, Land, "wrong"),
		labelled("d", "s1", 0.9, Bounce, "wrong"),
		labelled("e", "s1", 0.5, Land, "ugly"), // neither positive nor negative
		labelled("f", "s1", 0.5, Land, ""),     // no outcome yet
	}
	c, err := Calibrate(ds, ReadName, "defect", []string{"wrong"}, []string{"ok"})
	require.NoError(t, err)
	assert.Equal(t, "s1", c.Schema)
	assert.Equal(t, 3, c.Skipped)
	assert.InDelta(t, 0.75, c.AUC(), 1e-9, "three of four (wrong, ok) pairs ordered right")
	assert.Equal(t, Bar{At: 0.5, Caught: 1, Bounced: 1}, c.At(0.5))
	assert.Equal(t, Bar{At: 0.4, Caught: 2, Bounced: 1}, c.CatchAll())

	v, err := Calibrate(ds, ReadName, "verdict=BOUNCE", []string{"wrong"}, []string{"ok"})
	require.NoError(t, err)
	assert.Equal(t, []float64{0, 0.8}, v.Positives, "a choice is scored by the named option's p, 0 where it gave none")

	for _, tc := range []struct{ name, question, says string }{
		{"noul as choice", "defect=yes", "score a noul as <name>"},
		{"choice as noul", "verdict", "score a noul as <name>"},
		{"not asked", "lines_changed", "asked no question lines_changed"},
		{"unknown option", "verdict=BOUNCEE", "chose verdict=BOUNCEE or gave it a probability"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Calibrate(ds, ReadName, tc.question, []string{"wrong"}, []string{"ok"})
			assert.ErrorContains(t, err, tc.says)
		})
	}
	_, err = Calibrate(ds, "triage", "defect", []string{"wrong"}, []string{"ok"})
	assert.ErrorContains(t, err, "no decision named triage")
	_, err = Calibrate(ds, ReadName, "defect", []string{"lost"}, []string{"ok"})
	assert.ErrorContains(t, err, "0 positive and 2 negative")
}

// roundTrip is an http.RoundTripper in a function: the request is answered in
// process and no socket is opened.
type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// HTTPSend posts the body with the key as a bearer token and JSON as its type;
// a status other than 200 is an error naming the status and the body's head,
// and never the key.
func TestHTTPSendPostsWithTheKeyAndNamesAFailure(t *testing.T) {
	t.Parallel()
	const key = "k-secret-test"
	for _, tc := range []struct {
		name   string
		status int
		body   string
		says   string
	}{
		{"ok", http.StatusOK, `{"answers":{}}`, ""},
		{"payment", http.StatusPaymentRequired, `{"detail":{"error_type":"billing_error"}}`, `HTTP 402: "{\"detail\":{\"error_type\":\"billing_error\"}}"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var got *http.Request
			var sent []byte
			client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
				got = r
				var err error
				sent, err = io.ReadAll(r.Body)
				require.NoError(t, err)
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), Header: http.Header{}}, nil
			})}
			raw, err := HTTPSend(client, "https://decide.example.invalid/v1", key)(context.Background(), []byte(`{"state":"s"}`))
			require.NotNil(t, got)
			assert.Equal(t, http.MethodPost, got.Method)
			assert.Equal(t, "Bearer "+key, got.Header.Get("Authorization"))
			assert.Equal(t, "application/json", got.Header.Get("Content-Type"))
			assert.Equal(t, `{"state":"s"}`, string(sent))
			if tc.says == "" {
				require.NoError(t, err)
				assert.Equal(t, tc.body, string(raw))
				return
			}
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.says)
			assert.NotContains(t, err.Error(), key, "the key is never in an error")
		})
	}
	down := errors.New("connection refused")
	client := &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) { return nil, down })}
	_, err := HTTPSend(client, "https://decide.example.invalid/v1", key)(context.Background(), nil)
	assert.ErrorIs(t, err, down)
	assert.NotContains(t, err.Error(), key)
}
