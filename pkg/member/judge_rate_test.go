package member

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestJudgeRateLimitedProviderEndHandlesShapedResults tests that a rate-limited
// provider end with a shaped result (Verdict "nothing" or "not-done") and no
// pushed head is judged as FinishFailed with the provider kind, not as failed work.
// (docs/SPEC-CARD-CONTRACT.md section 4, provider failure; tla/CardContract.tla, JudgeOf)
func TestJudgeRateLimitedProviderEndHandlesShapedResults(t *testing.T) {
	t.Parallel()

	// A provider end with class=rate-limited and no pushed head is FinishFailed
	// whatever the shape or verdict.
	provider := "provider: class=rate-limited status=429 msg=Too many requests"
	fin, why := Judge(Result{
		Shaped:  true,
		Verdict: "nothing",
		End:     EndProvider,
		Provider: provider,
	}, Push{})
	require.Equal(t, FinishFailed, fin)
	require.Contains(t, why, EndProvider+": "+provider)

	// status=429 under another class that is not out-of-credit is the same.
	provider2 := "provider: class=other status=429 msg=Too many requests"
	fin, why = Judge(Result{
		Shaped:  true,
		Verdict: "not-done",
		End:     EndProvider,
		Provider: provider2,
	}, Push{})
	require.Equal(t, FinishFailed, fin)
	require.Contains(t, why, EndProvider+": "+provider2)

	// Shaped, verdict ok, with a pushed head is FinishOK.
	fin, why = Judge(Result{
		Shaped:  true,
		Verdict: "ok",
		End:     EndProvider,
		Provider: "provider: class=rate-limited status=429 msg=Too many requests",
	}, Push{Sha: fullSha})
	require.Equal(t, FinishOK, fin)
	require.Empty(t, why)

	// class=out-of-credit stays the card's as today (handled below as normal verdict).
	fin, why = Judge(Result{
		Shaped:  true,
		Verdict: "not-done",
		End:     EndProvider,
		Provider: "provider: class=out-of-credit status=429 msg=Insufficient credits",
	}, Push{})
	require.Equal(t, FinishFailed, fin)
	require.Equal(t, "verdict not-done", why)
}
