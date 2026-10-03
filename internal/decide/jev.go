package decide

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// The Jev backend (SPEC-NOVA-DECIDE section 3): TypeSafe's System One model,
// asked by one POST of {state, model, questions} and answering
// {answers: {<name>: {type, choice, probabilities, confidence} | {type, noul}},
// usage: {input_tokens, output_tokens}}.
const (
	JevURL    = "https://api.typesafe.ai/v1/systemone"
	JevModel  = "jev-latest"
	JevSecret = "JEV_API_KEY" // the variable `nova-secrets exec --only JEV_API_KEY` sets
)

// Send carries one request body to the backend and returns the response body.
// It is the transport: the decision is made above it, and a test injects a fake.
type Send func(ctx context.Context, body []byte) ([]byte, error)

// Jev is the Jev backend over an injected Send.
type Jev struct {
	Model string
	Send  Send
}

// Name is the backend as the record names it.
func (j Jev) Name() string { return "jev:" + j.Model }

// Ask encodes the request, sends it, and decodes the answers.
func (j Jev) Ask(ctx context.Context, s Schema, state string) (map[string]Answer, Usage, error) {
	body, err := json.Marshal(map[string]any{"state": state, "model": j.Model, "questions": s.Questions})
	if err != nil {
		return nil, Usage{}, fmt.Errorf("encoding the request: %w", err)
	}
	raw, err := j.Send(ctx, body)
	if err != nil {
		return nil, Usage{}, err
	}
	return jevAnswers(raw)
}

// jevAnswers decodes a Jev response. A choice with no probabilities carries
// its confidence as the chosen option's probability; an unknown answer type is
// an error naming it.
func jevAnswers(raw []byte) (map[string]Answer, Usage, error) {
	var wire struct {
		Answers map[string]struct {
			Type          string             `json:"type"`
			Choice        string             `json:"choice"`
			Probabilities map[string]float64 `json:"probabilities"`
			Confidence    float64            `json:"confidence"`
			Noul          float64            `json:"noul"`
		} `json:"answers"`
		Usage Usage `json:"usage"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, Usage{}, fmt.Errorf("the backend's response is not the answers shape: %w (it began %q)", err, head(raw))
	}
	out := map[string]Answer{}
	for name, a := range wire.Answers {
		switch a.Type {
		case Noul:
			out[name] = noulAnswer(a.Noul)
		case Choice:
			p := a.Probabilities
			if len(p) == 0 {
				p = map[string]float64{a.Choice: a.Confidence}
			}
			out[name] = Answer{Type: Choice, Value: a.Choice, P: p}
		default:
			return nil, wire.Usage, fmt.Errorf("the backend answered %s with type %q; it wants choice or noul", name, a.Type)
		}
	}
	return out, wire.Usage, nil
}

// HTTPSend is the real transport: one POST to url with the key as a bearer
// token. The key travels on the wire only; an error names the status and the
// head of the body, never the key.
func HTTPSend(client *http.Client, url, key string) Send {
	return func(ctx context.Context, body []byte) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("building the request: %w", err)
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+key)
		resp, err := client.Do(req)
		if err != nil {
			return nil, fmt.Errorf("the backend did not answer: %w", err)
		}
		defer resp.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return nil, fmt.Errorf("reading the backend's answer: %w", err)
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("the backend answered HTTP %d: %q", resp.StatusCode, head(raw))
		}
		return raw, nil
	}
}

// head is the start of a body, for an error a reader can act on.
func head(raw []byte) string {
	s := strings.TrimSpace(string(raw))
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}
