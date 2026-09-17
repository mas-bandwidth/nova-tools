package pulse

// #896: the sweep orders its enqueue by one typed score per PR. A fake decide
// provider stands in for TypeSafe Jev -- no network, no key on disk, never the
// real provider -- and records the state and question it was asked.

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
)

// fakeDecider is the fake decide provider: it records every (state, questions)
// it is asked and answers one scripted land score per PR, keyed off the pr= the
// state carries. It is the socket-free twin of the httptest fake below.
type fakeDecider struct {
	calls []fakeAsk
	reply map[int]decide.Answer
	err   error
}

type fakeAsk struct {
	state string
	qs    map[string]decide.Question
}

func (f *fakeDecider) Decide(ctx context.Context, state string, qs map[string]decide.Question) (map[string]decide.Answer, decide.Usage, error) {
	f.calls = append(f.calls, fakeAsk{state: state, qs: qs})
	if f.err != nil {
		return nil, decide.Usage{}, f.err
	}
	return map[string]decide.Answer{"land": f.reply[prFromState(state)]}, decide.Usage{InputTokens: 1, OutputTokens: 1}, nil
}

func prFromState(state string) int {
	for _, field := range strings.Fields(state) {
		if n, ok := strings.CutPrefix(field, "pr="); ok {
			pr, _ := strconv.Atoi(n)
			return pr
		}
	}
	return 0
}

// orderFixture builds three fresh green approvals and a source with the public
// features the question is built from: size, packages, red runs, age and a past
// group failure.
func orderFixture(t *testing.T) (string, *fakeSource, *fakeEnqueuer) {
	t.Helper()
	queue := sweepQueue(t,
		LedgerRow{PR: 7, Head: "h7", Card: "card-7.md", Verdict: "APPROVE", At: "2026-09-16T17:00:00Z"},
		LedgerRow{PR: 8, Head: "h8", Card: "card-8.md", Verdict: "APPROVE", At: "2026-09-16T17:00:00Z"},
		LedgerRow{PR: 9, Head: "h9", Card: "card-9.md", Verdict: "APPROVE", At: "2026-09-16T17:00:00Z"},
	)
	src := &fakeSource{calls: map[int]int{}, views: map[int][]PRView{
		7: {{State: "OPEN", Head: "h7", Checks: green(), ChangedFiles: 1, Additions: 120, Deletions: 60, Packages: []string{"cmd/nova-pulse"}, RedRuns: 2, AgeHours: 40, GroupFailed: true}},
		8: {{State: "OPEN", Head: "h8", Checks: green(), ChangedFiles: 3, Additions: 40, Deletions: 8, Packages: []string{"internal/pulse"}, RedRuns: 0, AgeHours: 5.5, GroupFailed: false}},
		9: {{State: "OPEN", Head: "h9", Checks: green(), ChangedFiles: 20, Additions: 900, Deletions: 400, Packages: []string{"internal/pulse", "cmd/nova-pulse", "internal/merge"}, RedRuns: 7, AgeHours: 200, GroupFailed: true}},
	}}
	return queue, src, &fakeEnqueuer{}
}

// #896 above the floor: the provider is asked the right typed question over the
// right bounded public features, the ORDER line carries score and confidence,
// and the enqueue runs in descending score -- the small green PR first, the
// poison candidate last.
func TestSweepOrdersByTypedScore(t *testing.T) {
	queue, src, enq := orderFixture(t)
	fake := &fakeDecider{reply: map[int]decide.Answer{
		7: {Type: "score", Score: 0.50, Confidence: 0.95}, // above floor, mid
		8: {Type: "score", Score: 0.95, Confidence: 0.95}, // above floor, first
		9: {Type: "score", Score: 0.05, Confidence: 0.95}, // above floor, last
	}}

	code, out, errs := sweepWithScorer(t, queue, src, enq, fake, 0.9, time.Date(2026, 9, 16, 18, 0, 0, 0, time.UTC))
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errs)
	}

	if len(enq.calls) != 3 || enq.calls[0] != 8 || enq.calls[1] != 7 || enq.calls[2] != 9 {
		t.Fatalf("enqueue order = %v, want [8 7 9] (descending score, small green first)", enq.calls)
	}
	for _, want := range []string{
		"ORDER pr=8 score=0.95 conf=0.95 floor=0.90",
		"ORDER pr=7 score=0.50 conf=0.95 floor=0.90",
		"ORDER pr=9 score=0.05 conf=0.95 floor=0.90",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "enqueued=3") {
		t.Errorf("SWEEP line = %q, want enqueued=3", out)
	}

	// the question and the state: one typed score named land over the six
	// bounded features, sent for the PR the answer belongs to.
	var asked *fakeAsk
	for i := range fake.calls {
		if strings.Contains(fake.calls[i].state, "pr=8") {
			asked = &fake.calls[i]
		}
	}
	if asked == nil {
		t.Fatalf("provider was not asked about pr=8; calls=%d", len(fake.calls))
	}
	q, ok := asked.qs["land"]
	if !ok || q.Score == nil || len(q.Score) == 0 {
		t.Fatalf("provider was not asked a land score: %+v", asked.qs)
	}
	if q.Choice != nil || q.Noul {
		t.Errorf("land question is not a score: %+v", q)
	}
	for _, want := range []string{"changed_files=3", "additions=40", "deletions=8", "packages=internal/pulse", "red_head_runs_24h=0", "age_hours=5.5", "group_failed_before=false"} {
		if !strings.Contains(asked.state, want) {
			t.Errorf("state missing %q: %q", want, asked.state)
		}
	}
}

