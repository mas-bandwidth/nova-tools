// Package decide supplies decision policy and recording above the typed
// provider client in internal/jevclient. Callers needing only provider transport
// can use that smaller package without decision journals or sprint machinery.
package decide

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/mas-bandwidth/nova-tools/internal/jevclient"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

const (
	DefaultBaseURL = jevclient.DefaultBaseURL
	DefaultModel   = jevclient.DefaultModel
	DefaultKeyEnv  = jevclient.DefaultKeyEnv
	FallbackKeyEnv = jevclient.FallbackKeyEnv
)

type Question = jevclient.Question
type Answer = jevclient.Answer
type Usage = jevclient.Usage

// Client applies caller constraints and records accepted provider answers.
// Configure it before sharing it among concurrent callers.
type Client struct {
	provider  *jevclient.Client
	decisions DecisionDriver
	floor     float64
	constrain func(map[string]Answer) (map[string]Answer, error)

	mu               sync.Mutex
	rowSource        string
	rowHasConfidence bool
	recorded         int
	recordErr        error
}

// New opens the shared provider client; the named environment variable holds
// its key. Missing credentials refuse without printing a key.
func New(baseURL, keyEnv string) (*Client, error) {
	provider, err := jevclient.New(baseURL, keyEnv)
	if err != nil {
		return nil, err
	}
	return &Client{provider: provider}, nil
}

// Constrain installs the caller's rules above validated provider answers,
// before recording or returning them. Configure it before concurrent calls.
func (c *Client) Constrain(fn func(map[string]Answer) (map[string]Answer, error)) {
	c.constrain = fn
}

// Decide asks one typed question set, then applies caller constraints and
// records the resulting answers. Usage survives a refusal after the call.
func (c *Client) Decide(ctx context.Context, state string, qs map[string]Question) (map[string]Answer, Usage, error) {
	answers, usage, err := c.provider.Decide(ctx, state, qs)
	if err != nil {
		return nil, usage, err
	}
	if c.constrain != nil {
		answers, err = c.constrain(answers)
		if err != nil {
			return nil, usage, err
		}
	}
	c.record(state, qs, answers)
	return answers, usage, nil
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
	if inner, ok := top["questions"]; ok {
		// The envelope may carry the criteria the question is answered
		// against -- their version, their file, and the state fields the
		// asker computes first -- so that a question and its criteria are
		// ONE versioned pair. Anything else beside it is a refusal that
		// names the key: a misspelled metadata key that fell through to
		// the bare form used to be read as a question.
		for key := range top {
			if key != "questions" && !questionEnvelopeKeys[key] {
				return nil, fmt.Errorf("decide: bad questions: %q stands beside \"questions\" and is not one of comment, criteria_version, criteria_file, state_fields, machinery", key)
			}
		}
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

// questionEnvelopeKeys are the keys a question file may carry BESIDE its
// questions: the criteria those questions are answered against, so the pair is
// versioned together (Glenn, 2026-09-19 -- the criteria go in as input tokens),
// and a comment. Anything else is a refusal that names it.
var questionEnvelopeKeys = map[string]bool{
	"comment":          true,
	"criteria_version": true,
	"criteria_file":    true,
	"state_fields":     true,
	// The rules the question is answered UNDER, so the binding between a
	// question and its machinery lives in the versioned pair rather than in a
	// name match inside a verb.
	"machinery": true,
}
