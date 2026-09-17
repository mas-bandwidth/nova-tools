// Red tests for internal/decide, written before the implementation.
//
// The httptest end-to-end test uses the documented response shape. Where this
// sandbox forbids listening sockets (even the repo's own httptest suite fails
// the same way), the socket-free tests below pin the same contract without a
// port: the request shape, the response parsing, the refusal, and the line.
package decide

import (
	"context"
	"encoding/json"
	"io"
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

// New refuses when the env var is unset, naming it.
func TestNewRefusesWhenEnvUnset(t *testing.T) {
	t.Setenv("CARD8331_JEV_KEY", "")
	t.Setenv("TYPESAFE_API_KEY", "")
	_, err := New("http://example.invalid", "CARD8331_JEV_KEY")
	if err == nil {
		t.Fatal("expected error when key env var is unset")
	}
	if !strings.Contains(err.Error(), "CARD8331_JEV_KEY") {
		t.Fatalf("error must name the variable, got: %v", err)
	}
}

// The request carries the documented body and the key on the header.
func TestRequestSendsDocumentedBody(t *testing.T) {
	t.Setenv("CARD8331_JEV_KEY", "sekret")
	c, err := New("http://example.invalid", "CARD8331_JEV_KEY")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	qs := map[string]Question{
		"gate":  {Instructions: "go or wait?", Choice: map[string]string{"go": "proceed", "wait": "hold"}},
		"risk":  {Instructions: "how risky?", Score: []string{"low", "mid", "high", "top"}},
		"nullq": {Instructions: "nothing to decide", Noul: true},
	}
	req, cancel, err := c.newRequest(context.Background(), "some state", qs)
	if err != nil {
		t.Fatalf("newRequest: %v", err)
	}
	defer cancel()
	if req.Method != http.MethodPost {
		t.Fatalf("method = %s, want POST", req.Method)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer sekret" {
		t.Fatalf("auth header = %q, want Bearer sekret", got)
	}
	raw, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if body["model"] != "jev-latest" {
		t.Fatalf("model = %v, want jev-latest", body["model"])
	}
	if body["state"] != "some state" {
		t.Fatalf("state = %v", body["state"])
	}
	qmap, _ := body["questions"].(map[string]any)
	if qmap == nil {
		t.Fatalf("questions missing: %v", body)
	}
	gate, _ := qmap["gate"].(map[string]any)
	if gate["type"] != "choice" || gate["instructions"] != "go or wait?" {
		t.Fatalf("gate question = %v", gate)
	}
	crit, _ := gate["criteria"].(map[string]any)
	if crit["go"] != "proceed" || crit["wait"] != "hold" {
		t.Fatalf("gate criteria = %v", crit)
	}
	risk, _ := qmap["risk"].(map[string]any)
	if risk["type"] != "score" {
		t.Fatalf("risk question = %v", risk)
	}
	levels, _ := risk["criteria"].([]any)
	if len(levels) != 4 || levels[0] != "low" {
		t.Fatalf("risk criteria = %v", risk["criteria"])
	}
	nullq, _ := qmap["nullq"].(map[string]any)
	if nullq["type"] != "noul" || nullq["instructions"] != "nothing to decide" {
		t.Fatalf("nullq question = %v", nullq)
	}
	if _, ok := nullq["criteria"]; ok {
		t.Fatalf("noul question must carry no criteria: %v", nullq)
	}
	if _, ok := req.Context().Deadline(); !ok {
		t.Fatal("request carries no deadline")
	}
}

// The documented response shape parses into choice/score/noul answers.
func TestDecodeParsesAllAnswerKinds(t *testing.T) {
	answers, usage, err := decodeResponse([]byte(`{"answers": {
		"gate": {"type":"choice","choice":"go","probabilities":{"go":0.93,"wait":0.07},"confidence":0.93},
		"risk": {"type":"score","score":2.5,"legend":{"0":"low","3":"high"},"confidence":0.81},
		"nullq": {"type":"noul","noul":0.12}
	}, "usage": {"input_tokens": 10, "output_tokens": 3}}`))
	if err != nil {
		t.Fatalf("decodeResponse: %v", err)
	}
	if answers["gate"].Choice != "go" || answers["gate"].Confidence != 0.93 {
		t.Fatalf("gate answer = %+v", answers["gate"])
	}
	if answers["gate"].Probabilities["go"] != 0.93 || answers["gate"].Probabilities["wait"] != 0.07 {
		t.Fatalf("gate probabilities = %+v", answers["gate"].Probabilities)
	}
	if answers["risk"].Score != 2.5 || answers["risk"].Confidence != 0.81 {
		t.Fatalf("risk answer = %+v", answers["risk"])
	}
	if answers["nullq"].Noul != 0.12 {
		t.Fatalf("nullq answer = %+v", answers["nullq"])
	}
	if usage.InputTokens != 10 || usage.OutputTokens != 3 {
		t.Fatalf("usage = %+v", usage)
	}
}

// roundTripFunc is an in-process fake transport: the same Decide path as the
// httptest test above, with no socket.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Decide end to end over a fake transport: documented body out, typed
// answers back, usage counted.
func TestDecideRoundTrip(t *testing.T) {
	var gotBody map[string]any
	var gotAuth string
	c := &Client{
		baseURL: "http://example.invalid",
		key:     "sekret",
		http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			gotAuth = r.Header.Get("Authorization")
			raw, _ := io.ReadAll(r.Body)
			if err := json.Unmarshal(raw, &gotBody); err != nil {
				t.Errorf("decode body: %v", err)
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body: io.NopCloser(strings.NewReader(`{"answers": {
					"gate": {"type":"choice","choice":"go","probabilities":{"go":0.93,"wait":0.07},"confidence":0.93},
					"risk": {"type":"score","score":2.5,"legend":{"0":"low","3":"high"},"confidence":0.81},
					"nullq": {"type":"noul","noul":0.12}
				}, "usage": {"input_tokens": 10, "output_tokens": 3}}`)),
			}, nil
		})},
	}
	qs := map[string]Question{
		"gate":  {Instructions: "go or wait?", Choice: map[string]string{"go": "proceed", "wait": "hold"}},
		"risk":  {Instructions: "how risky?", Score: []string{"low", "mid", "high", "top"}},
		"nullq": {Instructions: "nothing to decide", Noul: true},
	}
	answers, usage, err := c.Decide(context.Background(), "some state", qs)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if gotAuth != "Bearer sekret" {
		t.Fatalf("auth header = %q, want Bearer sekret", gotAuth)
	}
	if gotBody["model"] != "jev-latest" || gotBody["state"] != "some state" {
		t.Fatalf("body = %v", gotBody)
	}
	qmap, _ := gotBody["questions"].(map[string]any)
	if qmap == nil || qmap["gate"] == nil || qmap["risk"] == nil || qmap["nullq"] == nil {
		t.Fatalf("questions body missing entries: %v", gotBody["questions"])
	}
	if answers["gate"].Choice != "go" || answers["gate"].Confidence != 0.93 {
		t.Fatalf("gate answer = %+v", answers["gate"])
	}
	if answers["risk"].Score != 2.5 || answers["nullq"].Noul != 0.12 {
		t.Fatalf("risk/nullq answers = %+v %+v", answers["risk"], answers["nullq"])
	}
	if usage.InputTokens != 10 || usage.OutputTokens != 3 {
		t.Fatalf("usage = %+v", usage)
	}
}

