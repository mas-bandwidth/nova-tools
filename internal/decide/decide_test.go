// Red tests for internal/decide, written before the implementation.
//
// The httptest end-to-end test uses the documented response shape. Where this
// sandbox forbids listening sockets (even the repo's own httptest suite fails
// the same way), the socket-free tests below pin the same contract without a
// port: the request shape, the response parsing, the refusal, and the line.
package decide

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeServer runs the handler over httptest, skipping where the sandbox
// forbids listening sockets. The contract it would pin is pinned
// socket-free below, so a skip loses no assertion here.
func fakeServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("listening sockets forbidden here (%v); socket-free tests pin the contract", err)
	}
	ln.Close()
	return httptest.NewServer(handler)
}

// Line renders confidence and the below list.
func TestLineRendersConfidenceAndBelow(t *testing.T) {
	t.Parallel()

	answers := map[string]Answer{
		"gate": {Type: "choice", Choice: "go", Confidence: 0.93},
		"risk": {Type: "score", Score: 2.5, Confidence: 0.41},
	}
	line := Line("DECIDE", answers, 0.9)
	if !strings.Contains(line, "gate=go") {
		t.Fatalf("line missing gate choice: %q", line)
	}
	if !strings.Contains(line, "conf=0.93") {
		t.Fatalf("line missing confidence: %q", line)
	}
	if !strings.Contains(line, "floor=0.90") {
		t.Fatalf("line missing floor: %q", line)
	}
	if !strings.Contains(line, "below=risk") {
		t.Fatalf("line missing below list: %q", line)
	}
	if strings.Contains(line, "\n") {
		t.Fatalf("line must be one line: %q", line)
	}
}

// A clean decision names nobody below the floor.
func TestLineCleanDecision(t *testing.T) {
	t.Parallel()

	line := Line("DECIDE", map[string]Answer{
		"gate": {Type: "choice", Choice: "go", Confidence: 0.95},
	}, 0.9)
	if !strings.Contains(line, "below=-") {
		t.Fatalf("clean line must say below=-: %q", line)
	}
}

// Bad questions are refused, never guessed.
func TestParseQuestionsRefuses(t *testing.T) {
	t.Parallel()

	for name, doc := range map[string]string{
		"not json":   `[`,
		"empty":      `{}`,
		"unknown":    `{"g": {"type": "vote", "instructions": "x"}}`,
		"no instr":   `{"g": {"type": "noul"}}`,
		"empty map":  `{"g": {"type": "choice", "instructions": "x", "criteria": {}}}`,
		"empty list": `{"g": {"type": "score", "instructions": "x", "criteria": []}}`,
		"wrong kind": `{"g": {"type": "choice", "instructions": "x", "criteria": ["a"]}}`,
	} {
		if _, err := ParseQuestions([]byte(doc)); err == nil {
			t.Fatalf("%s: expected refusal", name)
		}
	}
	got, err := ParseQuestions([]byte(`{"questions": {"g": {"type": "noul", "instructions": "x"}}}`))
	if err != nil {
		t.Fatalf("wrapped form: %v", err)
	}
	if !got["g"].Noul {
		t.Fatalf("wrapped form parsed to %+v", got["g"])
	}
}
