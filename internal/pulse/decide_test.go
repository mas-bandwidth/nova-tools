package pulse

// Red tests for card 8371 (nova-tools #896): nova-pulse run makes the triage --decide
// pass after each harvest. A finished task whose needs_human is at or above the floor is
// appended to <queue>/HUMAN as one line and is NOT auto-retried; a provider_error above
// the floor is requeued once by the existing requeue path; below the floor nothing
// changes.
//
// No test reaches the network. The socket-free tests drive the decide seam directly and
// the httptest test (skipped where the sandbox forbids listening sockets) drives the same
// path through the real Jev client.

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/decide"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

var decideTestTime = time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)

// decideFixture builds a root with a swarm pool holding one finished task, and a queue
// directory for HUMAN.
func decideFixture(t *testing.T) (root, queue string, p *swarm.Pool, id, label string) {
	t.Helper()
	root = t.TempDir()
	queue = filepath.Join(t.TempDir(), "queue")
	if err := os.MkdirAll(queue, 0o755); err != nil {
		t.Fatal(err)
	}
	poolDir := filepath.Join(root, "pool")
	if err := os.MkdirAll(poolDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var err error
	p, err = swarm.OpenPool(poolDir)
	if err != nil {
		t.Fatal(err)
	}
	id, label = "20260917T000000Z-card-8371-abc123", "card-8371"
	sc := swarm.Sidecar{ID: id, Label: label, End: swarm.EndDone, RC: 1, Class: swarm.ClassPlanOnly}
	if err := os.WriteFile(p.Path(swarm.Done, id+".task"), []byte("do the thing\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(sc)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.Path(swarm.Done, id+".json"), append(raw, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, queue, p, id, label
}

// decideAnswers is the socket-free seam: one typed decision with the given reason,
// confidence and needs_human.
func decideAnswers(reason string, conf, human float64) swarm.DecideFunc {
	return func(ctx context.Context, state string, qs map[string]decide.Question) (map[string]decide.Answer, decide.Usage, error) {
		if _, ok := qs["reason"]; !ok {
			panic("the decide pass must ask the reason choice")
		}
		if _, ok := qs["needs_human"]; !ok {
			panic("the decide pass must ask the needs_human noul")
		}
		return map[string]decide.Answer{
			"reason":      {Type: "choice", Choice: reason, Probabilities: map[string]float64{reason: conf}, Confidence: conf},
			"needs_human": {Type: "noul", Noul: human, Confidence: human},
		}, decide.Usage{}, nil
	}
}

// decideInput is one decide pass at the default floor against the fixture pool.
func decideInput(queue string, p *swarm.Pool, do swarm.DecideFunc) DecideInput {
	return DecideInput{Queue: queue, Pools: []string{p.Dir}, Floor: 0.9, Do: do, Now: func() time.Time { return decideTestTime }}
}

func pendingTasks(t *testing.T, p *swarm.Pool) int {
	t.Helper()
	list, err := p.List(swarm.Pending)
	if err != nil {
		t.Fatal(err)
	}
	return len(list)
}

func humanLines(t *testing.T, queue string) []string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(queue, "HUMAN"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var out []string
	for _, l := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// A task whose needs_human is at or above the floor lands in HUMAN and is not requeued,
// even when its reason would otherwise be retried.
func TestRunDecideNeedsHumanGoesToHuman(t *testing.T) {
	_, queue, p, id, label := decideFixture(t)
	human, retried := DecideHarvest(decideInput(queue, p, decideAnswers("provider_error", 0.95, 0.95)))
	if human != 1 || retried != 0 {
		t.Fatalf("human=%d retried=%d, want 1 and 0 (needs_human wins over the retry)", human, retried)
	}
	lines := humanLines(t, queue)
	if len(lines) != 1 {
		t.Fatalf("HUMAN holds %d lines, want 1: %v", len(lines), lines)
	}
	want := "HUMAN task=" + id + " reason=provider_error conf=0.95 card=" + label
	if lines[0] != want {
		t.Fatalf("HUMAN line = %q, want %q", lines[0], want)
	}
	if n := pendingTasks(t, p); n != 0 {
		t.Fatalf("pending=%d, want 0: a needs_human task is not auto-retried", n)
	}
}

// A provider_error above the floor is requeued once by the existing requeue path, and
// never a second time.
func TestRunDecideProviderErrorRequeuesOnce(t *testing.T) {
	_, queue, p, _, _ := decideFixture(t)
	in := decideInput(queue, p, decideAnswers("provider_error", 0.95, 0.1))
	human, retried := DecideHarvest(in)
	if human != 0 || retried != 1 {
		t.Fatalf("human=%d retried=%d, want 0 and 1", human, retried)
	}
	if n := pendingTasks(t, p); n != 1 {
		t.Fatalf("pending=%d, want 1 (one requeue)", n)
	}
	if lines := humanLines(t, queue); len(lines) != 0 {
		t.Fatalf("a provider_error task must not land in HUMAN: %v", lines)
	}
	DecideHarvest(in)
	if n := pendingTasks(t, p); n != 1 {
		t.Fatalf("pending=%d after a second pass, want 1 (requeued once)", n)
	}
}

// Below the floor nothing changes: no HUMAN line and no requeue.
func TestRunDecideBelowFloorChangesNothing(t *testing.T) {
	_, queue, p, _, _ := decideFixture(t)
	human, retried := DecideHarvest(decideInput(queue, p, decideAnswers("provider_error", 0.6, 0.6)))
	if human != 0 || retried != 0 {
		t.Fatalf("human=%d retried=%d, want 0 and 0 below the floor", human, retried)
	}
	if n := pendingTasks(t, p); n != 0 {
		t.Fatalf("pending=%d, want 0 below the floor", n)
	}
	if lines := humanLines(t, queue); len(lines) != 0 {
		t.Fatalf("HUMAN must be empty below the floor: %v", lines)
	}
}

// The same contract through the real Jev client against an httptest fake, skipped where
// the sandbox forbids listening sockets.
func TestRunDecideAgainstHTTPFake(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("listening sockets forbidden here (%v); socket-free tests pin the contract", err)
	}
	ln.Close()

	_, queue, p, id, _ := decideFixture(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answers": {
			"reason": {"type":"choice","choice":"provider_error","probabilities":{"provider_error":0.95},"confidence":0.95},
			"needs_human": {"type":"noul","noul":0.95}
		}, "usage": {"input_tokens": 10, "output_tokens": 3}}`))
	}))
	defer srv.Close()

	t.Setenv("CARD8371_JEV_KEY", "sekret")
	client, err := decide.New(srv.URL, "CARD8371_JEV_KEY")
	if err != nil {
		t.Fatal(err)
	}
	human, retried := DecideHarvest(decideInput(queue, p, client.Decide))
	if human != 1 || retried != 0 {
		t.Fatalf("human=%d retried=%d, want 1 and 0", human, retried)
	}
	lines := humanLines(t, queue)
	if len(lines) != 1 || !strings.Contains(lines[0], "task="+id) {
		t.Fatalf("HUMAN = %v, want the task %s", lines, id)
	}
}
