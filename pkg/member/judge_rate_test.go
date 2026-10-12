package member

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestJudgeRateLimitedProviderEndIsTheProvidersWhateverTheShape pins the rate-limit
// rule: a provider end naming a rate limit (class=rate-limited, or a status=429 whose
// class is not out-of-credit) with no pushed head is FinishFailed with the provider
// kind, whatever the shape or verdict, and never the card's failed work; a
// rate-limited end whose head was pushed is finished work.
// (docs/SPEC-CARD-CONTRACT.md section 4, provider failure; tla/CardContract.tla, JudgeOf)
func TestJudgeRateLimitedProviderEndIsTheProvidersWhateverTheShape(t *testing.T) {
	t.Parallel()

	// a shaped result with a rate-limited provider end and no pushed head is the
	// provider's, not the card's.
	provider := "provider: class=rate-limited status=429 msg=Too many requests"
	fin, why := Judge(Result{Shaped: true, Verdict: "nothing", End: EndProvider, Provider: provider}, Push{None: "no commit"})
	require.Equal(t, FinishFailed, fin)
	require.Equal(t, EndProvider+": "+provider, why)

	// status=429 under a class that is not out-of-credit is the same.
	other := "provider: class=other status=429 msg=Too many requests"
	fin, why = Judge(Result{Shaped: true, Verdict: "not-done", End: EndProvider, Provider: other}, Push{None: "no commit"})
	require.Equal(t, FinishFailed, fin)
	require.Equal(t, EndProvider+": "+other, why)

	// shaped, verdict ok, with a pushed head is finished work.
	fin, why = Judge(Result{Shaped: true, Verdict: "ok", End: EndProvider, Provider: provider}, Push{Sha: fullSha})
	require.Equal(t, FinishOK, fin)
	require.Empty(t, why)

	// a 429 whose class is out-of-credit stays the card's, as today.
	fin, why = Judge(Result{Shaped: true, Verdict: "not-done", End: EndProvider, Provider: "provider: class=out-of-credit status=429 msg=Insufficient credits"}, Push{None: "no commit"})
	require.Equal(t, FinishFailed, fin)
	require.Equal(t, "verdict not-done", why)
}

// TestARateLimitedProviderRunIsFinishedFailedWithTheProvidersKind runs the same rule
// through the member: a child that wrote a shaped not-done RESULT.md, ended on the
// provider's rate limit and pushed no head is reported --failed with the provider's
// kind, so the sprint deals the card again rather than judging it.
// (docs/SPEC-CARD-CONTRACT.md section 4, the finish; tla/CardContract.tla, JudgeOf)
func TestARateLimitedProviderRunIsFinishedFailedWithTheProvidersKind(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "m", Width: 2})
	p := pk("c1")
	p.Gen = 2
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 2, &p)))
	_, err := g.tick(t) // restart: the child is ours now
	require.NoError(t, err)
	g.r.child("c1").end(Result{
		Shaped:   true,
		Verdict:  "not-done",
		End:      EndProvider,
		Provider: "provider: class=rate-limited status=429 msg=Too many requests",
		Report:   "the provider rate-limited the run before it finished",
	})
	g.s.reset()
	_, err = g.tick(t)
	require.NoError(t, err)
	lines := g.s.lines("finish")
	require.Len(t, lines, 1)
	want := "finish --as m c1@2 --report provider failure: provider: class=rate-limited status=429 msg=Too many requests; the provider rate-limited the run before it finished --failed --epoch 7"
	require.Equal(t, want, lines[0])
	require.True(t, strings.HasPrefix(lines[0], "finish --as m c1@2 --report provider failure: provider: class=rate-limited"), "a rate-limited run is reported with the provider's kind: %q", lines[0])
}
