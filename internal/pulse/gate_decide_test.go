package pulse

// Red tests for the gate's decide step (nova-tools #896). A failing ci job is classified
// by one typed decision behind a floor: flaky reruns the failed jobs once for the head,
// real keeps today's STOP, and anything below the floor changes nothing. The fake runs
// API and the httptest fake keep every test off the network.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
)

// fakeDecider answers the gate without a provider: a test picks the verdict and its
// confidence, and reads back the state the gate sent.
type fakeDecider struct {
	choice string
	conf   float64
	err    error
	state  string
	asked  int
}

func (f *fakeDecider) Decide(ctx context.Context, state string, qs map[string]decide.Question) (map[string]decide.Answer, decide.Usage, error) {
	f.asked++
	f.state = state
	if f.err != nil {
		return nil, decide.Usage{}, f.err
	}
	return map[string]decide.Answer{
		"verdict":             {Type: "choice", Choice: f.choice, Probabilities: map[string]float64{f.choice: f.conf}, Confidence: f.conf},
		"same_class_as_known": {Type: "noul", Noul: 0.9, Confidence: 0.9},
	}, decide.Usage{}, nil
}

func gateDecideRun(t *testing.T, queue string, src RunSource, dec Decider, floor float64) (string, string, int) {
	t.Helper()
	var out, errb strings.Builder
	code := Gate(GateInput{
		Repo: "mas-bandwidth/nova-tools", Branch: "main", Queue: queue, Source: src,
		Decide: true, Floor: floor, Decider: dec, Stdout: &out, Stderr: &errb,
	})
	return out.String(), errb.String(), code
}

func flakySrc(sha string) *fakeRuns {
	return &fakeRuns{
		runs: []CIRun{{ID: 35120376309, Status: "completed", Conclusion: "failure", HeadSHA: sha, Workflow: "ci", Event: "push"}},
		job:  CIJob{Name: "studio-fast", Log: "--- FAIL: TestFlaky\n    dial tcp: connection refused\n"},
	}
}

// gate-reruns-a-flaky-job-once-per-head: a flaky verdict at 0.95 at or above the floor
// reruns the failed jobs once, writes GATE RERUN and the RERUN-<sha> marker, and never
// reruns the same head twice -- the second run falls back to today's STOP.
func TestGateRerunsAFlakyJobOnceForTheHead(t *testing.T) {
	queue := t.TempDir()
	src := flakySrc("8f3714d1c0de0000")
	dec := &fakeDecider{choice: "flaky", conf: 0.95}

	out, errb, code := gateDecideRun(t, queue, src, dec, 0.9)
	if code != 0 {
		t.Fatalf("first gate exit = %d, want 0; out=%q err=%q", code, out, errb)
	}
	if len(src.reruns) != 1 {
		t.Fatalf("reruns = %d, want exactly 1", len(src.reruns))
	}
	lines := nonEmptyLines(out + errb)
	if len(lines) != 1 || lines[0] != "GATE RERUN sha=8f3714d1c0de job=studio-fast conf=0.95" {
		t.Fatalf("want one GATE RERUN line, got %q", lines)
	}
	if _, err := os.Stat(filepath.Join(queue, "RERUN-8f3714d1c0de")); err != nil {
		t.Fatalf("the RERUN marker was not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(queue, StopFile)); !os.IsNotExist(err) {
		t.Fatalf("a rerunning gate wrote a STOP (err=%v)", err)
	}

	out2, errb2, code2 := gateDecideRun(t, queue, src, dec, 0.9)
	if code2 != 1 {
		t.Fatalf("second gate exit = %d, want 1; out=%q err=%q", code2, out2, errb2)
	}
	if len(src.reruns) != 1 {
		t.Fatalf("the same head reran twice: %d", len(src.reruns))
	}
	if !strings.HasPrefix(nonEmptyLines(out2 + errb2)[0], "GATE RED ") {
		t.Fatalf("the second run did not fall back to STOP: %q", out2)
	}
	if _, err := os.Stat(filepath.Join(queue, StopFile)); err != nil {
		t.Fatalf("the second run wrote no STOP: %v", err)
	}
}

// gate-real-verdict-keeps-stop: a real verdict at 0.99 changes no behaviour: the STOP is
// written and the line carries decide=real conf=0.99.
func TestGateRealVerdictKeepsStop(t *testing.T) {
	queue := t.TempDir()
	src := flakySrc("8f3714d1c0de0000")
	dec := &fakeDecider{choice: "real", conf: 0.99}

	out, errb, code := gateDecideRun(t, queue, src, dec, 0.9)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; out=%q err=%q", code, out, errb)
	}
	if len(src.reruns) != 0 {
		t.Fatalf("a real verdict reran the job %d times", len(src.reruns))
	}
	if _, err := os.Stat(filepath.Join(queue, StopFile)); err != nil {
		t.Fatalf("a real verdict wrote no STOP: %v", err)
	}
	line := nonEmptyLines(out + errb)[0]
	if !strings.HasPrefix(line, "GATE RED ") || !strings.Contains(line, "decide=real ") || !strings.Contains(line, "conf=0.99") {
		t.Fatalf("GATE RED does not carry decide=real conf=0.99: %q", line)
	}
}

