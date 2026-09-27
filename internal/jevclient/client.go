// Package jevclient makes one typed decision per call through TypeSafe Jev.
//
// The provider is POST <baseURL> with header Authorization: Bearer <key> and
// a JSON body {"state": <text>, "model": "jev-latest", "questions": {...}}.
// Each question is one of three kinds: a choice among named options, a score
// against ordered levels, or a noul (null) statement. The response carries
// one typed answer per question plus a usage count.
//
// The key comes ONLY from the environment variable the caller names (New's
// keyEnv, default JEV_API_KEY with TYPESAFE_API_KEY also accepted): never a
// file, never argv, and it is never printed. A decision below the floor is a
// suggestion, never an authorization: callers keep today's behaviour as the
// fallback.
package jevclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

// DefaultBaseURL is the TypeSafe Jev endpoint New uses when baseURL is empty.
const DefaultBaseURL = "https://api.typesafe.ai/v1/systemone"

// DefaultModel is the model every request names.
const DefaultModel = "jev-latest"

// DefaultKeyEnv is the environment variable New reads when keyEnv is empty.
// TYPESAFE_API_KEY is accepted as a fallback.
const DefaultKeyEnv = "JEV_API_KEY"

// FallbackKeyEnv is accepted when the named variable is unset.
const FallbackKeyEnv = "TYPESAFE_API_KEY"

// deadline is the budget one Decide call gets.
const deadline = 10 * time.Second

// Question is one typed question. Exactly one of Choice, Score or Noul must
// be set: a non-nil Choice is a choice question with per-option descriptions,
// a non-nil Score is a score question with ordered level texts, and Noul true
// is a noul question carrying only its statement in Instructions.
type Question struct {
	Instructions string
	Choice       map[string]string
	Score        []string
	Noul         bool
}

// Answer is one typed answer. Type names which of the three it is ("choice",
// "score" or "noul"). Choice and Probabilities are set for choice answers,
// Score for score answers, Noul for noul answers; Confidence is set for
// choice and score answers from the response, and for a noul answer it is the
// noul value itself: the confidence that the question is null.
type Answer struct {
	Type          string
	Choice        string
	Probabilities map[string]float64
	Score         float64
	Noul          float64
	Confidence    float64
}

// Usage counts the tokens one call spent, and says PER COUNTER whether the
// provider reported it at all. A 200 carrying a valid answer is not evidence of
// reported usage: a response with no usage object, or one naming only some of
// the counters, has said nothing about the rest -- and nothing is not zero. An
// explicitly reported 0 is a measurement and is kept as one (SPEC-TOKENS rule
// 14).
type Usage struct {
	InputTokens  int
	OutputTokens int
	HasInput     bool
	HasOutput    bool
}

// Known reports whether the provider measured anything at all.
func (u Usage) Known() bool { return u.HasInput || u.HasOutput }

// Client sends typed questions to one provider endpoint. It has no storage,
// policy, event journal or sprint dependency. It is safe for concurrent calls.
type Client struct {
	baseURL string
	key     string
	http    *http.Client
}

// New reads the key from the environment variable keyEnv (DefaultKeyEnv when
// empty, with FallbackKeyEnv also accepted) and refuses with an error naming
// the variable when it is unset. baseURL empty means DefaultBaseURL. The key
// is never printed.
func New(baseURL, keyEnv string) (*Client, error) {
	return newClient(baseURL, keyEnv, os.Getenv)
}

func newClient(baseURL, keyEnv string, getenv func(string) string) (*Client, error) {
	if keyEnv == "" {
		keyEnv = DefaultKeyEnv
	}
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	key := getenv(keyEnv)
	if key == "" && keyEnv != FallbackKeyEnv {
		key = getenv(FallbackKeyEnv)
	}
	if key == "" {
		return nil, fmt.Errorf("decide: %s is not set; refusing to guess", keyEnv)
	}
	return &Client{baseURL: baseURL, key: key, http: &http.Client{Timeout: deadline}}, nil
}

// questionWire is the documented question shape.
type questionWire struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
}

func (q Question) wire() (questionWire, error) {
	kinds := 0
	if q.Choice != nil {
		kinds++
	}
	if q.Score != nil {
		kinds++
	}
	if q.Noul {
		kinds++
	}
	if kinds != 1 {
		return questionWire{}, fmt.Errorf("decide: question must be exactly one of choice, score or noul")
	}
	switch {
	case q.Choice != nil:
		if len(q.Choice) == 0 {
			return questionWire{}, fmt.Errorf("decide: choice question needs at least one option")
		}
		return questionWire{Type: "choice", Instructions: q.Instructions, Criteria: q.Choice}, nil
	case q.Score != nil:
		if len(q.Score) == 0 {
			return questionWire{}, fmt.Errorf("decide: score question needs at least one level")
		}
		return questionWire{Type: "score", Instructions: q.Instructions, Criteria: q.Score}, nil
	default:
		return questionWire{Type: "noul", Instructions: q.Instructions}, nil
	}
}

