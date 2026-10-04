package main

import (
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStartretryCoverStartSleepHonorsThePin reaches startSleep itself, the one function in
// startretry.go no unit test called: every other test drives a failed start through
// nativeRunConfig.startSleep, its own recording seam, so the real wait sat at 0.0% in the
// per-function coverage table. TestMain pins NOVA_SWARM_PROVIDER_BACKOFF to 0s once for the
// process, so each call here returns at once and the schedule's waits are never slept; the
// precondition is required so a run without the pin fails instead of waiting.
func TestStartretryCoverStartSleepHonorsThePin(t *testing.T) {
	t.Parallel()
	require.NotEmpty(t, os.Getenv("NOVA_SWARM_PROVIDER_BACKOFF"),
		"TestMain pins the launch wait to 0s; without the pin startSleep would sleep the schedule")
	cases := []struct {
		name string
		d    time.Duration
	}{
		{name: "a schedule entry", d: time.Second},
		{name: "the whole schedule", d: 31 * time.Second},
		{name: "the start window", d: harnessStartWindow},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			startSleep(tc.d)
		})
	}
}

// TestStartretryCoverStartFailureRefusals is the refusal side of the file's classifier: a
// launch that spent a token, wrote a result, ran past the window, or whose tail carries a
// provider's own words or nothing at all is never a start that failed, so
// harnessStartFailed refuses it with an empty cause and failed=false.
func TestStartretryCoverStartFailureRefusals(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		tail      string
		ran       time.Duration
		tokens    int
		published bool
	}{
		{name: "a token spent", tail: startRefusal, ran: 3 * time.Second, tokens: 1},
		{name: "a result published", tail: startRefusal, ran: 3 * time.Second, published: true},
		{name: "past the window", tail: startRefusal, ran: harnessStartWindow},
		{name: "a provider answered", tail: "rate limit 429\n", ran: 2 * time.Second},
		{name: "a quiet exit", tail: "nothing to say\n", ran: 2 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cause, failed := harnessStartFailed([]byte(tc.tail), tc.ran, tc.tokens, tc.published)
			assert.False(t, failed, "not a start that failed")
			assert.Empty(t, cause, "a refused launch names no cause")
		})
	}
}