// gate-below-floor-changes-nothing: a flaky verdict at 0.6, under the 0.9 floor, is a
// suggestion and never an authorization: no rerun, decide=?, and today's STOP stands.
func TestGateBelowFloorChangesNothing(t *testing.T) {
	queue := t.TempDir()
	src := flakySrc("8f3714d1c0de0000")
	dec := &fakeDecider{choice: "flaky", conf: 0.6}

	out, errb, code := gateDecideRun(t, queue, src, dec, 0.9)
	if code != 1 {
		t.Fatalf("exit = %d, want 1; out=%q err=%q", code, out, errb)
	}
	if len(src.reruns) != 0 {
		t.Fatalf("a below-floor verdict reran the job %d times", len(src.reruns))
	}
	if _, err := os.Stat(filepath.Join(queue, "RERUN-8f3714d1c0de")); !os.IsNotExist(err) {
		t.Fatalf("a below-floor verdict wrote a RERUN marker (err=%v)", err)
	}
	if _, err := os.Stat(filepath.Join(queue, StopFile)); err != nil {
		t.Fatalf("a below-floor verdict wrote no STOP: %v", err)
	}
	line := nonEmptyLines(out + errb)[0]
	if !strings.Contains(line, "decide=?") {
		t.Fatalf("GATE RED does not read decide=? below the floor: %q", line)
	}
}

// gate-decide-sends-the-tail-and-known-flakes: the state carries the last 60 log lines,
// each escaped line by line as triage does, and the open known-flaky patterns of
// <queue>/FLAKY.txt.
func TestGateDecideStateCarriesTheTailAndKnownFlakes(t *testing.T) {
	queue := t.TempDir()
	if err := os.WriteFile(filepath.Join(queue, "FLAKY.txt"), []byte("rate limit exceeded\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	var log strings.Builder
	for i := 0; i < 70; i++ {
		fmt.Fprintf(&log, "line-%02d\n", i)
	}
	log.WriteString("secret\twith\ttabs\n")
	dec := &fakeDecider{choice: "unknown", conf: 0.99}
	src := &fakeRuns{
		runs: []CIRun{{ID: 7, Status: "completed", Conclusion: "failure", HeadSHA: "abcdefabcdef0123", Workflow: "ci", Event: "push"}},
		job:  CIJob{Name: "dev", Log: log.String()},
	}
	if _, _, code := gateDecideRun(t, queue, src, dec, 0.9); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if strings.Contains(dec.state, "line-00") {
		t.Fatalf("the state carries a line older than the last 60:\n%s", dec.state)
	}
	if !strings.Contains(dec.state, "line-69") {
		t.Fatalf("the state does not carry the newest line:\n%s", dec.state)
	}
	if !strings.Contains(dec.state, "rate limit exceeded") {
		t.Fatalf("the state does not carry the known-flaky patterns:\n%s", dec.state)
	}
	if !strings.Contains(dec.state, `secret\x09with\x09tabs`) {
		t.Fatalf("the tail is not escaped line by line as triage does:\n%s", dec.state)
	}
}

// gate-decides-over-http: the shipped decide.Client, pointed at an httptest provider,
// drives the same rerun path end to end -- request shape, typed answer, rerun.
func TestGateDecidesOverHTTP(t *testing.T) {
	queue := t.TempDir()
	if err := os.WriteFile(filepath.Join(queue, "FLAKY.txt"), []byte("dial tcp: connection refused\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	states := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer testkey" {
			t.Errorf("authorization = %q, want Bearer testkey", got)
		}
		raw, _ := io.ReadAll(r.Body)
		var body struct {
			State string `json:"state"`
		}
		_ = json.Unmarshal(raw, &body)
		states <- body.State
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"answers":{"verdict":{"type":"choice","choice":"flaky","confidence":0.95},"same_class_as_known":{"type":"noul","noul":0.9}},"usage":{"input_tokens":5,"output_tokens":6}}`)
	}))
	defer srv.Close()

	t.Setenv("CARD8334_JEV_KEY", "testkey")
	client, err := decide.New(srv.URL, "CARD8334_JEV_KEY")
	if err != nil {
		t.Fatalf("decide.New: %v", err)
	}
	src := flakySrc("8f3714d1c0de0000")
	out, errb, code := gateDecideRun(t, queue, src, client, 0.9)
	if code != 0 {
		t.Fatalf("exit = %d, want 0; out=%q err=%q", code, out, errb)
	}
	if len(src.reruns) != 1 {
		t.Fatalf("reruns = %d, want 1", len(src.reruns))
	}
	if !strings.Contains(out, "GATE RERUN ") {
		t.Fatalf("out = %q, want a GATE RERUN line", out)
	}
	state := <-states
	if !strings.Contains(state, "dial tcp: connection refused") {
		t.Fatalf("the state does not carry the known-flaky pattern:\n%s", state)
	}
	if !strings.Contains(state, "--- FAIL: TestFlaky") {
		t.Fatalf("the state does not carry the failing tail:\n%s", state)
	}
}
