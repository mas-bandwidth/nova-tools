package member

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestJudgeRateLimitedProviderEnd verifies that a provider end with a rate-limited reason
// (class=rate-limited, or status=429 with a class other than out-of-credit) and no pushed
// head is judged as FinishFailed with the provider kind, per docs/SPEC-CARD-CONTRACT.md
// section 4 and tla/CardContract.tla JudgeOf.
func TestJudgeRateLimitedProviderEnd(t *testing.T) {
	t.Parallel()

	for name, c := range map[string]struct {
		r    Result
		pu   Push
		want Finish
		why  string
	}{
		// rate-limited provider end with no push: provider finish
		"rate-limited, verdict not-done, no push": {
			Result{Shaped: true, Verdict: "not-done", End: EndProvider, Provider: "provider: class=rate-limited status=429 msg=Too many requests"},
			Push{None: "nothing"}, FinishFailed, "provider failure: provider: class=rate-limited status=429 msg=Too many requests",
		},
		"rate-limited, verdict nothing, no push": {
			Result{Shaped: true, Verdict: "nothing", End: EndProvider, Provider: "provider: class=rate-limited status=429 msg=Too many requests"},
			Push{None: "nothing"}, FinishFailed, "provider failure: provider: class=rate-limited status=429 msg=Too many requests",
		},
		"status=429 non out-of-credit, no push": {
			Result{Shaped: true, Verdict: "not-done", End: EndProvider, Provider: "provider: class=other status=429 msg=limit"},
			Push{None: "nothing"}, FinishFailed, "provider failure: provider: class=other status=429 msg=limit",
		},
		// out-of-credit status=429 remains the card's own failure
		"out-of-credit status=429, verdict not-done": {
			Result{Shaped: true, Verdict: "not-done", End: EndProvider, Provider: "provider: class=out-of-credit status=429 msg=spent"},
			Push{None: "nothing"}, FinishFailed, "verdict not-done",
		},
		// shaped ok result with push remains OK
		"shaped ok with push": {
			Result{Shaped: true, Verdict: "ok", End: EndProvider, Provider: "provider: class=rate-limited status=429 msg=Too many requests"},
			Push{Sha: fullSha}, FinishOK, "",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fin, why := Judge(c.r, c.pu)
			require.Equal(t, c.want, fin, name)
			require.Equal(t, c.why, why, name)
		})
	}
}
