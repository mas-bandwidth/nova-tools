package swarm

import "testing"

// DOGFOOD D5 (2026-09-11, HIGH): a job that FINISHED -- exit.json rc=0, end=done, a clean
// RESULT.md beside it -- was filed in failed/ with rc=429.
//
// `finish` reads the harness log for a provider's rate limit because POSIX truncates 429 to
// 173, and it rewrote the job's outcome from what the log CONTAINED rather than from how
// the job ENDED. A harness that was rate-limited mid-run, backed off, retried itself and
// then answered leaves "429" in its log and a zero exit status: the completion evidence the
// supervisor wrote is the outcome, and the log line is history.
//
// The 429 outcome is for a job whose harness did NOT finish: a non-zero status, or no
// completion evidence at all.
func TestADoneJobIsNotFiledAsRateLimited(t *testing.T) {
	for _, c := range []struct {
		name      string
		inLog     bool
		end       string
		rc        int
		wantIs429 bool
	}{
		{"the harness was limited, backed off and then answered", true, EndDone, 0, false},
		{"the harness died on the rate limit", true, EndFailed, 1, true},
		{"the harness left no completion evidence at all", true, EndUnknown, -1, true},
		{"a clean job whose log never said it", false, EndDone, 0, false},
		{"a failed job whose log never said it", false, EndFailed, 1, false},
	} {
		if got := rateLimitedOutcome(c.inLog, c.end, c.rc); got != c.wantIs429 {
			t.Errorf("%s: the outcome is a rate limit=%t, want %t", c.name, got, c.wantIs429)
		}
	}
}
