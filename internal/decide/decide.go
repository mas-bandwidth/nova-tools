// Package decide makes one typed decision per call through TypeSafe Jev.
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
package decide

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
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

// Usage counts the tokens one call spent.
type Usage struct {
	InputTokens  int
	OutputTokens int
}

// Client talks to one Jev endpoint with one key the caller named.
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
	if keyEnv == "" {
		keyEnv = DefaultKeyEnv
	}
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	key := os.Getenv(keyEnv)
	if key == "" && keyEnv != FallbackKeyEnv {
		key = os.Getenv(FallbackKeyEnv)
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

// responseWire is the documented response shape.
type responseWire struct {
	Answers map[string]answerWire `json:"answers"`
	Usage   struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
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
	return decodeResponse(raw)
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
	return out, Usage{InputTokens: rw.Usage.InputTokens, OutputTokens: rw.Usage.OutputTokens}, nil
}

// ParseQuestions parses a questions file: either a bare map of name to
// question, or {"questions": {...}}. Each entry carries a type
// (choice/score/noul), instructions, and criteria (a map for choice, a list
// for score, absent for noul). Anything else is a refusal, never a guess.
func ParseQuestions(data []byte) (map[string]Question, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, fmt.Errorf("decide: bad questions: not a JSON object: %w", err)
	}
	raw := top
	if inner, ok := top["questions"]; ok && len(top) == 1 {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(inner, &m); err != nil {
			return nil, fmt.Errorf("decide: bad questions: \"questions\" is not an object")
		}
		raw = m
	}
	if len(raw) == 0 {
		return nil, fmt.Errorf("decide: bad questions: no questions given")
	}
	out := make(map[string]Question, len(raw))
	for name, r := range raw {
		var qw questionWire
		dec := json.NewDecoder(bytes.NewReader(r))
		dec.UseNumber()
		var generic map[string]json.RawMessage
		if err := dec.Decode(&generic); err != nil {
			return nil, fmt.Errorf("decide: bad questions: question %q is not an object", name)
		}
		var typ struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(r, &typ); err != nil {
			return nil, fmt.Errorf("decide: bad questions: question %q is not an object", name)
		}
		_ = qw
		var instr struct {
			Instructions string `json:"instructions"`
		}
		if err := json.Unmarshal(r, &instr); err != nil {
			return nil, fmt.Errorf("decide: bad questions: question %q is not an object", name)
		}
		if strings.TrimSpace(instr.Instructions) == "" {
			return nil, fmt.Errorf("decide: bad questions: question %q has no instructions", name)
		}
		switch typ.Type {
		case "choice":
			var crit map[string]string
			var cw struct {
				Criteria json.RawMessage `json:"criteria"`
			}
			if err := json.Unmarshal(r, &cw); err != nil || len(cw.Criteria) == 0 {
				return nil, fmt.Errorf("decide: bad questions: question %q needs criteria", name)
			}
			if err := json.Unmarshal(cw.Criteria, &crit); err != nil || len(crit) == 0 {
				return nil, fmt.Errorf("decide: bad questions: question %q choice criteria must be a non-empty object", name)
			}
			out[name] = Question{Instructions: instr.Instructions, Choice: crit}
		case "score":
			var levels []string
			var sw struct {
				Criteria json.RawMessage `json:"criteria"`
			}
			if err := json.Unmarshal(r, &sw); err != nil || len(sw.Criteria) == 0 {
				return nil, fmt.Errorf("decide: bad questions: question %q needs criteria", name)
			}
			if err := json.Unmarshal(sw.Criteria, &levels); err != nil || len(levels) == 0 {
				return nil, fmt.Errorf("decide: bad questions: question %q score criteria must be a non-empty list", name)
			}
			out[name] = Question{Instructions: instr.Instructions, Score: levels}
		case "noul":
			out[name] = Question{Instructions: instr.Instructions, Noul: true}
		default:
			return nil, fmt.Errorf("decide: bad questions: question %q has unknown type %q", name, typ.Type)
		}
	}
	return out, nil
}

// Line renders one status line for a decision: the prefix, one
// <name>=<value> conf=<0-1> pair per answer in name order, then the floor and
// the below list naming every answer under it. A decision below the floor is
// a suggestion, never an authorization.
func Line(prefix string, answers map[string]Answer, floor float64) string {
	names := make([]string, 0, len(answers))
	for name := range answers {
		names = append(names, name)
	}
	sort.Strings(names)
	var b strings.Builder
	b.WriteString(oneline.Field(prefix))
	for _, name := range names {
		a := answers[name]
		var value string
		switch a.Type {
		case "score":
			value = fmt.Sprintf("%.2f", a.Score)
		case "noul":
			value = fmt.Sprintf("%.2f", a.Noul)
		case "choice", "":
			value = oneline.Field(a.Choice)
		default:
			value = oneline.Field(a.Choice)
		}
		fmt.Fprintf(&b, " %s=%s conf=%.2f", oneline.Field(name), value, a.Confidence)
	}
	fmt.Fprintf(&b, " floor=%.2f", floor)
	below := make([]string, 0)
	for _, name := range names {
		if answers[name].Confidence < floor {
			below = append(below, oneline.Field(name))
		}
	}
	b.WriteString(" below=")
	if len(below) == 0 {
		b.WriteString("-")
	} else {
		b.WriteString(strings.Join(below, ","))
	}
	return b.String()
}
