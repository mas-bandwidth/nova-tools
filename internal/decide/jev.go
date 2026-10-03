package decide

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
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
// its confidence as the chosen option's probability. An answer with no
// probability at all (a noul with no noul, a choice with neither) is an error
// naming the question: a missing number is never read as 0, which would be a
// confident "no" (STANDARD.md section 2, nothing hidden). An unknown answer
// type is an error naming it.
func jevAnswers(raw []byte) (map[string]Answer, Usage, error) {
	var wire struct {
		Answers map[string]struct {
			Type          string             `json:"type"`
			Choice        string             `json:"choice"`
			Probabilities map[string]float64 `json:"probabilities"`
			Confidence    *float64           `json:"confidence"`
			Noul          *float64           `json:"noul"`
		} `json:"answers"`
		Usage Usage `json:"usage"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		return nil, Usage{}, fmt.Errorf("the backend's response is not the answers shape: %w (it began %q)", err, head(raw))
	}
	out := map[string]Answer{}
	var missing []string
	for _, name := range slices.Sorted(maps.Keys(wire.Answers)) {
		a := wire.Answers[name]
		switch {
		case a.Type == Noul && a.Noul != nil:
			out[name] = noulAnswer(*a.Noul)
		case a.Type == Choice && len(a.Probabilities) > 0:
			out[name] = Answer{Type: Choice, Value: a.Choice, P: a.Probabilities}
		case a.Type == Choice && a.Confidence != nil:
			out[name] = Answer{Type: Choice, Value: a.Choice, P: map[string]float64{a.Choice: *a.Confidence}}
		case a.Type == Noul || a.Type == Choice:
			missing = append(missing, name)
		default:
			return nil, wire.Usage, fmt.Errorf("the backend answered %s with type %q; it wants choice or noul", name, a.Type)
		}
	}
	if len(missing) > 0 {
		return nil, wire.Usage, fmt.Errorf("the backend answered %s with no probability; a missing number is never read as 0", strings.Join(missing, ", "))
	}
	return out, wire.Usage, nil
}

// JevHTTP is the Jev backend over the real transport with key: what nova-decide and the
// sprint's decide read ask through.
func JevHTTP(key string) Jev {
	return Jev{Model: JevModel, Send: HTTPSend(http.DefaultClient, JevURL, key)}
}

// HTTPSend is the real transport, and the one function of this package that
// opens a socket (through client): one POST to url with the key as a bearer
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
			if key != "" { // a body that echoes the key never carries it into an error
				raw = bytes.ReplaceAll(raw, []byte(key), []byte("<key>"))
			}
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
