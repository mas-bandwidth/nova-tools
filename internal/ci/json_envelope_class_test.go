package ci

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// json_envelope_class_test.go holds the standard's output structure rule
// (docs/STANDARD.md, section 2): every verb accepts --json and prints one object
// with result.status and result.exit matching the exit.

// jsonEnvelopeOutput represents the one-object JSON envelope.
type jsonEnvelopeOutput struct {
	Result jsonEnvelopeResult `json:"result"`
}

type jsonEnvelopeResult struct {
	Verb    string   `json:"verb"`
	Status  string   `json:"status"`
	Exit    int      `json:"exit"`
	Remedy string   `json:"remedy"`
	Why     []string `json:"why"`
}

func TestJsonEnvelopeOutputEnvelope(t *testing.T) {
	t.Parallel()
	// Test that a valid JSON envelope parses correctly
	jsonStr := `{"result":{"verb":"test","status":"ok","exit":0,"remedy":"","why":[]}}`
	var env jsonEnvelopeOutput
	require.NoError(t, json.Unmarshal([]byte(jsonStr), &env))
	assert.Equal(t, "test", env.Result.Verb)
	assert.Equal(t, "ok", env.Result.Status)
	assert.Equal(t, 0, env.Result.Exit)
}

func TestJsonEnvelopeRefusalJSON(t *testing.T) {
	t.Parallel()
	// Test that a refusal JSON has the expected structure
	jsonStr := `{"result":{"verb":"put","status":"refused","exit":2,"remedy":"nova-put -h","why":["unknown flag"]}}`
	var env jsonEnvelopeOutput
	require.NoError(t, json.Unmarshal([]byte(jsonStr), &env))
	assert.Equal(t, "put", env.Result.Verb)
	assert.Equal(t, "refused", env.Result.Status)
	assert.Equal(t, 2, env.Result.Exit)
}

func TestJsonEnvelopeFailedJSON(t *testing.T) {
	t.Parallel()
	// Test that a failed JSON has the expected structure
	jsonStr := `{"result":{"verb":"deploy","status":"failed","exit":1,"remedy":"check logs","why":["connection refused"]}}`
	var env jsonEnvelopeOutput
	require.NoError(t, json.Unmarshal([]byte(jsonStr), &env))
	assert.Equal(t, "deploy", env.Result.Verb)
	assert.Equal(t, "failed", env.Result.Status)
	assert.Equal(t, 1, env.Result.Exit)
}
