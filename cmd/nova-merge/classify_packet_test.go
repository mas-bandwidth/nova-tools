package main

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/merge"
)

// mergePacketDecideServer runs a fake typed-decision provider over httptest, skipping
// where the sandbox forbids listening sockets. The fake speaks the Jev body-and-response
// shape of rule 2 and never dials the real provider (rule 9).
func mergePacketDecideServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("listening sockets forbidden here (%v); the fake provider needs a loopback listener", err)
	}
	ln.Close()
	return httptest.NewServer(handler)
}

// mergePacketDecideHandler records the request, asserts the header carries the key the
// environment gave, and answers with the given raw answers JSON.
func mergePacketDecideHandler(t *testing.T, seen *map[string]any, answers string) http.HandlerFunc {
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
		*seen = map[string]any{
			"state":     state,
			"model":     model,
			"questions": qs,
			"auth":      r.Header.Get("Authorization"),
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answers":` + answers + `,"usage":{"input_tokens":1,"output_tokens":1}}`))
	}
}

// a-merge-classification-never-merges: a merge classification labels the packet with the
// classifier's typed risk and scope, and the run merges nothing, pushes nothing, approves
// nothing and clears no branch protection. The decision only annotates the packet.
func TestAMergeClassificationNeverMerges(t *testing.T) {
	l := newLab(t)
	setupPR(t, l, 951, "feature-a", "a.txt", true)

	seen := map[string]any{}
	srv := mergePacketDecideServer(t, mergePacketDecideHandler(t, &seen,
		`{"risk":{"type":"score","score":2,"confidence":0.93},"scope":{"type":"noul","noul":0.8,"confidence":0.8}}`))
	defer srv.Close()
	t.Setenv("CARD9331_JEV_KEY", "sekret")

	beforePushes := len(l.pushes())
	beforeState, err := os.ReadFile(merge.StatePath(l.lane))
	if err != nil {
		t.Fatal(err)
	}

	exit, stdout, stderr := l.run("packet", "--lane", l.lane, "--who", "emma", "--all",
		"--decide", "--base-url", srv.URL, "--key-env", "CARD9331_JEV_KEY", "--floor", "0.9")
	if exit != 0 {
		t.Fatalf("packet --decide exit=%d stderr=%s stdout=%s", exit, stderr, stdout)
	}

	// The classifier's answer lands on the packet: one typed risk and scope, with the
	// evidence pointer (rule 10) and the floor (rule 5).
	contains(t, stdout, "PACKET DECIDE entry=951")
	contains(t, stdout, "risk=2.00")
	contains(t, stdout, "scope=0.80")
	contains(t, stdout, "floor=0.90")
	contains(t, stdout, "evidence=")

	// The provider was asked the typed questions and given the sealed key (rules 2 and 3).
	questions, _ := seen["questions"].(map[string]any)
	if _, ok := questions["risk"]; !ok {
		t.Fatalf("provider was not asked the risk question: %v", seen)
	}
	if _, ok := questions["scope"]; !ok {
		t.Fatalf("provider was not asked the scope question: %v", seen)
	}
	if model, _ := seen["model"].(string); model != "jev-latest" {
		t.Fatalf("provider model = %q, want jev-latest", model)
	}
	if auth, _ := seen["auth"].(string); auth != "Bearer sekret" {
		t.Fatalf("provider Authorization = %q, want the environment's key", auth)
	}

	// Rule 7: the classification never merges, pushes, approves or clears protection.
	if len(l.host.Merges) != 0 {
		t.Errorf("a merge classification merged: %v", l.host.Merges)
	}
	if len(l.host.Readied) != 0 {
		t.Errorf("a merge classification approved a review: %v", l.host.Readied)
	}
	if after := len(l.pushes()); after != beforePushes {
		t.Errorf("a merge classification pushed: before=%d after=%d", beforePushes, after)
	}
	afterState, err := os.ReadFile(merge.StatePath(l.lane))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeState, afterState) {
		t.Error("a merge classification changed the lane state")
	}
	if strings.Contains(stdout, "MERGED") || strings.Contains(stdout, "RUN MERGE") {
		t.Errorf("a merge classification printed a merge:\n%s", stdout)
	}
}
