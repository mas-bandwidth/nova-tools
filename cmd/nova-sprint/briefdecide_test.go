package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/internal/sprint/store"
	"github.com/mas-bandwidth/nova-tools/pkg/decide"
	"github.com/mas-bandwidth/nova-tools/pkg/sprintwire"
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

// briefOpOf is the brief decision op a card stores, and the record it names.
func briefOpOf(t *testing.T, ta *testApp, id string) (op, record string) {
	t.Helper()
	var v cardView
	ta.json("card "+id, &v)
	require.NotNil(t, v.Primary)
	return v.Primary.F(sprint.FieldBriefOp), v.Primary.F(sprint.FieldBriefRecord)
}

// Under JEV_API_KEY, add asks the brief decision of every card it names after its own
// checks: one BRIEF line per card with p(converges), the minutes and the questions it
// failed, marked uncalibrated, recorded under <card>@brief-<hex>, and the card stores
// that op and the record. With no bar every card is added; the key is in no line and
// no record; with --json the add's object is alone on stdout and holds the lines.
func TestAddAsksTheBriefDecisionOfEveryCard(t *testing.T) {
	t.Parallel()
	ta, b, record := briefTestApp(t, "")
	dir := t.TempDir()
	writeNeedsBrief(t, dir, "a1", "Fix a1. converges=0.8", "")
	writeNeedsBrief(t, dir, "a2", "Fix a2, vague. converges=0.3", "")
	out := ta.ok("add --stream s1 --brief-dir " + dir)
	assert.Contains(t, out, "BRIEF card=a1 op=a1@brief-")
	assert.Contains(t, out, " p_converges=0.80 minutes=20-45 failed=- uncalibrated=true recorded=new\n")
	assert.Contains(t, out, " p_converges=0.30 minutes=20-45 failed=commit_stated(0.20),ambiguous_step:step-2(0.70) uncalibrated=true recorded=new\n")
	assert.Contains(t, out, "MOVED a2 -> ready", "with no bar a low brief is reported, never refused")
	assert.Equal(t, 2, b.asks)
	ds, err := decide.Load(record)
	require.NoError(t, err)
	require.Len(t, ds, 2)
	op, rec := briefOpOf(t, ta, "a1")
	assert.Equal(t, decide.BriefOp("a1", needsBrief("Fix a1. converges=0.8", "")), op)
	assert.Equal(t, ds[0].ID, op, "the card stores the op add printed")
	assert.Equal(t, record, rec)

	one := writeNeedsBrief(t, t.TempDir(), "x", "Fix it. converges=0.8", "")
	assert.Contains(t, ta.ok("add --stream s2 b1 b2 --brief-file "+one), "BRIEF card=b2 op=b2@brief-")
	assert.Equal(t, 4, b.asks, "one brief named with two ids is two decisions")
	var res output
	code, stdout, stderr := ta.do("add --stream s3 c1 --one --brief-file " + one + " --json")
	require.Equal(t, 0, code, stderr)
	require.NoError(t, json.Unmarshal([]byte(stdout), &res), "with --json the add's object is alone on stdout")
	require.Len(t, res.Brief, 1, "with --json the BRIEF line is the object's brief field")
	assert.Contains(t, res.Brief[0], "BRIEF card=c1 op=c1@brief-")
	assert.NotContains(t, stderr, "BRIEF")
	raw, err := os.ReadFile(record)
	require.NoError(t, err)
	assert.NotContains(t, string(raw)+out+stdout+stderr, "k-test", "the key is in no line and no record")
}

