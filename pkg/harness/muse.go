package harness

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strings"
)

// MuseKeyEnv is the member's seat-secret name. It is consumed on stdin,
// removed from the harness environment, and never passed to tool children.
const MuseKeyEnv = "META_API_KEY"

// MuseArgv runs the route's model against the card's prompt file.
func MuseArgv(bin, model, promptFile string) []string {
	return []string{bin, "exec", "--prompt-file", promptFile, "--json", "--model", model, "--api-key-stdin"}
}

// MuseInput extracts the provider key while stripping it from the child environment.
func MuseInput(env []string) (key string, child []string) {
	for _, entry := range env {
		if value, ok := strings.CutPrefix(entry, MuseKeyEnv+"="); ok {
			key = value
		} else {
			child = append(child, entry)
		}
	}
	return key, child
}

// MuseResult is the terminal answer and summed provider usage, with absent
// classes left absent. Failure is the terminal refusal for the existing classifier.
type MuseResult struct {
	Text    string
	Model   string
	Usage   map[string]int64
	Failure string
	Turns   int
}

// ReadMuse reads the version-one JSONL envelopes. Model calls, not goal
// attribution mirrors, own the usage, so each reported call is counted once.
func ReadMuse(out []byte) (MuseResult, error) {
	result := MuseResult{Usage: map[string]int64{}}
	for _, line := range bytes.Split(out, []byte("\n")) {
		line = bytes.TrimSpace(line)
		if len(line) == 0 || line[0] != '{' {
			continue
		}
		var envelope struct {
			SchemaVersion int             `json:"schema_version"`
			PayloadType   string          `json:"payload_type"`
			Payload       json.RawMessage `json:"payload"`
		}
		if err := json.Unmarshal(line, &envelope); err != nil {
			return result, fmt.Errorf("muse JSONL: %w", err)
		}
		if envelope.SchemaVersion > 1 {
			return result, fmt.Errorf("muse JSONL schema %d is unsupported; want version 1", envelope.SchemaVersion)
		}
		var event struct {
			Kind     string            `json:"kind"`
			Model    string            `json:"model"`
			Text     string            `json:"text"`
			Reason   *string           `json:"reason"`
			Terminal string            `json:"terminal"`
			Usage    map[string]*int64 `json:"usage"`
			Event    json.RawMessage   `json:"event"`
		}
		if len(envelope.Payload) == 0 {
			continue
		}
		if err := json.Unmarshal(envelope.Payload, &event); err != nil {
			return result, fmt.Errorf("muse event: %w", err)
		}
		if envelope.PayloadType == "runtime.session" && len(event.Event) > 0 {
			if err := json.Unmarshal(event.Event, &event); err != nil {
				return result, fmt.Errorf("muse run event: %w", err)
			}
		}
		switch event.Kind {
		case "model_completed":
			result.Turns++
			result.Model = event.Model
			for _, source := range []string{"input_tokens", "output_tokens", "cached_tokens", "cache_write_tokens", "cache_read_tokens", "reasoning_tokens"} {
				column := source
				n := event.Usage[source]
				if n == nil {
					continue
				}
				if *n < 0 || result.Usage[column] > math.MaxInt64-*n {
					return result, fmt.Errorf("muse usage column %s wants a non-negative count within int64", column)
				}
				result.Usage[column] += *n
			}
		case "run_terminal", "terminal":
			result.Text = event.Text
			result.Failure = ""
			if event.Terminal != "completed" {
				if event.Reason != nil {
					result.Failure = strings.TrimSpace(*event.Reason)
				}
				if result.Failure == "" {
					result.Failure = strings.TrimSpace(event.Text)
				}
				if result.Failure == "" {
					result.Failure = strings.TrimSpace(event.Terminal)
				}
				if result.Failure == "" {
					result.Failure = "run failed"
				}
			}
		}
	}
	return result, nil
}
