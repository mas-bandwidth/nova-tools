package jevclient

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/quick"
)

func TestCredentialSelectionAndDefaults(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"", map[string]string{DefaultKeyEnv: "primary", FallbackKeyEnv: "fallback"}, "primary"},
		{"CUSTOM", map[string]string{"CUSTOM": "custom", FallbackKeyEnv: "fallback"}, "custom"},
		{"CUSTOM", map[string]string{FallbackKeyEnv: "fallback"}, "fallback"},
		{FallbackKeyEnv, map[string]string{FallbackKeyEnv: "fallback"}, "fallback"},
	} {
		c, err := newClient("", tc.name, func(k string) string { return tc.env[k] })
		if err != nil || c.key != tc.want || c.baseURL != DefaultBaseURL || c.http.Timeout != deadline {
			t.Fatalf("credential/default selection: %v", err)
		}
	}
	_, err := newClient("", "", func(string) string { return "" })
	if err == nil || !strings.Contains(err.Error(), DefaultKeyEnv) {
		t.Fatalf("missing default key: %v", err)
	}
}

func TestRequestRefusalsNeverCallProvider(t *testing.T) {
	t.Parallel()
	for _, qs := range []map[string]Question{
		nil,
		{"bad": {}},
		{"bad": {Choice: map[string]string{"yes": "yes"}, Noul: true}},
		{"bad": {Choice: map[string]string{}}},
		{"bad": {Score: []string{}}},
	} {
		c := &Client{baseURL: "http://example.invalid", key: "fixture-key", http: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			t.Error("refused question reached provider")
			return nil, errors.New("unexpected call")
		})}}
		answers, usage, err := c.Decide(context.Background(), "evidence", qs)
		if err == nil || answers != nil || usage.Known() || strings.Contains(err.Error(), "fixture-key") {
			t.Fatalf("question refusal: answers=%v usage=%v error=%v", answers, usage, err)
		}
	}
	c := &Client{baseURL: ":bad", key: "fixture-key"}
	_, _, err := c.Decide(context.Background(), "evidence", map[string]Question{"q": {Noul: true}})
	if err == nil || !strings.Contains(err.Error(), "build request") {
		t.Fatalf("bad URL: %v", err)
	}
}

type responseBody struct {
	io.Reader
	closed bool
}

func (b *responseBody) Close() error { b.closed = true; return nil }

type brokenReader struct{}

func (brokenReader) Read([]byte) (int, error) { return 0, errors.New("fixture read failure") }

func TestProviderRefusalsCloseBodiesAndKeepKnownUsage(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, raw, want    string
		readFailure, known bool
	}{
		{name: "read", readFailure: true, want: "read response"},
		{name: "malformed", raw: "{", want: "decode response"},
		{name: "type", raw: `{"answers":{"q":{"type":"other"}}}`, want: "unknown answer type"},
		{name: "unoffered", raw: `{"answers":{"q":{"type":"choice","choice":"outside"}},"usage":{"input_tokens":0}}`, want: "does not offer", known: true},
	} {
		body := &responseBody{Reader: strings.NewReader(tc.raw)}
		if tc.readFailure {
			body.Reader = brokenReader{}
		}
		c := &Client{baseURL: "http://example.invalid", http: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusOK, Body: body}, nil
		})}}
		answers, usage, err := c.Decide(context.Background(), "evidence", map[string]Question{"q": {Choice: map[string]string{"inside": "offered"}}})
		if err == nil || !strings.Contains(err.Error(), tc.want) || answers != nil || usage.Known() != tc.known || !body.closed {
			t.Fatalf("%s: answers=%v usage=%v closed=%v error=%v", tc.name, answers, usage, body.closed, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	c := &Client{baseURL: "http://example.invalid", http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return nil, r.Context().Err()
	})}}
	_, _, err := c.Decide(ctx, "evidence", map[string]Question{"q": {Noul: true}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
}

func TestChoiceValidationIsClosedUnderArbitraryNames(t *testing.T) {
	t.Parallel()
	property := func(offered, answer string) bool {
		qs := map[string]Question{"q": {Choice: map[string]string{offered: "offered"}}}
		err := ValidateAnswers(qs, map[string]Answer{"q": {Type: "choice", Choice: answer, Confidence: 1}})
		return (err == nil) == (strings.TrimSpace(answer) != "" && answer == offered)
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 500, Rand: rand.New(rand.NewSource(4460))}); err != nil {
		t.Fatal(err)
	}
	if err := quick.Check(func(name string) bool { return property(name, name) }, &quick.Config{MaxCount: 500, Rand: rand.New(rand.NewSource(4461))}); err != nil {
		t.Fatal(err)
	}
	for _, qs := range []map[string]Question{{"q": {Score: []string{"low", "high"}}}, {"q": {Noul: true}}} {
		if err := ValidateAnswers(qs, map[string]Answer{"q": {Type: "choice", Choice: "wrong"}}); err == nil {
			t.Fatal("wrong answer type accepted")
		}
	}
	if err := ValidateAnswers(nil, map[string]Answer{"unasked": {}}); err == nil {
		t.Fatal("unasked answer accepted")
	}
}

func TestConcurrentRequestsKeepTheirOwnEvidence(t *testing.T) {
	t.Parallel()
	c := &Client{baseURL: "http://example.invalid", http: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if _, ok := r.Context().Deadline(); !ok {
			t.Error("request has no deadline")
		}
		var request struct {
			State string `json:"state"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			return nil, err
		}
		body, err := json.Marshal(map[string]any{"answers": map[string]any{"q": map[string]any{"type": "choice", "choice": request.State}}, "usage": map[string]int{"input_tokens": 1}})
		if err != nil {
			return nil, err
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(string(body)))}, nil
	})}}
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			state := "request-" + strconv.Itoa(i)
			answers, usage, err := c.Decide(context.Background(), state, map[string]Question{"q": {Choice: map[string]string{state: "this request"}}})
			if err != nil || answers["q"].Choice != state || !usage.HasInput || usage.InputTokens != 1 {
				t.Errorf("concurrent result: %v %v %v", answers, usage, err)
			}
		}()
	}
	wg.Wait()
}

func TestJournalValuesKeepTheirTypedWireMeaning(t *testing.T) {
	t.Parallel()
	property := func(value float64) bool {
		for _, q := range []Question{{Score: []string{"low", "high"}}, {Noul: true}} {
			wire, err := q.wire()
			if err != nil || wire.Type != q.Kind() {
				return false
			}
			a := Answer{Type: q.Kind(), Score: value, Noul: value}
			number, err := strconv.ParseFloat(a.Value(), 64)
			if err != nil || number != value {
				return false
			}
		}
		return true
	}
	if err := quick.Check(property, &quick.Config{MaxCount: 500, Rand: rand.New(rand.NewSource(4462))}); err != nil {
		t.Fatal(err)
	}
	q := Question{Choice: map[string]string{"keep": "keep"}}
	wire, err := q.wire()
	if err != nil || wire.Type != q.Kind() || (Answer{Type: "choice", Choice: "keep"}).Value() != "keep" {
		t.Fatal("choice journal value differs from its wire meaning")
	}
	if (Question{}).Kind() != "" {
		t.Fatal("malformed question has a journal kind")
	}
}
