package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// fakeJev is the provider the --decide tests talk to. It is an httptest server and
// nothing else: no network, no real key, and a call counter the empty-inbox test
// reads to prove no decision was asked for.
type fakeJev struct {
	calls  atomic.Int64
	mu     sync.Mutex
	states []string
	auth   []string
}

func (f *fakeJev) record(r *http.Request) string {
	f.calls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.auth = append(f.auth, r.Header.Get("Authorization"))
	raw, _ := io.ReadAll(r.Body)
	var body struct {
		State string `json:"state"`
	}
	_ = json.Unmarshal(raw, &body)
	f.states = append(f.states, body.State)
	return body.State
}

func (f *fakeJev) sawState(substr string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.states {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}

const decideTestKeyEnv = "CARD8373_JEV_KEY"

// startFakeJev starts the handler and returns it with its URL. The handler answers
// by the state it was handed, so each note gets a distinct typed decision.
func startFakeJev(t *testing.T) (*fakeJev, string) {
	t.Helper()
	f := &fakeJev{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state := f.record(r)
		kind, conf := "done", 0.99
		needs, blocked := 0.10, 0.05
		switch {
		case strings.Contains(state, "gate"):
			kind, conf, needs, blocked = "question", 0.95, 0.90, 0.10
		case strings.Contains(state, "finding"):
			kind, conf, needs, blocked = "refusal", 0.50, 0.20, 0.80
		case strings.Contains(state, "start the batch"):
			kind, conf, needs, blocked = "start", 0.99, 0.70, 0.00
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"answers": {` +
			`"kind": {"type":"choice","choice":"` + kind + `","probabilities":{"` + kind + `":` + ftoa(conf) + `},"confidence":` + ftoa(conf) + `},` +
			`"needs_reply": {"type":"noul","noul":` + ftoa(needs) + `},` +
			`"blocked": {"type":"noul","noul":` + ftoa(blocked) + `}` +
			`}, "usage": {"input_tokens": 5, "output_tokens": 1}}`))
	}))
	t.Cleanup(srv.Close)
	return f, srv.URL
}

func ftoa(v float64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// publicBus makes the fixture's clone a public one: the marker --decide requires.
func publicBus(t *testing.T, checkout string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(checkout, ".public"), []byte("public\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// addDecideNote lands one more incoming note from Bo on the fixture.
func addDecideNote(t *testing.T, checkout, path, id, subject, body string) {
	t.Helper()
	writeFile(t, checkout, path, "From: Bo\nTo: Ada\nDate: Mon Sep  7 00:03:00 UTC 2026\nId: "+id+"\nSubject: "+subject+"\n\n"+body)
	gitIn(t, checkout, "add", "--", path)
	gitIn(t, checkout, "-c", "user.name=Bo", "-c", "user.email=bo@example.com", "commit", "-q", "-m", "decide "+id)
	gitIn(t, checkout, "push", "-q", "origin", "main")
}

func decideArgs(checkout, baseURL string, extra ...string) []string {
	args := []string{"inbox", "--bus", checkout, "--as", "Ada", "--receipt-max-words", "40",
		"--full", "--decide", "--floor", "0.9", "--base-url", baseURL, "--key-env", decideTestKeyEnv}
	return append(args, extra...)
}

// Three notes get three annotated INBOX NOTE lines and one INBOX DECIDED line.
func TestInboxDecideAnnotatesNotesAndSummarizes(t *testing.T) {
	checkout, _ := busDir(t)
	publicBus(t, checkout)
	addDecideNote(t, checkout, "from-bo/finding.md", "bo-222222222222", "A finding worth acting on", "This is a finding, and it is long enough that nobody would mistake its shape.")
	addDecideNote(t, checkout, "from-bo/start.md", "bo-333333333333", "Please start the batch", "Go ahead and start the batch now.")
	f, url := startFakeJev(t)
	t.Setenv(decideTestKeyEnv, "sk-test-key")
	t.Setenv("TYPESAFE_API_KEY", "")

	r := invoke(t, "", decideArgs(checkout, url)...).mustCode(t, 0)

	if got := strings.Count(r.stdout, "INBOX NOTE id="); got != 3 {
		t.Fatalf("want 3 INBOX NOTE lines, got %d:\n%s", got, r.stdout)
	}
	for _, want := range []string{
		"kind=question needs_reply=0.90 blocked=0.10 conf=0.95",
		"kind=refusal needs_reply=0.20 blocked=0.80 conf=0.50",
		"kind=start needs_reply=0.70 blocked=0.00 conf=0.99",
	} {
		if !strings.Contains(r.stdout, want) {
			t.Fatalf("listing missing decision %q:\n%s", want, r.stdout)
		}
	}
	if !strings.Contains(r.stdout, "INBOX DECIDED n=3 needs_reply=2 below_floor=1") {
		t.Fatalf("missing or wrong DECIDED summary:\n%s", r.stdout)
	}
	if f.calls.Load() != 3 {
		t.Fatalf("want 3 provider calls, got %d", f.calls.Load())
	}
}

// A body past its first 600 characters is not sent, and an sk- key in the sent
// prefix is redacted before it leaves.
func TestInboxDecideBoundsAndRedactsState(t *testing.T) {
	checkout, _ := busDir(t)
	publicBus(t, checkout)
	tail := strings.Repeat("x", 40) + " TAIL-MARKER"
	body := "sk-livesecret and then " + strings.Repeat("y", 700) + tail
	addDecideNote(t, checkout, "from-bo/secret.md", "bo-444444444444", "Please start the batch", body)
	f, url := startFakeJev(t)
	t.Setenv(decideTestKeyEnv, "sk-test-key")
	t.Setenv("TYPESAFE_API_KEY", "")

	invoke(t, "", decideArgs(checkout, url)...).mustCode(t, 0)

	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.states) == 0 {
		t.Fatal("provider was never called")
	}
	for _, s := range f.states {
		if strings.Contains(s, "sk-livesecret") {
			t.Fatalf("provider saw the unredacted key: %q", s)
		}
		if strings.Contains(s, "TAIL-MARKER") {
			t.Fatalf("provider saw body past the first 600 characters: %q", s)
		}
	}
}

// A STOP: subject is a structured signal: never sent, always needs_reply=1.00
// kind=edge.
func TestInboxDecideStopAndHoldBypassProvider(t *testing.T) {
	checkout, _ := busDir(t)
	publicBus(t, checkout)
	addDecideNote(t, checkout, "from-bo/stop.md", "bo-555555555555", "STOP: do not run this", "Ignore the earlier instruction.")
	addDecideNote(t, checkout, "from-bo/hold.md", "bo-666666666666", "HOLD: wait for me", "Please hold until I say go.")
	f, url := startFakeJev(t)
	t.Setenv(decideTestKeyEnv, "sk-test-key")
	t.Setenv("TYPESAFE_API_KEY", "")

	r := invoke(t, "", decideArgs(checkout, url)...).mustCode(t, 0)

	if f.sawState("do not run this") || f.sawState("wait for me") {
		t.Fatalf("a STOP/HOLD note reached the provider:\n%q", r.stdout)
	}
	if strings.Count(r.stdout, "kind=edge needs_reply=1.00") != 2 {
		t.Fatalf("STOP/HOLD notes were not marked as structured edge signals:\n%s", r.stdout)
	}
	// Only the fixture's own question note is a plain note to decide.
	if f.calls.Load() != 1 {
		t.Fatalf("want 1 provider call for the one plain note, got %d", f.calls.Load())
	}
}

// An empty inbox asks the provider nothing.
func TestInboxDecideEmptyInboxMakesNoProviderCalls(t *testing.T) {
	checkout, _ := busDir(t)
	publicBus(t, checkout)
	// Close every note addressed to Ada: a full read then has nothing to decide.
	writeFile(t, checkout, "from-ada/re-gate.md", "From: Ada\nTo: Bo\nDate: Mon Sep  7 00:04:00 UTC 2026\nSubject: Re: gate\nRe: bo-abcdef012345\n\nclosed\n")
	writeFile(t, checkout, "from-ada/re-heard.md", "From: Ada\nTo: Bo\nDate: Mon Sep  7 00:05:00 UTC 2026\nSubject: Re: heard\nRe: bo-111111111111\n\nclosed\n")
	gitIn(t, checkout, "add", "--", "from-ada/re-gate.md", "from-ada/re-heard.md")
	gitIn(t, checkout, "-c", "user.name=Ada", "-c", "user.email=ada@example.com", "commit", "-q", "-m", "close both")
	gitIn(t, checkout, "push", "-q", "origin", "main")
	f, url := startFakeJev(t)
	t.Setenv(decideTestKeyEnv, "sk-test-key")
	t.Setenv("TYPESAFE_API_KEY", "")

	r := invoke(t, "", decideArgs(checkout, url)...).mustCode(t, 0)

	if f.calls.Load() != 0 {
		t.Fatalf("empty inbox made %d provider calls, want 0", f.calls.Load())
	}
	if !strings.Contains(r.stdout, "INBOX DECIDED n=0 needs_reply=0 below_floor=0") {
		t.Fatalf("empty inbox did not print a zero summary:\n%s", r.stdout)
	}
}

// A bus with no .public marker is refused by name, and --allow-private is the
// only way past it.
func TestInboxDecideRefusesPrivateBusByName(t *testing.T) {
	checkout, _ := busDir(t)
	addDecideNote(t, checkout, "from-bo/note.md", "bo-777777777777", "Please start the batch", "start it")
	f, url := startFakeJev(t)
	t.Setenv(decideTestKeyEnv, "sk-test-key")
	t.Setenv("TYPESAFE_API_KEY", "")

	r := invoke(t, "", decideArgs(checkout, url)...).mustCode(t, 2)
	if !strings.Contains(r.stderr, ".public") || !strings.Contains(r.stderr, checkout) {
		t.Fatalf("private-bus refusal did not name the bus and its marker:\n%s", r.stderr)
	}
	if f.calls.Load() != 0 {
		t.Fatalf("a refused private bus still called the provider %d times", f.calls.Load())
	}

	r = invoke(t, "", decideArgs(checkout, url, "--allow-private")...).mustCode(t, 0)
	if f.calls.Load() == 0 {
		t.Fatalf("--allow-private did not let the decision through:\n%s", r.stdout)
	}
}
