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

// fakeServer runs the handler over httptest, skipping where the sandbox
// forbids listening sockets. Refusal paths below run socket-free everywhere.
func fakeServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("listening sockets forbidden here (%v); refusal tests pin the verb", err)
	}
	ln.Close()
	return httptest.NewServer(handler)
}

func writeQuestions(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "q.json")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func choiceQuestions() map[string]any {
	return map[string]any{
		"gate": map[string]any{"type": "choice", "instructions": "go?", "criteria": map[string]string{"go": "proceed", "wait": "hold"}},
	}
}

// Below the floor the verb exits 3 with one line.
func TestVerbExits3BelowFloor(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"answers": {"gate": {"type":"choice","choice":"go","probabilities":{"go":0.4,"wait":0.6},"confidence":0.4}}, "usage": {"input_tokens": 1, "output_tokens": 1}}`))
	})
	defer srv.Close()

	q := writeQuestions(t, choiceQuestions())
	t.Setenv("CARD8331_JEV_KEY", "sekret")
	var stdout, stderr bytes.Buffer
	code := run([]string{"--questions", q, "--state", q, "--floor", "0.9", "--base-url", srv.URL, "--key-env", "CARD8331_JEV_KEY"}, &stdout, &stderr)
	if code != 3 {
		t.Fatalf("exit = %d, want 3 (stdout=%q stderr=%q)", code, stdout.String(), stderr.String())
	}
	out := strings.TrimSpace(stdout.String() + stderr.String())
	if !strings.Contains(out, "below=gate") {
		t.Fatalf("output missing below=gate: %q", out)
	}
	if line := strings.TrimSpace(stdout.String()); line != "" && strings.Contains(line, "\n") {
		t.Fatalf("must print exactly one line: %q", stdout.String())
	}
}

// A provider 500 is a refusal: exit 2 with a REFUSED line.
func TestVerbExits2On500(t *testing.T) {
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	defer srv.Close()

	q := writeQuestions(t, choiceQuestions())
	t.Setenv("CARD8331_JEV_KEY", "sekret")
	var stdout, stderr bytes.Buffer
	code := run([]string{"--questions", q, "--state", q, "--base-url", srv.URL, "--key-env", "CARD8331_JEV_KEY"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stdout=%q stderr=%q)", code, stdout.String(), stderr.String())
	}
	combined := stdout.String() + stderr.String()
	if !strings.Contains(combined, "REFUSED") || !strings.Contains(combined, "reason=") {
		t.Fatalf("refusal line must say REFUSED reason=...: %q", combined)
	}
}

// No key is a refusal naming the variable, never printing the key.
func TestVerbExits2WithoutKey(t *testing.T) {
	q := writeQuestions(t, choiceQuestions())
	t.Setenv("CARD8331_JEV_KEY", "")
	t.Setenv("TYPESAFE_API_KEY", "")
	var stdout, stderr bytes.Buffer
	code := run([]string{"--questions", q, "--state", q, "--key-env", "CARD8331_JEV_KEY"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stdout=%q stderr=%q)", code, stdout.String(), stderr.String())
	}
	combined := stdout.String() + stderr.String()
	if !strings.Contains(combined, "REFUSED") || !strings.Contains(combined, "reason=no-key") {
		t.Fatalf("refusal must say REFUSED reason=no-key: %q", combined)
	}
	if !strings.Contains(combined, "CARD8331_JEV_KEY") {
		t.Fatalf("refusal must name the variable: %q", combined)
	}
}

// Bad questions are a refusal.
func TestVerbExits2OnBadQuestions(t *testing.T) {
	q := writeQuestions(t, map[string]any{
		"gate": map[string]any{"type": "vote", "instructions": "go?"},
	})
	t.Setenv("CARD8331_JEV_KEY", "sekret")
	var stdout, stderr bytes.Buffer
	code := run([]string{"--questions", q, "--state", q, "--key-env", "CARD8331_JEV_KEY"}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit = %d, want 2 (stdout=%q stderr=%q)", code, stdout.String(), stderr.String())
	}
	if combined := stdout.String() + stderr.String(); !strings.Contains(combined, "reason=bad-questions") {
		t.Fatalf("refusal must say reason=bad-questions: %q", combined)
	}
}

// A missing --questions is a refusal, not a guess.
func TestVerbExits2WithoutQuestions(t *testing.T) {
	t.Setenv("CARD8331_JEV_KEY", "sekret")
	var stdout, stderr bytes.Buffer
	if code := run([]string{}, &stdout, &stderr); code != 2 {
		t.Fatalf("exit = %d, want 2 (stdout=%q stderr=%q)", code, stdout.String(), stderr.String())
	}
}
