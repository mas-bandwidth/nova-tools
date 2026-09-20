package swarm

import (
	"crypto/rand"
	"math/big"
	"os"
	"regexp"
	"strings"
	"time"
)

// A LAUNCH THAT DIES IN THE FIRST SECONDS ON A PROVIDER 5XX IS NOT A FINISHED TASK (issue
// #900). Nine of forty Muse requests died in under two seconds with
// `Unexpected server error ... ref=err_...`; each had taken a slot and spent its harvest on
// a request the provider never began, and the pool read them as ordinary failures. A death
// inside the launch grace whose tail names a server error is a LAUNCH failure: the same
// task is retried, with jittered backoff, twice, and only then filed -- with `end=provider`
// and the provider's ref -- so the waste is a retry and not a failure.

// DefaultLaunchGrace is how long a harness may run before its exit stops counting as a
// launch failure. A worker description's `launch_grace` overrides it.
const DefaultLaunchGrace = 15 * time.Second

// MaxProviderAttempts is the whole number of launches one task gets: the first, then the
// two retries the grace earns.
const MaxProviderAttempts = 3

// launchFailureRE matches the provider's own words for a server error, case-insensitively:
// OpenCode's `Unexpected server error ... ref=err_...`, the same while it says `internal
// server error`, and the bare statuses 502, 503 and 529 as whole words.
var launchFailureRE = regexp.MustCompile(`(?i)unexpected server error|internal server error|\b(502|503|529)\b`)

// providerRefRE pulls the provider's own reference out of the tail, the one token a person
// can carry to the provider. A tail with a server error and no ref is still a launch
// failure; the ref is then a dash.
var providerRefRE = regexp.MustCompile(`ref=([A-Za-z0-9_.:/-]+)`)

// LaunchGrace is the worker description's own, or the default where it names none.
func LaunchGrace(w Worker) time.Duration {
	if d, err := time.ParseDuration(strings.TrimSpace(w.LaunchGrace)); err == nil && d > 0 {
		return d
	}
	return DefaultLaunchGrace
}

// ProviderLaunchFailure says whether an output tail is a provider server error, and returns
// the provider's ref where the tail carries one. It is the ONE classifier: `run`'s
// supervisor, the dispatcher's finish and the native runner all ask it the same question.
func ProviderLaunchFailure(raw []byte) (ref string, ok bool) {
	pf, ok := ClassifyProviderFailure(raw)
	if !ok {
		return "", false
	}
	return pf.Ref, true
}

// ProviderRetryDelay is the wait before the retry of a launch that just failed: 5-20s
// jittered after the first fast failure, 30-60s after the second. The jitter keeps a fleet
// that died on one provider wobble from retrying in lockstep. NOVA_SWARM_PROVIDER_BACKOFF
// pins the delay for a test.
var ProviderRetryDelay = func(failed int) time.Duration {
	if v := strings.TrimSpace(os.Getenv("NOVA_SWARM_PROVIDER_BACKOFF")); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d >= 0 {
			return d
		}
	}
	lo, hi := 5, 20
	if failed >= 2 {
		lo, hi = 30, 60
	}
	return time.Duration(lo+randBelow(hi-lo+1)) * time.Second
}

// providerLaunchReason is the sentence the supervisor records on exit.json for a launch
// that died on a provider server error. The tail itself is capped on the RUN line by finish.
func providerLaunchReason(ref, tail string) string {
	if ref != "" {
		return "provider launch failure ref=" + ref
	}
	return "provider launch failure"
}

// randBelow is a cryptographic random in [0,n): the jitter need not be unpredictable, but
// the one source this package already trusts is here.
func randBelow(n int) int {
	if n <= 1 {
		return 0
	}
	v, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		return 0
	}
	return int(v.Int64())
}
