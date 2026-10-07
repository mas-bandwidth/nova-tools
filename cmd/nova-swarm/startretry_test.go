package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// the harness's own output when its catalog refused the model (opencode v1.18.20, verbatim
// shape, 2026-10-01): its printed ERROR line, then the UnknownError envelope
const startRefusal = `timestamp=2026-10-01T20:02:53.818Z level=ERROR run=5c22c68d message=failed ref=err_1ad647ee error="ProviderModelNotFoundError: Model not found: openrouter/x-ai/grok-4.7. Did you mean: x-ai/grok-4.20?"
Error: {"name":"UnknownError","data":{"message":"Unexpected server error. Check server logs for details.","ref":"err_1ad647ee"}}`

// TestHarnessStartFailedIsNarrow: only a launch with no result, no tokens, inside the
// window, whose output is the catalog refusal or the bare envelope, is a failed start; a
// provider that answered (a status, an API error, a stream error), a token spent, a
// result written, or a run past the window never is.
func TestHarnessStartFailedIsNarrow(t *testing.T) {
	t.Parallel()
	envelope := `Error: {"name":"UnknownError","data":{"message":"Unexpected server error. Check server logs for details."}}`
	cases := []struct {
		name      string
		tail      string
		elapsed   time.Duration
		tokens    int
		published bool
		cause     string // "" is not a failed start
	}{
		{"the catalog refusal", startRefusal, 3 * time.Second, 0, false, "unknown-model"},
		{"the bare envelope", envelope, 3 * time.Second, 0, false, "unexpected-server-error"},
		{"a provider 503", "Unexpected server error: the provider answered 503; ref=err_x\n" + envelope, 2 * time.Second, 0, false, ""},
		{"an upstream stream error", `level=ERROR message="stream error" error.error="AI_APICallError: Upstream request failed: Endpoint is unavailable."` + "\n" + envelope, 3 * time.Second, 0, false, ""},
		{"tokens spent", startRefusal, 3 * time.Second, 12, false, ""},
		{"a result written", startRefusal, 3 * time.Second, 0, true, ""},
		{"past the window", startRefusal, 11 * time.Second, 0, false, ""},
		{"a quiet zero-token exit", "nothing to say\n", 2 * time.Second, 0, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cause, failed := harnessStartFailed([]byte(c.tail), c.elapsed, c.tokens, c.published)
			assert.Equal(t, c.cause != "", failed)
			assert.Equal(t, c.cause, cause)
		})
	}
}

// TestTheStartScheduleIsTheOwners pins the waits: "maybe: 1-1-1-2-2-4-4-8-8 is better ;)",
// nine retries and 31 s in all.
func TestTheStartScheduleIsTheOwners(t *testing.T) {
	t.Parallel()
	var total time.Duration
	var secs []int
	for _, d := range harnessStartWaits {
		total += d
		secs = append(secs, int(d/time.Second))
	}
	assert.Equal(t, []int{1, 1, 1, 2, 2, 4, 4, 8, 8}, secs)
	assert.Equal(t, 31*time.Second, total)
}
