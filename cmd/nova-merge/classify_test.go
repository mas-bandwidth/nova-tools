// Red tests for `nova-merge classify`, written before the verb. The decide provider is a
// fake over httptest; the run comes from the package's FakeHost. Nothing here reaches the
// network and no real key is read.
package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// classifyDecideServer runs a fake typed-decision provider over httptest, skipping where
// the sandbox forbids listening sockets.
func classifyDecideServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("listening sockets forbidden here (%v); the fake provider needs a loopback listener", err)
	}
	ln.Close()
	return httptest.NewServer(handler)
}

// classifyDecideHandler records the request and answers with the given raw answers JSON.
func classifyDecideHandler(t *testing.T, seen *map[string]any, answers string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		var raw map[string]json.RawMessage
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			t.Errorf("fake provider: bad request body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		var state, model string
		_ = json.Unmarshal(raw["state"], &state)
		_ = json.Unmarshal(raw["model"], &model)
		var questions map[string]json.RawMessage
		_ = json.Unmarshal(raw["questions"], &questions)
		qs := map[string]any{}
		for name, qr := range questions {
			var q any
			if err := json.Unmarshal(qr, &q); err != nil {
				t.Errorf("fake provider: bad question %q: %v", name, err)
				continue
			}
			qs[name] = q
		}
		*seen = map[string]any{"state": state, "model": model, "questions": qs}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answers":` + answers + `,"usage":{"input_tokens":1,"output_tokens":1}}`))
	}
}

// classifyLab is a lane whose fake host holds one failed merge-group run: one failed job
// on ubuntu-latest naming TestRefillCounts in internal/pulse, a package the PR did not
// change.
func classifyLab(t *testing.T) *lab {
	t.Helper()
	l := newLab(t)
	l.init("main")
	l.host.MergeGroupRuns[int64(42)] = merge.MergeRun{
		ID: 42, Event: "merge_group", PR: 77,
		Jobs: []merge.RunJob{{
			Name: "ci-ok", Runner: "ubuntu-latest", Package: "internal/pulse",
			Tests: []string{"TestRefillCounts"}, Changed: false,
		}},
	}
	return l
}

// Above the floor the typed choice drives the line: flaky-under-load re-runs.
func TestClassifyFlakyUnderLoadReruns(t *testing.T) {
	l := classifyLab(t)
	seen := map[string]any{}
	srv := classifyDecideServer(t, classifyDecideHandler(t, &seen,
		`{"kind":{"type":"choice","choice":"flaky-under-load","probabilities":{"flaky-under-load":0.95},"confidence":0.95}}`))
	defer srv.Close()
	t.Setenv("CARD896_JEV_KEY", "sekret")

	exit, out, errb := l.run("classify", "--lane", l.lane, "--run", "42", "--base-url", srv.URL, "--key-env", "CARD896_JEV_KEY")
	if exit != 0 {
		t.Fatalf("classify exit=%d stderr=%s", exit, errb)
	}
	want := "CLASSIFY run=42 pr=77 kind=flaky-under-load conf=0.95 floor=0.90 rerun=yes park=no"
	if !strings.Contains(out, want) {
		t.Fatalf("classify line missing %q:\n%s", want, out)
	}

	qs, _ := seen["questions"].(map[string]any)
	if len(qs) != 1 {
		t.Fatalf("expected exactly one question, got %v", qs)
	}
	q, _ := qs["kind"].(map[string]any)
	if q == nil || q["type"] != "choice" {
		t.Fatalf("kind question is not a choice: %v", q)
	}
	crit, _ := q["criteria"].(map[string]any)
	for _, opt := range []string{"flaky-under-load", "own-change", "environment"} {
		if _, ok := crit[opt]; !ok {
			t.Fatalf("kind criteria missing option %q: %v", opt, crit)
		}
	}
	instr, _ := q["instructions"].(string)
	for _, feat := range []string{"TestRefillCounts", "internal/pulse", "ubuntu-latest"} {
		if !strings.Contains(instr, feat) {
			t.Fatalf("kind question does not carry %q as a feature:\n%s", feat, instr)
		}
	}
}

// own-change above the floor parks.
func TestClassifyOwnChangeParks(t *testing.T) {
	l := classifyLab(t)
	seen := map[string]any{}
	srv := classifyDecideServer(t, classifyDecideHandler(t, &seen,
		`{"kind":{"type":"choice","choice":"own-change","probabilities":{"own-change":0.94},"confidence":0.94}}`))
	defer srv.Close()
	t.Setenv("CARD896_JEV_KEY", "sekret")

	exit, out, errb := l.run("classify", "--lane", l.lane, "--run", "42", "--base-url", srv.URL, "--key-env", "CARD896_JEV_KEY")
	if exit != 0 {
		t.Fatalf("classify exit=%d stderr=%s", exit, errb)
	}
	want := "CLASSIFY run=42 pr=77 kind=own-change conf=0.94 floor=0.90 rerun=no park=yes"
	if !strings.Contains(out, want) {
		t.Fatalf("classify line missing %q:\n%s", want, out)
	}
}

// Below the floor the answer is a suggestion: kind unknown, no action, and the raw answer
// named on the line. The caller decides as it did before the decision route existed.
func TestClassifyBelowFloorLeavesToday(t *testing.T) {
	l := classifyLab(t)
	seen := map[string]any{}
	srv := classifyDecideServer(t, classifyDecideHandler(t, &seen,
		`{"kind":{"type":"choice","choice":"own-change","probabilities":{"own-change":0.31},"confidence":0.31}}`))
	defer srv.Close()
	t.Setenv("CARD896_JEV_KEY", "sekret")

	exit, out, errb := l.run("classify", "--lane", l.lane, "--run", "42", "--base-url", srv.URL, "--key-env", "CARD896_JEV_KEY")
	if exit != 0 {
		t.Fatalf("classify exit=%d stderr=%s", exit, errb)
	}
	want := "CLASSIFY run=42 pr=77 kind=unknown conf=0.31 floor=0.90 rerun=no park=no below=own-change"
	if !strings.Contains(out, want) {
		t.Fatalf("below-floor classify line missing %q:\n%s", want, out)
	}
}