// The brief decision comes after every check of add's own, in both forms: an add its
// arguments refuse (--score included), whose brief the card lint refuses, whose cards
// share a file in PATHS, or whose brief is over the size, asks nothing; nor does a
// --brief-op typed on an add no server runs. A counting backend: zero calls.
func TestAddAsksNothingOfACardItRefuses(t *testing.T) {
	t.Parallel()
	ta, b, _ := briefTestApp(t, "")
	dir := t.TempDir()
	writeNeedsBrief(t, dir, "a1", "Fix a1. converges=0.8", "")
	one := filepath.Join(dir, "a1.md")
	bad := filepath.Join(t.TempDir(), "bad.md")
	require.NoError(t, os.WriteFile(bad, []byte("Fix it, with no rules at all. converges=0.9\n"), 0o600))
	shared := t.TempDir()
	writeHeaderBrief(t, shared, "q1", "-", "internal/x.go")
	writeHeaderBrief(t, shared, "q2", "-", "internal/x.go")
	big := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(big, "z1.md"), []byte(needsBrief("Fix z1. "+strings.Repeat("x", store.MaxBriefBytes), "")), 0o600))
	for _, c := range []struct{ args, says string }{
		{"add --brief-dir " + dir, "wants --stream"},
		{"add --stream s1 --brief-dir " + dir + " --score abc", "--score wants a number"},
		{"add --stream s1 a1 --one --brief-file " + one + " --score abc", "--score wants a number"},
		{"add --stream s1 --one --brief-file " + bad, "LINT DRIFT brief"},
		{"add --stream s1 b1 b2 --brief-file " + bad, "LINT DRIFT brief"},
		{"add --stream s1 --brief-dir " + shared, "internal/x.go is named in PATHS by q1 and q2"},
		{"add --stream s1 --brief-dir " + big, "over the"},
		{"add --stream s1 a1 --one --brief-file " + one + " --brief-op a1=a1@brief-deadbeef", "--brief-op is the served add's wire word"},
	} {
		code, _, stderr := ta.do(c.args)
		assert.Equal(t, 2, code, c.args)
		assert.Contains(t, stderr, c.says, c.args)
		assert.Zero(t, b.asks, "a refused add costs no call: %s", c.args)
	}
}

// A home directory that cannot be found leaves add no record to keep the decisions in:
// that is a fault, said on one NOTE line, nothing asked, and the add goes on.
func TestAddSaysWhenTheRecordCannotBeNamed(t *testing.T) {
	t.Parallel()
	ta, b, _ := briefTestApp(t, "")
	ta.a.briefRecord = func() (string, error) { return "", errors.New("$HOME is not defined") }
	out := ta.ok("add --stream s1 a1 --one --brief-file " + writeNeedsBrief(t, t.TempDir(), "a", "Fix a. converges=0.8", ""))
	assert.Contains(t, out, "NOTE brief: no brief decision: the record: $HOME is not defined\n")
	assert.Contains(t, out, "MOVED a1 -> ready")
	assert.Zero(t, b.asks)
}

// With the sprint row's decide_brief_bar set, a card whose p(converges) is under it
// refuses the whole add, exit 2, nothing written, naming the card, its p and the
// questions it failed, and saying the decision is uncalibrated; the decisions are
// recorded all the same, so a rewritten brief is asked anew and the others are
// answered from the record.
func TestAddRefusesABriefUnderTheBar(t *testing.T) {
	t.Parallel()
	ta, b, _ := briefTestApp(t, "0.5")
	dir := t.TempDir()
	writeNeedsBrief(t, dir, "a1", "Fix a1. converges=0.8", "")
	writeNeedsBrief(t, dir, "a2", "Fix a2, vague. converges=0.3", "")
	code, _, stderr := ta.do("add --stream s1 --brief-dir " + dir)
	assert.Equal(t, 2, code)
	assert.Contains(t, stderr, "nova-sprint add REFUSED: the brief of a2 ranks p(converges)=0.30, under decide_brief_bar 0.50, failing commit_stated(0.20), ambiguous_step:step-2(0.70); the brief decision is uncalibrated")
	assert.NotContains(t, stderr, "the brief of a1")
	code, _, _ = ta.do("card a1")
	assert.NotEqual(t, 0, code, "nothing was written: a1 is not on the table")

	writeNeedsBrief(t, dir, "a2", "Fix a2: change x.go line 4. converges=0.7", "")
	out := ta.ok("add --stream s1 --brief-dir " + dir)
	assert.Contains(t, out, "BRIEF card=a1 ")
	assert.Contains(t, out, "recorded=existing")
	assert.Contains(t, out, "MOVED a2 -> ready")
	assert.Equal(t, 3, b.asks, "a1 answered from the record; a2's rewritten brief asked anew")
}

