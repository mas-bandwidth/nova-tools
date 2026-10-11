package member

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestAProviderRateLimitedRunIsFinishedFailedWithTheProvidersKindAndReason tests that
// a provider end with a rate limit cause (class=rate-limited or status=429 with class!=out-of-credit)
// and no pushed head is finished as failed with the provider kind and reason.
// (docs/SPEC-CARD-CONTRACT.md section 4, the finish; tla/CardContract.tla, JudgeOf)
func TestAProviderRateLimitedRunIsFinishedFailedWithTheProvidersKindAndReason(t *testing.T) {
	t.Parallel()

	// (a) Through the member, like TestAProviderFailedRunIsFinishedFailedWithTheProvidersKindAndReason
	// A child ends with Result{Shaped: true, Verdict: "not-done", End: EndProvider, Provider: "..."}
	// and no pushed head. The finish line it reports is --failed with --report beginning "provider failure:".
	t.Run("through member", func(t *testing.T) {
		t.Parallel()
		g := newRig(Config{As: "m", Width: 2})
		p := pk("c1")
		p.Gen = 2
		g.s.set("queue", 0, queueJSON(t, 7, working("c1", 2, &p)))
		_, err := g.tick(t)
		require.NoError(t, err)
		g.r.child("c1").end(Result{
			Shaped: true,
			Verdict: "not-done",
			End:     EndProvider,
			Provider: "provider: class=rate-limited status=429 msg=Too many requests",
		})
		g.s.reset()
		_, err = g.tick(t)
		require.NoError(t, err)
		lines := g.s.lines("finish")
		require.Equal(t, 1, len(lines))
		require.Contains(t, lines[0], "finish --as m c1@2")
		require.Contains(t, lines[0], "provider failure: provider: class=rate-limited")
		require.Contains(t, lines[0], "--failed")
	})

	// (b) Judge directly
	t.Run("judge directly", func(t *testing.T) {
		t.Parallel()

		// rate-limited verdict nothing
		fin, why := Judge(Result{
			Shaped: true,
			Verdict: "nothing",
			End:     EndProvider,
			Provider: "provider: class=rate-limited status=429 msg=Too many requests",
		}, Push{None: "nothing"})
		require.Equal(t, FinishFailed, fin)
		require.Contains(t, why, "provider failure:")
		require.Contains(t, why, "class=rate-limited")

		// status=429 under another class (not out-of-credit)
		fin, why = Judge(Result{
			Shaped: true,
			Verdict: "not-done",
			End:     EndProvider,
			Provider: "provider: class=unknown-model status=429 msg=Too many requests",
		}, Push{None: "nothing"})
		require.Equal(t, FinishFailed, fin)
		require.Contains(t, why, "provider failure:")
		require.Contains(t, why, "status=429")

		// Shaped, verdict ok, with a pushed head stays FinishOK
		fin, why = Judge(Result{
			Shaped: true,
			Verdict: "ok",
			End:     EndProvider,
			Provider: "provider: class=rate-limited status=429 msg=Too many requests",
		}, Push{Sha: fullSha})
		require.Equal(t, FinishOK, fin)
		require.Empty(t, why)

		// class=out-of-credit status=429 stays the card's as today
		fin, why = Judge(Result{
			Shaped: true,
			Verdict: "not-done",
			End:     EndProvider,
			Provider: "provider: class=out-of-credit status=429 msg=Quota exceeded",
		}, Push{None: "nothing"})
		require.Equal(t, FinishFailed, fin)
		require.NotContains(t, why, "provider failure:")
		require.Contains(t, why, "verdict not-done")
	})
}