// answerWire is the documented answer shape.
type answerWire struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Score         float64            `json:"score,omitempty"`
	Legend        map[string]string  `json:"legend,omitempty"`
	Noul          float64            `json:"noul,omitempty"`
	Confidence    float64            `json:"confidence,omitempty"`
}

func (w answerWire) answer() (Answer, error) {
	switch w.Type {
	case "choice":
		return Answer{Type: "choice", Choice: w.Choice, Probabilities: w.Probabilities, Confidence: w.Confidence}, nil
	case "score":
		return Answer{Type: "score", Score: w.Score, Confidence: w.Confidence}, nil
	case "noul":
		return Answer{Type: "noul", Noul: w.Noul, Confidence: w.Noul}, nil
	default:
		return Answer{}, fmt.Errorf("decide: unknown answer type %q", w.Type)
	}
}

// responseWire is the documented response shape. The usage counters are
// POINTERS on purpose: a missing field decodes as nil, which is an absence, and
// a present 0 decodes as a pointer to zero, which is a measurement. Decoding
// them as plain ints made every silent response look like a free one.
type responseWire struct {
	Answers map[string]answerWire `json:"answers"`
	Usage   struct {
		InputTokens  *int `json:"input_tokens"`
		OutputTokens *int `json:"output_tokens"`
	} `json:"usage"`
}

// Decide asks the provider one typed decision and parses the answers. It
// applies a 10 s deadline of its own. A decision below the caller's floor is
// a suggestion, never an authorization: the caller keeps today's behaviour
// as the fallback.
func (c *Client) Decide(ctx context.Context, state string, qs map[string]Question) (map[string]Answer, Usage, error) {
	req, cancel, err := c.newRequest(ctx, state, qs)
	if err != nil {
		return nil, Usage{}, err
	}
	defer cancel()
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, Usage{}, fmt.Errorf("decide: provider error: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, Usage{}, fmt.Errorf("decide: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, Usage{}, fmt.Errorf("decide: provider error: status %d", resp.StatusCode)
	}
	answers, usage, err := decodeResponse(raw)
	if err != nil {
		return nil, Usage{}, err
	}
	// A typed decision is typed at BOTH ends: an answer that is not one of the
	// question's own criteria is the provider failing to answer, and not a
	// decision with a confidence on it. It is refused HERE, before anything
	// records it as a decision or a floor is applied to it (edges 22 and 23).
	// The usage travels with the refusal: the call was made and it cost what it
	// cost, and a refusal cannot unspend it.
	if err := ValidateAnswers(qs, answers); err != nil {
		return nil, usage, err
	}
	return answers, usage, nil
}

// newRequest builds the documented POST: the state, the model and the typed
// questions, with the key on the Authorization header and a 10 s deadline.
// The key travels on the wire only, and is never printed. The caller holds
// the returned cancel until the request completes.
func (c *Client) newRequest(ctx context.Context, state string, qs map[string]Question) (*http.Request, context.CancelFunc, error) {
	noop := func() {}
	if len(qs) == 0 {
		return nil, noop, fmt.Errorf("decide: no questions given; refusing to guess")
	}
	wired := make(map[string]questionWire, len(qs))
	for name, q := range qs {
		w, err := q.wire()
		if err != nil {
			return nil, noop, fmt.Errorf("decide: question %q: %w", name, err)
		}
		wired[name] = w
	}
	body, err := json.Marshal(map[string]any{
		"state":     state,
		"model":     DefaultModel,
		"questions": wired,
	})
	if err != nil {
		return nil, noop, fmt.Errorf("decide: encode request: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, deadline)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(body))
	if err != nil {
		cancel()
		return nil, noop, fmt.Errorf("decide: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.key)
	return req, cancel, nil
}

// decodeResponse parses the documented response shape into typed answers.
func decodeResponse(raw []byte) (map[string]Answer, Usage, error) {
	var rw responseWire
	if err := json.Unmarshal(raw, &rw); err != nil {
		return nil, Usage{}, fmt.Errorf("decide: decode response: %w", err)
	}
	out := make(map[string]Answer, len(rw.Answers))
	for name, w := range rw.Answers {
		a, err := w.answer()
		if err != nil {
			return nil, Usage{}, fmt.Errorf("decide: answer %q: %w", name, err)
		}
		out[name] = a
	}
	usage := Usage{}
	if rw.Usage.InputTokens != nil {
		usage.InputTokens, usage.HasInput = *rw.Usage.InputTokens, true
	}
	if rw.Usage.OutputTokens != nil {
		usage.OutputTokens, usage.HasOutput = *rw.Usage.OutputTokens, true
	}
	return out, usage, nil
}
