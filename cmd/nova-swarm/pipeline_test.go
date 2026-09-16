//go:build !windows

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// THE NATIVE PATH'S TWO MODES (issue #856). `--mode pipeline` runs the card's steps as a
// pipeline of stateless calls and never starts the harness at all; `MODE: explore` keeps
// today's harness loop under a turn budget the harness log is counted against. These tests
// run the real verb against a fake endpoint and the fake harness: no provider, no network.

const pipeKey = "sk-fake-cmd-2718281828-never-logged"

// pipeEndpoint answers each call with the next canned body and counts the calls.
func pipeEndpoint(t *testing.T, bodies ...string) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+pipeKey {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		n := int(calls.Add(1))
		body := "nothing more"
		if n <= len(bodies) {
			body = bodies[n-1]
		}
		raw, _ := json.Marshal(body)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"choices":[{"message":{"content":%s}}],"usage":{"prompt_tokens":%d,"completion_tokens":7}}`, raw, 100+n)
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// pipeJobRepo makes the clone the card's steps work in, under the job directory the native
// run will choose for this label.
func pipeJobRepo(t *testing.T, slot, label string) string {
	t.Helper()
	repo := filepath.Join(slot, "jobs", label, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "hello.go"), []byte("package hello\n\nconst Name = \"hello\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, argv := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "fixture@example.invalid"},
		{"config", "user.name", "Fixture"},
		{"add", "-A"},
		{"commit", "-q", "-m", "base"},
	} {
		cmd := exec.Command("git", argv...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", argv, err, out)
		}
	}
	return repo
}

const pipeCard = "RESULT: CARD-P the pipeline card\n" +
	"You are a Go engineer. MODE: pipeline\n" +
	"STEP 1. git rev-parse --abbrev-ref HEAD\n" +
	"STEP 2. Write the red test in `hello_test.go`.\n" +
	"STEP T. grep -q FIXED hello.go\n" +
	"STEP 3. Implement the fix in `hello.go`.\n" +
	"STEP C. git add -A && git commit -q -m pipeline\n" +
	"STEP 4. Write RESULT.md: line 1 the RESULT line above.\n"

const pipeRedDiff = "```\ndiff --git a/hello_test.go b/hello_test.go\nnew file mode 100644\n--- /dev/null\n+++ b/hello_test.go\n@@ -0,0 +1 @@\n+package hello\n```\n"
const pipeFixDiff = "```\ndiff --git a/hello.go b/hello.go\n--- a/hello.go\n+++ b/hello.go\n@@ -1,3 +1,3 @@\n package hello\n \n-const Name = \"hello\"\n+const Name = \"FIXED\"\n```\n"
const pipeResult = "```\nRESULT: CARD-P the pipeline card\nDONE -- the pipeline card landed\n```\n"

// TEST: `native --mode pipeline` runs the card as a pipeline -- three model calls for its
// three model steps -- the harness binary is never started, and the NATIVE line carries the
// mode, the steps, the calls and the tokens.
func TestNativePipelineModeRunsTheStepsAndNamesThemOnTheLine(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	pipeJobRepo(t, slot, "pipe-label")
	srv, calls := pipeEndpoint(t, pipeRedDiff, pipeFixDiff, pipeResult)
	cardPath := filepath.Join(root, "card.md")
	if err := os.WriteFile(cardPath, []byte(pipeCard), 0o644); err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(root, "key")
	if err := os.WriteFile(keyFile, []byte(pipeKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	rc := run([]string{
		"native", "--harness", bin, "--model", "deepseek/deepseek-v4-pro",
		"--label", "pipe-label", "--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "60s", "--no-wall",
		"--mode", "pipeline", "--endpoint", srv.URL, "--key-file", keyFile,
	}, strings.NewReader(""), &stdout, &stderr, time.Now())
	if rc != 0 {
		t.Fatalf("the pipeline run exited %d:\n%s\n%s", rc, stdout.String(), stderr.String())
	}
	if got := calls.Load(); got != 3 {
		t.Errorf("the endpoint was called %d times, want 3:\n%s", got, stderr.String())
	}
	line := stdout.String()
	for _, want := range []string{"NATIVE OK ", "mode=pipeline", "steps=6", "calls=3", "in=306", "out=21"} {
		if !strings.Contains(line, want) {
			t.Errorf("the NATIVE line carries no %s:\n%s", want, line)
		}
	}
	// The harness was never started: a pipeline is the harness.
	if _, err := os.Stat(filepath.Join(slot, "jobs", "pipe-label", "argv")); err == nil {
		t.Errorf("the harness binary ran under --mode pipeline")
	}
	body, err := os.ReadFile(filepath.Join(slot, "jobs", "pipe-label", "RESULT.md"))
	if err != nil || !strings.HasPrefix(string(body), "RESULT: CARD-P the pipeline card") {
		t.Errorf("no RESULT.md carrying the contract line: %v %q", err, body)
	}
	// Each call's input size is in the run's own capture, which is what a reader reads.
	capture, err := os.ReadFile(filepath.Join(slot, "jobs", "pipe-label", "harness-output.log"))
	if err != nil {
		t.Fatalf("no capture: %v", err)
	}
	if n := strings.Count(string(capture), "kind=model"); n != 3 {
		t.Errorf("the capture shows %d model calls, want 3:\n%s", n, capture)
	}
	if strings.Contains(string(capture), pipeKey) || strings.Contains(stdout.String(), pipeKey) || strings.Contains(stderr.String(), pipeKey) {
		t.Errorf("the key reached a log")
	}
}

// TEST: a `MODE: explore` card keeps the harness loop and is STOPPED at its turn budget,
// and the partial RESULT names the budget -- a card that would have run to the deadline
// instead ends with one readable line about why (issues #855, #856).
func TestNativeExploreStopsAtItsTurnBudgetAndSaysSo(t *testing.T) {
	bin := nativeHarness(t)
	root, slot := aSlot(t)
	cardPath := filepath.Join(root, "card.md")
	card := "RESULT: CARD-E the explore card\nMODE: explore\nSTEP 1. find where the rule lives\nFAKE-TURNS 40\n"
	if err := os.WriteFile(cardPath, []byte(card), 0o644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	rc := run([]string{
		"native", "--harness", bin, "--model", "fake/fake-model",
		"--label", "explore-label", "--card", cardPath, "--slot", slot, "--root", root,
		"--deadline", "60s", "--no-wall", "--max-turns", "3",
	}, strings.NewReader(""), &stdout, &stderr, time.Now())
	if rc == 0 {
		t.Errorf("a card stopped at its budget exits non-zero, got %d", rc)
	}
	if !strings.Contains(stdout.String(), "turns=") || !strings.Contains(stdout.String(), "max_turns=3") {
		t.Errorf("the NATIVE line does not name the budget:\n%s", stdout.String())
	}
	body, err := os.ReadFile(filepath.Join(slot, "jobs", "explore-label", "RESULT.md"))
	if err != nil {
		t.Fatalf("the stopped card published no partial RESULT: %v", err)
	}
	lines := strings.Split(string(body), "\n")
	if lines[0] != "RESULT: CARD-E the explore card" {
		t.Errorf("the partial RESULT's line 1 is not the contract line: %q", lines[0])
	}
	if len(lines) < 2 || !strings.HasPrefix(lines[1], "ABSTAIN") || !strings.Contains(lines[1], "max=3") {
		t.Errorf("the partial RESULT does not name the budget:\n%s", body)
	}
}
