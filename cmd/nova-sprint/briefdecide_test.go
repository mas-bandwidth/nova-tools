package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
)

// briefBackend is a fake brief backend: p(converges) is the number after
// "converges=" in the card, a card holding "vague" fails commit_stated and names
// step 2 ambiguous, and a card holding "down" is a backend failure. It counts asks.
type briefBackend struct {
	mu   sync.Mutex
	asks int
}

func (*briefBackend) Name() string { return "fake" }

func (b *briefBackend) Ask(ctx context.Context, s decide.Schema, state string) (map[string]decide.Answer, decide.Usage, error) {
	b.mu.Lock()
	b.asks++
	b.mu.Unlock()
	if strings.Contains(state, "down") {
		return nil, decide.Usage{}, errors.New("the backend answered HTTP 503")
	}
	p := func(v float64) *float64 { return &v }
	conv := 0.9
	if _, v, ok := strings.Cut(state, "converges="); ok {
		f, err := strconv.ParseFloat(strings.TrimRight(strings.Fields(v)[0], "."), 64)
		if err != nil {
			return nil, decide.Usage{}, err
		}
		conv = f
	}
	t := map[string]decide.FixedAnswer{"converges": {Noul: p(conv)},
		"minutes":        {Choice: "20-45", P: map[string]float64{"20-45": 0.6}},
		"ambiguous_step": {Choice: "none", P: map[string]float64{"none": 0.8}}}
	for _, q := range []string{"repo_branch", "files_named", "gate_stated", "commit_stated", "report_stated", "one_thing"} {
		t[q] = decide.FixedAnswer{Noul: p(0.9)}
	}
	if strings.Contains(state, "vague") {
		t["commit_stated"] = decide.FixedAnswer{Noul: p(0.2)}
		t["ambiguous_step"] = decide.FixedAnswer{Choice: "step-2", P: map[string]float64{"step-2": 0.7}}
	}
	return decide.Fixed{Table: t}.Ask(ctx, s, state)
}

// briefTestApp is a test app with the backend's key in its environment, the fake
// backend, a record of its own and the sprint row's bar.
func briefTestApp(t *testing.T, bar string) (*testApp, *briefBackend, string) {
	t.Helper()
	ta := newTestApp(t)
	b, record := &briefBackend{}, filepath.Join(t.TempDir(), "decide", "brief.jsonl")
	env := ta.a.getenv
	ta.a.getenv = func(k string) string {
		if k == decide.JevSecret {
			return "k-test"
		}
		return env(k)
	}
	ta.a.decideBackend = func(key string) decide.Backend {
		assert.Equal(t, "k-test", key)
		return b
	}
	ta.a.briefRecord = func() (string, error) { return record, nil }
	ta.a.briefBar = func(context.Context) (string, error) { return bar, nil }
	ta.ok("init --readers reader-a,reader-b --members m1")
	return ta, b, record
}

// endsOf is each card's brief outcome in the record, by card.
func endsOf(t *testing.T, record string) map[string]string {
	t.Helper()
	ds, err := decide.Load(record)
	require.NoError(t, err)
	out := map[string]string{}
	for _, d := range ds {
		if d.Outcome != nil {
			out[d.Inputs["card"]] = d.Outcome.Label + ": " + d.Outcome.Note
		}
	}
	return out
}

// Under JEV_API_KEY, add asks the brief decision of every card it names before it
// writes: one BRIEF line per card with p(converges), the minutes and the questions it
// failed, recorded under <card>@brief-<hex>. With no bar every card is added; the same
// brief asked again is answered from the record.
func TestAddAsksTheBriefDecisionOfEveryCard(t *testing.T) {
	t.Parallel()
	ta, b, record := briefTestApp(t, "")
	dir := t.TempDir()
	writeNeedsBrief(t, dir, "a1", "Fix a1. converges=0.8", "")
	writeNeedsBrief(t, dir, "a2", "Fix a2, vague. converges=0.3", "")
	out := ta.ok("add --stream s1 --brief-dir " + dir)
	assert.Contains(t, out, "BRIEF card=a1 op=a1@brief-")
	assert.Contains(t, out, " p_converges=0.80 minutes=20-45 failed=- recorded=new\n")
	assert.Contains(t, out, " p_converges=0.30 minutes=20-45 failed=commit_stated(0.20),ambiguous_step:step-2(0.70) recorded=new\n")
	assert.Contains(t, out, "MOVED a2 -> ready", "with no bar a low brief is reported, never refused")
	assert.Equal(t, 2, b.asks)
	ds, err := decide.Load(record)
	require.NoError(t, err)
	require.Len(t, ds, 2)
	assert.Equal(t, decide.BriefOp("a1", needsBrief("Fix a1. converges=0.8", "")), ds[0].ID)

	one := writeNeedsBrief(t, t.TempDir(), "x", "Fix it. converges=0.8", "")
	assert.Contains(t, ta.ok("add --stream s2 b1 b2 --brief-file "+one), "BRIEF card=b2 op=b2@brief-")
	assert.Equal(t, 4, b.asks, "one brief named with two ids is two decisions")
	var res output
	code, stdout, stderr := ta.do("add --stream s3 c1 --brief-file " + one + " --json")
	require.Equal(t, 0, code, stderr)
	require.NoError(t, json.Unmarshal([]byte(stdout), &res), "with --json the add's object is alone on stdout")
	assert.Contains(t, stderr, "BRIEF card=c1 ")
}