// No key asks nothing and says nothing (the key is the opt-in: a keyless add is the add
// it was before the brief decision); a brief decision that cannot be made is one NOTE
// line and the add goes on: a bar that cannot be read reports only; a backend failure
// names the card.
func TestAddGoesOnWhenTheBriefDecisionCannotBeMade(t *testing.T) {
	t.Parallel()
	plain := newTestApp(t)
	called := false
	plain.a.decideBackend = func(string) decide.Backend { called = true; return &briefBackend{} }
	plain.a.briefRecord = func() (string, error) { return filepath.Join(t.TempDir(), "brief.jsonl"), nil }
	plain.ok("init --readers reader-a,reader-b --members m1")
	out := plain.ok("add --stream s1 a1 --one --brief-file " + writeNeedsBrief(t, t.TempDir(), "a", "Fix a. converges=0.1", ""))
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
	op, _ := briefOpOf(t, ta, "a2")
	assert.Empty(t, op, "a card with no decision stores no op")
}

// silentBackend answers nothing until its context is done.
type silentBackend struct{}

func (silentBackend) Name() string { return "hanging" }

func (silentBackend) Ask(ctx context.Context, _ decide.Schema, _ string) (map[string]decide.Answer, decide.Usage, error) {
	<-ctx.Done()
	return nil, decide.Usage{}, ctx.Err()
}

// A backend that never answers holds the add for the one batch deadline and no
// longer: the add then writes its cards, with one NOTE per card unanswered (in flight
// at the deadline, or never asked). The bubble's clock is synctest's: no real time.
func TestAHangingBackendHoldsAddForOneDeadline(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		ta, _, record := briefTestApp(t, "")
		ta.a.decideBackend = func(string) decide.Backend { return silentBackend{} }
		dir := t.TempDir()
		for i := range 10 {
			writeNeedsBrief(t, dir, "c"+strconv.Itoa(i), "Fix it.", "")
		}
		start := time.Now()
		out := ta.ok("add --stream s1 --brief-dir " + dir)
		assert.Equal(t, decide.BriefDeadline, time.Since(start), "one deadline for the whole batch")
		assert.Equal(t, 10, strings.Count(out, "context deadline exceeded"))
		assert.Equal(t, 10-decide.BriefWidth, strings.Count(out, "not asked: context deadline exceeded"), "past the width in flight, never asked")
		assert.Equal(t, 10, strings.Count(out, "NOTE brief: no brief decision of c"))
		assert.Equal(t, 10, strings.Count(out, "MOVED c"), "the add writes its cards")
		assert.NoFileExists(t, record, "nothing was recorded")
	})
}

