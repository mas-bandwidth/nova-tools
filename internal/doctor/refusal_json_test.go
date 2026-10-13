package doctor

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDoctorRefusalEmitsJSONOnStdout is card doctor-json-refusal-envelope
// (ratings triage 2026-10-13 Part 2, ordinal 18): an invalid --check under
// --json emits the standard pkg/tool JSON refusal envelope on stdout at
// exit 2 with empty stderr; without --json the refusal stays plain text
// on stderr.
func TestDoctorRefusalEmitsJSONOnStdout(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	reg.Register(Check{Name: "a", Dependency: "a-dep", Run: func(context.Context, Env) Result {
		return Result{Status: OK, Evidence: "a evidence"}
	}})

	var out, errb bytes.Buffer
	code := Main(reg, fakeEnv{}, "", []string{"run", "--check", "nope", "--json"}, strings.NewReader(""), &out, &errb)
	assert.Equal(t, 2, code)
	assert.Empty(t, errb.String(), "a --json refusal leaves stderr empty")
	var env struct {
		Result struct {
			Verb   string   `json:"verb"`
			Status string   `json:"status"`
			Exit   int      `json:"exit"`
			Why    []string `json:"why"`
		} `json:"result"`
	}
	require.NoError(t, json.Unmarshal(out.Bytes(), &env), out.String())
	assert.Equal(t, "run", env.Result.Verb)
	assert.Equal(t, "refused", env.Result.Status)
	assert.Equal(t, 2, env.Result.Exit)
	require.NotEmpty(t, env.Result.Why)
	assert.Contains(t, env.Result.Why[0], `no check named "nope"`)

	out.Reset()
	errb.Reset()
	code = Main(reg, fakeEnv{}, "", []string{"run", "--check", "nope"}, strings.NewReader(""), &out, &errb)
	assert.Equal(t, 2, code)
	assert.Empty(t, out.String(), "a plain refusal stays off stdout")
	assert.Contains(t, errb.String(), `no check named "nope"`)
}