// #896 below the floor: the score is 0.5, the ORDER line says below=land, and
// the enqueue keeps exactly the existing order -- oldest PR first.
func TestSweepBelowFloorKeepsExistingOrder(t *testing.T) {
	queue, src, enq := orderFixture(t)
	fake := &fakeDecider{reply: map[int]decide.Answer{
		7: {Type: "score", Score: 0.90, Confidence: 0.10},
		8: {Type: "score", Score: 0.10, Confidence: 0.10},
		9: {Type: "score", Score: 0.99, Confidence: 0.10},
	}}

	code, out, errs := sweepWithScorer(t, queue, src, enq, fake, 0.9, time.Date(2026, 9, 16, 18, 0, 0, 0, time.UTC))
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errs)
	}
	if len(enq.calls) != 3 || enq.calls[0] != 7 || enq.calls[1] != 8 || enq.calls[2] != 9 {
		t.Fatalf("enqueue order = %v, want [7 8 9] (below the floor the existing order stands)", enq.calls)
	}
	for _, want := range []string{
		"ORDER pr=7 score=0.50 conf=0.10 floor=0.90 below=land",
		"ORDER pr=8 score=0.50 conf=0.10 floor=0.90 below=land",
		"ORDER pr=9 score=0.50 conf=0.10 floor=0.90 below=land",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q:\n%s", want, out)
		}
	}
}

// #896 one tie above the floor keeps the existing order among the tied scores
// (oldest first), and the whole run is still one SWEEP line plus the ORDER lines.
func TestSweepTieKeepsExistingOrder(t *testing.T) {
	queue, src, enq := orderFixture(t)
	fake := &fakeDecider{reply: map[int]decide.Answer{
		7: {Type: "score", Score: 0.80, Confidence: 0.95},
		8: {Type: "score", Score: 0.80, Confidence: 0.95},
		9: {Type: "score", Score: 0.80, Confidence: 0.95},
	}}
	code, out, errs := sweepWithScorer(t, queue, src, enq, fake, 0.9, time.Date(2026, 9, 16, 18, 0, 0, 0, time.UTC))
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errs)
	}
	if len(enq.calls) != 3 || enq.calls[0] != 7 || enq.calls[1] != 8 || enq.calls[2] != 9 {
		t.Fatalf("tied enqueue order = %v, want [7 8 9]", enq.calls)
	}
	if !strings.Contains(out, "SWEEP ") || strings.Count(out, "ORDER ") != 3 {
		t.Errorf("expected one SWEEP line and three ORDER lines:\n%s", out)
	}
}

// sweepWithScorer is runSweep with the ordering decision wired in.
func sweepWithScorer(t *testing.T, queue string, src PRSource, enq Enqueuer, ask Decider, floor float64, at time.Time) (int, string, string) {
	t.Helper()
	var out, errs strings.Builder
	code := Sweep(SweepInput{
		Repo: "mas-bandwidth/nova-tools", Queue: queue, Source: src, Enqueuer: enq,
		Scorer: DecideScorer{Ask: ask}, Floor: floor,
		Now: func() time.Time { return at }, Stdout: &out, Stderr: &errs,
	})
	return code, out.String(), errs.String()
}

// TestSweepAsksTheJevProviderOverHTTP drives the real decide client against an
// httptest fake speaking the documented Jev shape. It skips where the sandbox
// forbids a listening socket; the socket-free fake above pins the same contract.
func TestSweepAsksTheJevProviderOverHTTP(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("listening sockets forbidden here (%v); TestSweepOrdersByTypedScore pins the contract", err)
	}
	ln.Close()

	queue, src, enq := orderFixture(t)
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		var body struct {
			State string `json:"state"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		score := map[int]float64{7: 0.50, 8: 0.95, 9: 0.05}[prFromState(body.State)]
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"answers":{"land":{"type":"score","score":%v,"confidence":0.95}},"usage":{"input_tokens":1,"output_tokens":1}}`, score)
	}))
	defer srv.Close()

	t.Setenv("CARD8337_JEV_KEY", "sekret")
	client, err := decide.New(srv.URL, "CARD8337_JEV_KEY")
	if err != nil {
		t.Fatalf("decide.New: %v", err)
	}
	var out, errs strings.Builder
	code := Sweep(SweepInput{
		Repo: "mas-bandwidth/nova-tools", Queue: queue, Source: src, Enqueuer: enq,
		Scorer: DecideScorer{Ask: client}, Floor: 0.9,
		Now: func() time.Time { return time.Date(2026, 9, 16, 18, 0, 0, 0, time.UTC) }, Stdout: &out, Stderr: &errs,
	})
	if code != 0 {
		t.Fatalf("exit = %d, stderr=%s", code, errs.String())
	}
	if gotAuth != "Bearer sekret" {
		t.Fatalf("provider auth header = %q, want the key the environment gave", gotAuth)
	}
	if len(enq.calls) != 3 || enq.calls[0] != 8 || enq.calls[1] != 7 || enq.calls[2] != 9 {
		t.Fatalf("enqueue order over HTTP = %v, want [8 7 9]", enq.calls)
	}
}
