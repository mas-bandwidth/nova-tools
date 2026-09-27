package jevclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// New refuses when the env var is unset, naming it.
func TestNewRefusesWhenEnvUnset(t *testing.T) {
	t.Parallel()
	_, err := newClient("http://example.invalid", "CARD8331_JEV_KEY", func(string) string { return "" })
	if err == nil {
		t.Fatal("expected error when key env var is unset")
	}
	if !strings.Contains(err.Error(), "CARD8331_JEV_KEY") {
		t.Fatalf("error must name the variable, got: %v", err)
	}
}

// The request carries the documented body and the key on the header.
func TestRequestSendsDocumentedBody(t *testing.T) {
	t.Parallel()
	c, err := newClient("http://example.invalid", "CARD8331_JEV_KEY", func(string) string { return "sekret" })
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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()

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
	t.Parallel()
	var gotBody map[string]any
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	}))
	defer srv.Close()

	c, err := newClient(srv.URL, "CARD8331_JEV_KEY", func(string) string { return "sekret" })
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
