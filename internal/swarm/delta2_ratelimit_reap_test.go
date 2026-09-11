package swarm

import "testing"

// DELTA READ 2 (2026-09-11), finding 1: THE 429 PATH RE-QUARANTINED A BUDGET KILL.
//
// `finish` reads the harness log for a provider's rate limit (POSIX truncates 429 to 173)
// and, when it finds one, rewrites the job as `rc=429 end=failed`. `EndFailed` is not on
// `violationWord`'s exemption list, so a job the supervisor ended AT ITS BUDGET whose reap
// left a process behind got rule 11's `violation=background` written into its sidecar --
// the exact loss c070fbe closed, arriving by the other road. `Jobs()` skips every sidecar
// with a violation word, so the findings rule 13 promises survive the kill were dropped
// from `TRIAGE BATCH` again.
//
// RULE 13, VERBATIM (SPEC-SWARM.md:232-233): "a job that ends this way keeps the findings
// it appended so far (rule 3)."
//
// The reap's own ends are the supervisor's OWN VERDICT, written from inside the job: a 429
// in the log beside one of them is history, exactly as it is beside a clean exit (D5). The
// two places that ask -- the outcome and the violation word -- ask the same question of one
// list, so the next end word added cannot be exempt in one place and not the other.
func TestARateLimitDoesNotRewriteTheReapsOwnEnd(t *testing.T) {
	for _, c := range []struct {
		name      string
		inLog     bool
		end       string
		rc        int
		wantIs429 bool
	}{
		{"a job ended at its budget with a 429 in the log", true, EndBudget, -1, false},
		{"a job whose usage stopped being readable, 429 in the log", true, EndUnverifiable, -1, false},
		{"a job reaped at its deadline with a 429 in the log", true, EndKilled, -1, false},
		// Unchanged: a harness that died on the rate limit, and one that answered.
		{"the harness died on the rate limit", true, EndFailed, 1, true},
		{"the harness was limited, backed off and then answered", true, EndDone, 0, false},
	} {
		if got := rateLimitedOutcome(c.inLog, c.end, c.rc); got != c.wantIs429 {
			t.Errorf("%s: the outcome is a rate limit=%t, want %t", c.name, got, c.wantIs429)
		}
	}
}

// AND THE CONSEQUENCE: the end the 429 path leaves behind still carries no violation word.
func TestTheRateLimitedEndOfAReapKeepsItsFindings(t *testing.T) {
	for _, end := range []string{EndBudget, EndUnverifiable, EndKilled} {
		got := end
		if rateLimitedOutcome(true, end, -1) {
			got = EndFailed
		}
		if word := violationWord(got, 1); word != "" {
			t.Errorf("a 429 in the log of a job ended %s writes violation=%q; rule 13 keeps its findings", end, word)
		}
	}
}