// With a server, add's checks and its brief decisions run where add is typed (the
// caller's key and files), and the server is sent the op ids and the record with the
// add: the server asks nothing and the cards it writes store them.
func TestAServedAddAsksWhereItIsTyped(t *testing.T) {
	t.Parallel()
	r := newServerRig(t, "nova-sprint init --readers reader-a,reader-b --members m1")
	serverAsked := false
	r.a.decideBackend = func(string) decide.Backend { serverAsked = true; return &briefBackend{} }
	record := filepath.Join(t.TempDir(), "brief.jsonl")
	env := map[string]string{ServerEnv: "127.0.0.1:6390", "NOVA_SPRINT_ACTOR": "boss", decide.JevSecret: "k-test"}
	c := newApp(func(k string) string { return env[k] })
	t.Cleanup(c.close)
	b := &briefBackend{}
	c.decideBackend = func(string) decide.Backend { return b }
	c.briefBar = func(context.Context) (string, error) { return "", nil }
	var sent [][]string
	c.forward = func(_ context.Context, _ string, verbs ...[]string) ([]sprintwire.Result, error) {
		sent = append(sent, verbs...)
		return r.a.serveFrom(sprintwire.Request{Verbs: verbs}, true).Results, nil
	}
	dir := t.TempDir()
	writeNeedsBrief(t, dir, "a1", "Fix a1. converges=0.8", "")
	var out, errb bytes.Buffer
	require.Equal(t, 0, c.run([]string{"add", "--stream", "s1", "--brief-dir", dir, "--decide-record", record}, &out, &errb), errb.String())
	assert.Contains(t, out.String(), "BRIEF card=a1 op=a1@brief-")
	assert.Contains(t, out.String(), "MOVED a1 -> ready")
	assert.Equal(t, 1, b.asks)
	assert.False(t, serverAsked, "the server asks nothing")
	op := decide.BriefOp("a1", needsBrief("Fix a1. converges=0.8", ""))
	require.Len(t, sent, 1)
	assert.Contains(t, sent[0], "a1="+op, "the op ids go with the add")
	var v cardView
	require.NoError(t, json.Unmarshal([]byte(r.boss("nova-sprint card a1 --json")), &v))
	assert.Equal(t, op, v.Primary.F(sprint.FieldBriefOp))
	assert.Equal(t, record, v.Primary.F(sprint.FieldBriefRecord))

	// --json: the object is the server's, and the brief lines asked here are its brief field.
	writeNeedsBrief(t, dir, "a2", "Fix a2. converges=0.7", "")
	out.Reset()
	errb.Reset()
	require.Equal(t, 0, c.run([]string{"add", "--stream", "s2", "--brief-file", filepath.Join(dir, "a2.md"), "x2", "--one", "--decide-record", record, "--json"}, &out, &errb), errb.String())
	var res output
	require.NoError(t, json.Unmarshal(out.Bytes(), &res), out.String())
	require.Len(t, res.Moved, 1)
	assert.Contains(t, res.Moved[0], "x2 -> ready")
	require.Len(t, res.Brief, 1)
	assert.Contains(t, res.Brief[0], "BRIEF card=x2 op=x2@brief-")
	assert.NotContains(t, errb.String(), "BRIEF")

	// A --brief-op typed on the caller is refused here, with or without the key, and
	// nothing is sent; the server takes an op only as <id>@brief-..., the card's own.
	sent = nil
	for _, key := range []string{"k-test", ""} {
		env[decide.JevSecret] = key
		errb.Reset()
		assert.Equal(t, 2, c.run([]string{"add", "--stream", "s3", "y1", "--one", "--brief-file", filepath.Join(dir, "a2.md"), "--brief-op", "y1=y1@brief-deadbeef"}, &out, &errb))
		assert.Contains(t, errb.String(), "--brief-op is the served add's wire word")
	}
	assert.Empty(t, sent, "nothing is sent")
	res2 := r.a.serveFrom(sprintwire.Request{Verbs: [][]string{{"add", "--actor", "boss", "--stream", "s3", "y1", "--one", "--brief-file", filepath.Join(dir, "a2.md"), "--brief-op", "y1=a1@brief-" + strings.TrimPrefix(op, "a1@brief-")}}}, true).Results[0]
	assert.Equal(t, 2, res2.Code, res2.Stdout+res2.Stderr)
	assert.Contains(t, res2.Stderr, "names no brief decision of a card of this add")
	assert.NotEqual(t, 0, r.a.run([]string{"card", "y1"}, &out, &errb), "nothing was written")
}

// A card's end attaches to the brief decision it stores: a drop attaches dropped with
// its reason, a landing attaches landed at its attempt; a card that stores no decision
// attaches nothing (decide's TestAttachBriefsAttachesByTheExactOp: an op the record
// lacks is named, never matched to another decision).
func TestACardsEndAttachesToItsBrief(t *testing.T) {
	t.Parallel()
	ta, _, record := briefTestApp(t, "")
	dir := t.TempDir()
	writeNeedsBrief(t, dir, "a1", "Fix a1. converges=0.8", "")
	ta.ok("add --stream s1 --brief-dir " + dir)
	ta.ok("add --stream s1 --count 1 --one")
	out := ta.ok("drop a1 s1-1 --reason obsolete")
	assert.NotContains(t, out, "brief decision")
	assert.Equal(t, map[string]string{"a1": "dropped: obsolete"}, endsOf(t, record))

	r := newLandRig(t)
	env := r.a.getenv
	r.a.getenv = func(k string) string {
		if k == decide.JevSecret {
			return "k-test"
		}
		return env(k)
	}
	r.a.decideBackend = func(string) decide.Backend { return &briefBackend{} }
	r.a.briefBar = func(context.Context) (string, error) { return "", nil }
	r.a.briefRecord = func() (string, error) { return record, nil }
	r.ok("add --stream s2 s2-1 s2-2 --brief-file " + writeNeedsBrief(t, t.TempDir(), "x", "Do it. converges=0.6", ""))
	r.queued(map[string]string{"s2-1": r.head("s2-1", "main", "one.txt", "one\n"), "s2-2": r.head("s2-2", "main", "two.txt", "two\n")}, "s2-1", "s2-2")
	code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
	require.Equal(t, 0, code, out+errs)
	assert.NotContains(t, out, "brief decision")
	assert.Equal(t, map[string]string{"a1": "dropped: obsolete", "s2-1": "landed: landed at attempt 1", "s2-2": "landed: landed at attempt 1"}, endsOf(t, record))
}