// With the sprint row's decide_brief_bar set, a card whose p(converges) is under it
// refuses the whole add, exit 2, nothing written, naming the card, its p and the
// questions it failed; the decisions are recorded all the same, so a rewritten brief
// is asked anew and the others are answered from the record.
func TestAddRefusesABriefUnderTheBar(t *testing.T) {
	t.Parallel()
	ta, b, _ := briefTestApp(t, "0.5")
	dir := t.TempDir()
	writeNeedsBrief(t, dir, "a1", "Fix a1. converges=0.8", "")
	low := writeNeedsBrief(t, dir, "a2", "Fix a2, vague. converges=0.3", "")
	code, _, stderr := ta.do("add --stream s1 --brief-dir " + dir)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "nova-sprint add REFUSED: the brief of a2 converges at p=0.30, under decide_brief_bar 0.50, failing commit_stated(0.20), ambiguous_step:step-2(0.70); a card is a flash child's whole brief")
	assert.NotContains(t, stderr, "the brief of a1")
	code, _, _ = ta.do("card a1")
	assert.NotEqual(t, 0, code, "nothing was written: a1 is not on the table")

	writeNeedsBrief(t, dir, "a2", "Fix a2: change x.go line 4. converges=0.7", "")
	require.FileExists(t, low)
	out := ta.ok("add --stream s1 --brief-dir " + dir)
	assert.Contains(t, out, "BRIEF card=a1 ")
	assert.Contains(t, out, "recorded=existing")
	assert.Contains(t, out, "MOVED a2 -> ready")
	assert.Equal(t, 3, b.asks, "a1 answered from the record; a2's rewritten brief asked anew")
}

// A brief decision that cannot be made is one NOTE line and the add goes on: no key
// asks nothing and says nothing; a bar that cannot be read reports only; a backend
// failure names the card.
func TestAddGoesOnWhenTheBriefDecisionCannotBeMade(t *testing.T) {
	t.Parallel()
	plain := newTestApp(t)
	called := false
	plain.a.decideBackend = func(string) decide.Backend { called = true; return &briefBackend{} }
	plain.a.briefRecord = func() (string, error) { return filepath.Join(t.TempDir(), "brief.jsonl"), nil }
	plain.ok("init --readers reader-a,reader-b --members m1")
	out := plain.ok("add --stream s1 a1 --brief-file " + writeNeedsBrief(t, t.TempDir(), "a", "Fix a. converges=0.1", ""))
	assert.NotContains(t, out, "BRIEF")
	assert.False(t, called, "with no key no backend is made")

	ta, _, _ := briefTestApp(t, "")
	ta.a.briefBar = func(context.Context) (string, error) { return "", errors.New("no route to nova-config") }
	dir := t.TempDir()
	writeNeedsBrief(t, dir, "a1", "Fix a1. converges=0.1", "")
	writeNeedsBrief(t, dir, "a2", "Fix a2, down.", "")
	out = ta.ok("add --stream s1 --brief-dir " + dir)
	assert.Contains(t, out, "NOTE brief: the sprint row's decide_brief_bar could not be read (no route to nova-config); every brief is reported and none refused\n")
	assert.Contains(t, out, "NOTE brief: no brief decision of a2: the backend answered HTTP 503\n")
	assert.Contains(t, out, "BRIEF card=a1 ")
	assert.Contains(t, out, "MOVED a2 -> ready")
}

// A card's end attaches to its brief decision: a drop attaches dropped with its
// reason, and a landing attaches landed at its attempt. A card with no brief
// decision attaches nothing and the verb says nothing of it.
func TestACardsEndAttachesToItsBrief(t *testing.T) {
	t.Parallel()
	ta, _, record := briefTestApp(t, "")
	dir := t.TempDir()
	writeNeedsBrief(t, dir, "a1", "Fix a1. converges=0.8", "")
	ta.ok("add --stream s1 --brief-dir " + dir)
	ta.ok("add --stream s1 --count 1")
	out := ta.ok("drop a1 s1-1 --reason obsolete")
	assert.NotContains(t, out, "brief decision")
	assert.Equal(t, map[string]string{"a1": "dropped: obsolete"}, endsOf(t, record))

	r := newLandRig(t)
	r.a.briefRecord = ta.a.briefRecord
	r.ok("add --stream s2 --count 2")
	_, err := decide.Briefs(context.Background(), &briefBackend{}, map[string]string{"s2-1": "brief of s2-1"}, record, t0, 1, 0)
	require.NoError(t, err)
	r.queued(map[string]string{"s2-1": r.head("s2-1", "main", "one.txt", "one\n"), "s2-2": r.head("s2-2", "main", "two.txt", "two\n")}, "s2-1", "s2-2")
	code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
	require.Equal(t, 0, code, out+errs)
	assert.NotContains(t, out, "brief decision")
	assert.Equal(t, map[string]string{"a1": "dropped: obsolete", "s2-1": "landed: landed at attempt 1"}, endsOf(t, record))
}
