package main

// a-decide-flag-below-its-floor-runs-the-pre-flag-branch: --decide only advises. The
// fixture answers under the floor, so the verb must cut exactly the packet it cut before
// the flag existed, print the evidence pointer the judgment came from on its own line,
// and write nothing by way of a decision -- no rule row, no second card, no push.
// (SPEC-DECIDE rules 5, 7 and 10.)

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// decideListenable skips where the sandbox forbids a loopback listener; the httptest fake
// of rule 9 needs one and never dials the real network.
func decideListenable(t *testing.T) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("listening sockets forbidden here (%v); the fake needs a loopback listener", err)
	}
	ln.Close()
}

func TestADecideFlagBelowItsFloorRunsThePreFlagBranch(t *testing.T) {
	dir := t.TempDir()
	queue := filepath.Join(dir, "queue")
	if err := os.MkdirAll(queue, 0o755); err != nil {
		t.Fatal(err)
	}
	evidence := filepath.Join(dir, "nosha.txt")
	if err := os.WriteFile(evidence, []byte(
		"RESULT card-892 sha=0123456789ab the handoff line carries no sha\n"+
			"REFUSED nosha: the handoff line carries no sha to pin\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The pre-flag branch, recorded first: the packet cut with no --decide at all.
	baseOut := filepath.Join(dir, "base.md")
	var bout, berr bytes.Buffer
	if code := run([]string{"triage", "--case", "nosha", "--queue", queue, "--out", baseOut,
		"--ref", "card-892", "--evidence", evidence}, &bout, &berr, time.Now().UTC()); code != 0 {
		t.Fatalf("baseline triage exit=%d err=%s", code, berr.String())
	}
	baseCard, err := os.ReadFile(baseOut)
	if err != nil {
		t.Fatal(err)
	}

	decideListenable(t)
	asked := ""
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/systemone" {
			t.Errorf("provider path = %q, want /v1/systemone", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer testkey" {
			t.Errorf("authorization = %q, want Bearer testkey", got)
		}
		var body struct {
			State string `json:"state"`
			Model string `json:"model"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		asked = body.State
		if body.Model != "jev-latest" {
			t.Errorf("provider model = %q, want jev-latest", body.Model)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"answers":{"verdict":{"type":"choice","choice":"ADMIT","confidence":0.6}},"usage":{"input_tokens":3,"output_tokens":4}}`)
	}))
	defer srv.Close()
	t.Setenv("CARD9328_JEV_KEY", "testkey")

	out := filepath.Join(dir, "triage-nosha.md")
	var o, e bytes.Buffer
	code := run([]string{"triage", "--case", "nosha", "--queue", queue, "--out", out,
		"--ref", "card-892", "--evidence", evidence,
		"--decide", "--floor", "0.9", "--base-url", srv.URL + "/v1/systemone",
		"--key-env", "CARD9328_JEV_KEY"}, &o, &e, time.Now().UTC())
	if code != 0 {
		t.Fatalf("--decide triage exit=%d want 0; out=%q err=%q", code, o.String(), e.String())
	}

	// The pre-flag branch ran unchanged: the same packet bytes as the no-flag run.
	gotCard, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("the card was not written: %v", err)
	}
	if string(gotCard) != string(baseCard) {
		t.Fatalf("--decide changed the packet the verb cuts:\n got %q\nwant %q", gotCard, baseCard)
	}

	lines := strings.Split(strings.TrimRight(o.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want the pre-flag line and one suggestion line, got %q", o.String())
	}
	if !strings.HasPrefix(lines[0], "TRIAGE OK case=nosha") {
		t.Fatalf("pre-flag line = %q", lines[0])
	}
	sug := lines[1]
	for _, must := range []string{"TRIAGE DECIDE", "verdict=?", "conf=0.60", "floor=0.90", "evidence=" + evidence} {
		if !strings.Contains(sug, must) {
			t.Fatalf("suggestion line %q does not carry %q", sug, must)
		}
	}
	// Rule 7: the decision wrote nothing -- no rule row, no second card, no push.
	if _, err := os.Stat(filepath.Join(queue, "RULES.tsv")); !os.IsNotExist(err) {
		t.Fatalf("the decision wrote a RULES.tsv row (err=%v)", err)
	}
	entries, _ := os.ReadDir(dir)
	for _, en := range entries {
		switch en.Name() {
		case "queue", "nosha.txt", "base.md", "triage-nosha.md":
		default:
			t.Fatalf("the decision wrote an unexpected file %q", en.Name())
		}
	}
	// Rule 4: the state sent is the bounded public packet, never more.
	if !strings.Contains(asked, "nosha") || !strings.Contains(asked, "card-892") {
		t.Fatalf("state does not carry the case and ref:\n%s", asked)
	}
}

// Above the floor the typed answer is printed as a suggestion: the verdict is named, the
// verb still cuts its packet, and it still acts on nothing.
func TestTriageDecideAboveFloorPrintsTheTypedSuggestion(t *testing.T) {
	dir := t.TempDir()
	queue := filepath.Join(dir, "queue")
	if err := os.MkdirAll(queue, 0o755); err != nil {
		t.Fatal(err)
	}
	evidence := filepath.Join(dir, "nosha.txt")
	if err := os.WriteFile(evidence, []byte("RESULT card-892 sha=0123456789ab the handoff line carries no sha\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	decideListenable(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"answers":{"verdict":{"type":"choice","choice":"ADMIT","confidence":0.95}},"usage":{"input_tokens":3,"output_tokens":4}}`)
	}))
	defer srv.Close()
	t.Setenv("CARD9328_JEV_KEY", "testkey")

	out := filepath.Join(dir, "triage-nosha.md")
	var o, e bytes.Buffer
	code := run([]string{"triage", "--case", "nosha", "--queue", queue, "--out", out,
		"--ref", "card-892", "--evidence", evidence,
		"--decide", "--floor", "0.9", "--base-url", srv.URL + "/v1/systemone",
		"--key-env", "CARD9328_JEV_KEY"}, &o, &e, time.Now().UTC())
	if code != 0 {
		t.Fatalf("--decide triage exit=%d want 0; out=%q err=%q", code, o.String(), e.String())
	}
	sug := strings.Split(strings.TrimRight(o.String(), "\n"), "\n")[1]
	for _, must := range []string{"TRIAGE DECIDE", "verdict=ADMIT", "conf=0.95", "floor=0.90", "evidence=" + evidence} {
		if !strings.Contains(sug, must) {
			t.Fatalf("suggestion line %q does not carry %q", sug, must)
		}
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatalf("the pre-flag branch did not write the card: %v", err)
	}
}