// A provider 500 is a provider error.
func TestDecideRefusesOn500(t *testing.T) {
	c := &Client{
		baseURL: "http://example.invalid",
		key:     "sekret",
		http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusInternalServerError, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("boom"))}, nil
		})},
	}
	_, _, err := c.Decide(context.Background(), "s", map[string]Question{"g": {Instructions: "x", Noul: true}})
	if err == nil || !strings.Contains(err.Error(), "provider error") {
		t.Fatalf("expected provider error, got: %v", err)
	}
}

// Decide sends the documented body and parses choice/score/noul answers,
// against an httptest fake returning the documented response shape.
func TestDecideSendsBodyAndParses(t *testing.T) {
	var gotBody map[string]any
	var gotAuth string
	srv := fakeServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"answers": {
			"gate": {"type":"choice","choice":"go","probabilities":{"go":0.93,"wait":0.07},"confidence":0.93},
			"risk": {"type":"score","score":2.5,"legend":{"0":"low","3":"high"},"confidence":0.81},
			"nullq": {"type":"noul","noul":0.12}
		}, "usage": {"input_tokens": 10, "output_tokens": 3}}`))
	})
	defer srv.Close()

	t.Setenv("CARD8331_JEV_KEY", "sekret")
	c, err := New(srv.URL, "CARD8331_JEV_KEY")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	qs := map[string]Question{
		"gate":  {Instructions: "go or wait?", Choice: map[string]string{"go": "proceed", "wait": "hold"}},
		"risk":  {Instructions: "how risky?", Score: []string{"low", "mid", "high", "top"}},
		"nullq": {Instructions: "nothing to decide", Noul: true},
	}
	answers, usage, err := c.Decide(context.Background(), "some state", qs)
	if err != nil {
		t.Fatalf("Decide: %v", err)
	}
	if gotAuth != "Bearer sekret" {
		t.Fatalf("auth header = %q, want Bearer sekret", gotAuth)
	}
	if gotBody["model"] != "jev-latest" {
		t.Fatalf("model = %v, want jev-latest", gotBody["model"])
	}
	if gotBody["state"] != "some state" {
		t.Fatalf("state = %v", gotBody["state"])
	}
	qmap, _ := gotBody["questions"].(map[string]any)
	if qmap == nil || qmap["gate"] == nil || qmap["risk"] == nil || qmap["nullq"] == nil {
		t.Fatalf("questions body missing entries: %v", gotBody["questions"])
	}
	if answers["gate"].Choice != "go" || answers["gate"].Confidence != 0.93 {
		t.Fatalf("gate answer = %+v", answers["gate"])
	}
	if answers["gate"].Probabilities["go"] != 0.93 {
		t.Fatalf("gate probabilities = %+v", answers["gate"].Probabilities)
	}
	if answers["risk"].Score != 2.5 {
		t.Fatalf("risk answer = %+v", answers["risk"])
	}
	if answers["nullq"].Noul != 0.12 {
		t.Fatalf("nullq answer = %+v", answers["nullq"])
	}
	if usage.InputTokens != 10 || usage.OutputTokens != 3 {
		t.Fatalf("usage = %+v", usage)
	}
}

// Line renders confidence and the below list.
func TestLineRendersConfidenceAndBelow(t *testing.T) {
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
	line := Line("DECIDE", map[string]Answer{
		"gate": {Type: "choice", Choice: "go", Confidence: 0.95},
	}, 0.9)
	if !strings.Contains(line, "below=-") {
		t.Fatalf("clean line must say below=-: %q", line)
	}
}

// Bad questions are refused, never guessed.
func TestParseQuestionsRefuses(t *testing.T) {
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
