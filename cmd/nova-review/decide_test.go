package main

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// decideServer runs a fake typed-decision provider over httptest, skipping where the
// sandbox forbids listening sockets. The fake speaks the Jev body-and-response shape
// of internal/decide and never dials the network.
func decideServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("listening sockets forbidden here (%v); the fake needs a loopback listener", err)
	}
	ln.Close()
	return httptest.NewServer(handler)
}

// publicPacketLab is packetLab plus the .public marker that lets --decide send the
// packet's state to the provider.
func publicPacketLab(t *testing.T) (lane, head string) {
	t.Helper()
	lane, head = packetLab(t)
	if err := os.WriteFile(filepath.Join(lane, ".public"), []byte("public\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return lane, head
}

// decideHandler decodes the request, records what it was asked, and answers with the
// given raw answers JSON.
func decideHandler(t *testing.T, seen *map[string]any, answers string) http.HandlerFunc {
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

// A packet built with --decide carries one advisory REVIEW DECIDE line and still
// carries every section it carried before; the decision changes no verdict.
func TestPacketDecideAppendsRiskAndSecondReaderLine(t *testing.T) {
	lane, _ := publicPacketLab(t)
	seen := map[string]any{}
	srv := decideServer(t, decideHandler(t, &seen, `{"risk":{"type":"score","score":2,"confidence":0.93},"needs_second_reader":{"type":"noul","noul":0.9,"confidence":0.9}}`))
	defer srv.Close()

	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(lane); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CARD8375_JEV_KEY", "sekret")
	var out, errb bytes.Buffer
	code := run([]string{"packet", "--lane", lane, "--branch", "feature", "--who", "emma", "--out", "packet.md", "--decide", "--base-url", srv.URL, "--key-env", "CARD8375_JEV_KEY"}, &out, &errb)
	if code != 0 {
		t.Fatalf("packet --decide exit=%d stderr=%s", code, errb.String())
	}
	body, err := os.ReadFile("packet.md")
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	want := "REVIEW DECIDE risk=2.00 scope=- second_reader=0.90 conf=0.90 evidence=branch#feature"
	if !strings.Contains(got, want) {
		t.Fatalf("packet missing %q:\n%s", want, got)
	}
	for _, section := range []string{"## This head", "## Your prior verdicts on this entry", "## All verdicts at earlier heads", "## Open findings", "## Rules touched", "## Diff ", "## Not included"} {
		if !strings.Contains(got, section) {
			t.Errorf("decision changed the packet: missing section %q", section)
		}
	}
	hdr, err := readPacketFirstLine("packet.md")
	if err != nil {
		t.Fatal(err)
	}
	if hdr.Bytes != len(body) {
		t.Fatalf("header claims bytes=%d, file is %d", hdr.Bytes, len(body))
	}
	questions, _ := seen["questions"].(map[string]any)
	if _, ok := questions["risk"]; !ok {
		t.Fatalf("provider was not asked the risk question: %v", seen)
	}
	if _, ok := questions["needs_second_reader"]; !ok {
		t.Fatalf("provider was not asked needs_second_reader: %v", seen)
	}
	if model, _ := seen["model"].(string); model != "jev-latest" {
		t.Fatalf("provider model = %q, want jev-latest", model)
	}
	state, _ := seen["state"].(string)
	if !strings.Contains(state, "change") {
		t.Errorf("state does not carry the PR title: %q", state)
	}
	if !strings.Contains(state, "+changed") {
		t.Errorf("state does not carry the diff: %q", state)
	}
}

// With --card the packet asks whether the diff does what the card's RESULT line
// promises and nothing else, and the state carries the card.
func TestPacketDecideWithCardAsksScope(t *testing.T) {
	lane, _ := publicPacketLab(t)
	seen := map[string]any{}
	srv := decideServer(t, decideHandler(t, &seen, `{"risk":{"type":"score","score":1,"confidence":0.91},"scope_matches_card":{"type":"noul","noul":0.8,"confidence":0.8},"needs_second_reader":{"type":"noul","noul":0.7,"confidence":0.7}}`))
	defer srv.Close()

	card := filepath.Join(t.TempDir(), "card.md")
	if err := os.WriteFile(card, []byte("RESULT: nova-review packet --decide adds a line\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(lane); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CARD8375_JEV_KEY", "sekret")
	var out, errb bytes.Buffer
	code := run([]string{"packet", "--lane", lane, "--branch", "feature", "--who", "emma", "--out", "packet.md", "--decide", "--card", card, "--base-url", srv.URL, "--key-env", "CARD8375_JEV_KEY"}, &out, &errb)
	if code != 0 {
		t.Fatalf("packet --decide --card exit=%d stderr=%s", code, errb.String())
	}
	body, _ := os.ReadFile("packet.md")
	if !strings.Contains(string(body), "REVIEW DECIDE risk=1.00 scope=0.80 second_reader=0.70 conf=0.70 evidence=branch#feature") {
		t.Fatalf("packet --card line wrong:\n%s", body)
	}
	state, _ := seen["state"].(string)
	if !strings.Contains(state, "RESULT: nova-review packet --decide adds a line") {
		t.Fatalf("state does not carry the card: %q", state)
	}
	questions, _ := seen["questions"].(map[string]any)
	if _, ok := questions["scope_matches_card"]; !ok {
		t.Fatalf("provider was not asked scope_matches_card: %v", seen)
	}
}

// A private repo is refused by name before any provider call.
func TestPacketDecideRefusesNonPublicRepo(t *testing.T) {
	lane, _ := packetLab(t) // repo "test/repo", no public list entry and no .public marker
	called := 0
	srv := decideServer(t, func(w http.ResponseWriter, r *http.Request) {
		called++
		w.WriteHeader(http.StatusInternalServerError)
	})
	defer srv.Close()

	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(lane); err != nil {
		t.Fatal(err)
	}
	var out, errb bytes.Buffer
	code := run([]string{"packet", "--lane", lane, "--branch", "feature", "--who", "emma", "--out", "packet.md", "--decide", "--base-url", srv.URL}, &out, &errb)
	if code != 2 {
		t.Fatalf("private repo --decide exit=%d, want 2 (stderr=%s)", code, errb.String())
	}
	if !strings.Contains(errb.String(), "test/repo") {
		t.Fatalf("refusal does not name the repo: %s", errb.String())
	}
	if called != 0 {
		t.Fatalf("private state was sent to the provider %d times", called)
	}
	if _, err := os.Stat("packet.md"); err == nil {
		t.Fatal("refused --decide still wrote a packet")
	}
}

// A decision below the floor is a suggestion, never clearance: the line is still
// appended and the packet is still written.
func TestPacketDecideBelowFloorIsOnlyAdvice(t *testing.T) {
	lane, _ := publicPacketLab(t)
	seen := map[string]any{}
	srv := decideServer(t, decideHandler(t, &seen, `{"risk":{"type":"score","score":3,"confidence":0.2},"needs_second_reader":{"type":"noul","noul":0.3,"confidence":0.3}}`))
	defer srv.Close()

	old, _ := os.Getwd()
	defer os.Chdir(old)
	if err := os.Chdir(lane); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CARD8375_JEV_KEY", "sekret")
	var out, errb bytes.Buffer
	code := run([]string{"packet", "--lane", lane, "--branch", "feature", "--who", "emma", "--out", "packet.md", "--decide", "--floor", "0.9", "--base-url", srv.URL, "--key-env", "CARD8375_JEV_KEY"}, &out, &errb)
	if code != 0 {
		t.Fatalf("below-floor --decide exit=%d stderr=%s", code, errb.String())
	}
	body, _ := os.ReadFile("packet.md")
	if !strings.Contains(string(body), "REVIEW DECIDE risk=3.00 scope=- second_reader=0.30 conf=0.20") {
		t.Fatalf("below-floor line wrong:\n%s", body)
	}
}
