package main

import (
	"os"
	"regexp"
	"strings"
	"time"
)

// harnessStartWaits is the wait before each relaunch of a harness whose START failed
// (harnessStartFailed), one entry per retry, in the same job on the same route. The
// owner, 2026-10-01: "maybe: 1-1-1-2-2-4-4-8-8 is better ;)". Nine retries, 31 s in all;
// when they are spent the run hands back as any provider failure, naming the starts.
var harnessStartWaits = []time.Duration{
	1 * time.Second, 1 * time.Second, 1 * time.Second,
	2 * time.Second, 2 * time.Second,
	4 * time.Second, 4 * time.Second,
	8 * time.Second, 8 * time.Second,
}

// harnessStartWindow is how long a harness may run before an exit with no tokens and no
// result stops counting as a start that failed.
const harnessStartWindow = 10 * time.Second

var (
	// startModelRE is the harness's own catalog refusing the model before any request
	// (opencode's printed `error="ProviderModelNotFoundError: Model not found: <model>. Did
	// you mean ..."`, OPENCODE_PRINT_LOGS).
	startModelRE = regexp.MustCompile(`ProviderModelNotFoundError`)
	// startEnvelopeRE is opencode's envelope for a run that died inside its own server
	// (`UnknownError` ... `Unexpected server error. Check server logs for details.`).
	startEnvelopeRE = regexp.MustCompile(`UnknownError[\s\S]{0,200}Unexpected server error`)
	// providerWordsRE is a provider that answered: a status, an API call error, a stream
	// error, a rate limit. Output carrying any of it is a provider failure, never a start.
	providerWordsRE = regexp.MustCompile(`(?i)APICallError|stream error|provider answered|rate.?limit|statusCode|server_error|overloaded|\b(?:http|status)\W{0,3}[45]\d\d\b|\b(?:502|503|529)\b`)
)

// harnessStartFailed is whether one launch never got past the harness's start, and the
// word for why. Narrow, by design: a launch that published no result, spent no tokens and
// ended inside harnessStartWindow, whose output is the harness's catalog refusing the
// model (unknown-model), or opencode's UnknownError envelope with no provider's words
// anywhere in it (unexpected-server-error). A launch that spent a token, wrote a result,
// ran past the window, or carries a provider's status, API error, stream error or rate
// limit is never a start that failed: those keep their own paths (the grace retry, the
// hand-back).
func harnessStartFailed(tail []byte, elapsed time.Duration, tokens int, published bool) (string, bool) {
	if published || tokens > 0 || elapsed >= harnessStartWindow {
		return "", false
	}
	switch {
	case startModelRE.Match(tail):
		return "unknown-model", true
	case startEnvelopeRE.Match(tail) && !providerWordsRE.Match(tail):
		return "unexpected-server-error", true
	}
	return "", false
}

// startSleep is the wait itself: the schedule's, unless NOVA_SWARM_PROVIDER_BACKOFF pins
// every launch wait (the test binary's TestMain pins it to zero, as for the provider
// retry).
func startSleep(d time.Duration) {
	if v := strings.TrimSpace(os.Getenv("NOVA_SWARM_PROVIDER_BACKOFF")); v != "" {
		if pinned, err := time.ParseDuration(v); err == nil && pinned >= 0 {
			d = pinned
		}
	}
	time.Sleep(d)
}
