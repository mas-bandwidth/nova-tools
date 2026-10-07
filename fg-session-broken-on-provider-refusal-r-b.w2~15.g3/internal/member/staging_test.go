package member

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A launch refused at staging, before any child ran, is the member's failure and not the
// card's: it is finished failed with the kind `staging refused` and the stage's own reason,
// never the missing shape's; the sprint deals the card to another member
// (tla/CardContract.tla, StageRefused).
func TestAStagingRefusedLaunchIsFinishedWithTheStagingKindAndReason(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "m", Width: 2})
	p := pk("c1")
	p.Gen = 2
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 2, &p)))
	_, err := g.tick(t)
	require.NoError(t, err)
	g.r.child("c1").end(Result{Report: "no child ran", End: EndStaging, Staging: "no bench mirror for https://example.com/o/quack.git"})
	g.s.reset()
	_, err = g.tick(t)
	require.NoError(t, err)
	require.Equal(t, []string{"finish --as m c1@2 --report staging refused: no bench mirror for https://example.com/o/quack.git; no child ran --failed --epoch 7"}, g.s.lines("finish"))
}

// Judge names a staging refusal by its own kind and reason, never the shape's, and never with
// another end in front of it.
func TestJudgeNamesAStagingRefusalByItsKind(t *testing.T) {
	t.Parallel()
	fin, why := Judge(Result{End: EndStaging, Staging: "stage-timeout"}, Push{None: "nothing"})
	require.Equal(t, FinishFailed, fin)
	require.Equal(t, "staging refused: stage-timeout", why)
}

// A launch native ended because this member's usage source stopped answering (its read of
// the harness's usage database, three reads in a row), which left no result, is the member's
// failure and never the card's (the owner, 2026-10-03: a provider problem is never a card
// problem): it is finished on the staging kind with the budget's words, so the sprint deals
// the card to another member, spends none of its redeal bound and opens no judgment on it.
// Then the member rests: it starts no card for UsageRest from that end, said once with since
// when, and takes again after, said once. On 2026-10-03 at 11:46 PM every child on the flash
// routes ended so together, and each end was judged as its card's.
func TestALaunchTheUsageSourceEndedIsTheMembersFailureAndRestsTheMember(t *testing.T) {
	t.Parallel()
	g := newRig(Config{As: "m", Width: 2})
	p := pk("c1")
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 1, &p)))
	_, err := g.tickAt(t, 100)
	require.NoError(t, err)
	words := "unverifiable: the usage source stopped answering, tokens 12 of 400,000, cost unreported"
	g.r.child("c1").end(Result{Report: "ended by the sampler", End: EndUnverifiable, Budget: words})
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 1, &p), ready("c2")))
	g.s.set("take", 0, takeJSON(t, pk("c2")))
	_, err = g.tickAt(t, 160)
	require.NoError(t, err)
	rest := "the usage source stopped answering since 1970-01-01T00:02:40Z: no card is started until 1970-01-01T00:12:40Z"
	require.Equal(t, []string{
		"finish --as m c1@1 --report staging refused: budget " + words + "; ended by the sampler --failed --epoch 7",
		"finish --as m c2@1 --failed --report staging refused: " + rest + " --epoch 7",
	}, g.s.lines("finish"), "the end is the member's, and the card taken in the same pass is refused")
	require.Equal(t, []string{"c1"}, g.r.started(), "nothing new is started")
	g.s.set("queue", 0, queueJSON(t, 7, ready("c2"))) // c1 is reported
	g.s.reset()
	_, err = g.tickAt(t, 160+int64(UsageRest.Seconds())-1)
	require.NoError(t, err)
	require.Equal(t, 1, strings.Count(g.out.String(), "take REFUSED: "+rest+"\n"), "said once")
	require.Equal(t, []string{"finish --as m c2@1 --failed --report staging refused: " + rest + " --epoch 7"}, g.s.lines("finish"), "still resting a second before the end")
	g.s.reset()
	_, err = g.tickAt(t, 160+int64(UsageRest.Seconds()))
	require.NoError(t, err)
	require.Empty(t, g.s.lines("finish"))
	require.Equal(t, []string{"c1", "c2"}, g.r.started(), "the rest ended: the card is started")
	require.Contains(t, g.out.String(), "NOTE take resumed: the rest after the usage source stopped answering (1970-01-01T00:02:40Z) ended\n")
}

// Judge: a launch the usage source ended with no result is the staging kind with the budget's
// words, and with no end in front of it; one that left a result with the shape is judged as
// the card's own work, said as a budget end (the words the member said before).
func TestJudgeNamesAnUnverifiableEndByTheMemberWhenTheChildLeftNoResult(t *testing.T) {
	t.Parallel()
	words := "unverifiable: the usage source stopped answering, tokens 12 of 400,000, cost unreported"
	fin, why := Judge(Result{End: EndUnverifiable, Budget: words}, Push{None: "the child's result names no commit"})
	require.Equal(t, FinishFailed, fin)
	require.Equal(t, "staging refused: budget "+words, why)
	_, why = Judge(Result{End: EndUnverifiable}, Push{None: "the child's result names no commit"})
	require.Equal(t, "staging refused: budget unverifiable: the usage source stopped answering", why, "native named no words")
	fin, why = Judge(Result{End: EndUnverifiable, Budget: words, Shaped: true, Verdict: "not-done"}, Push{None: "the child's result names no commit"})
	require.Equal(t, FinishFailed, fin)
	require.Equal(t, "budget: "+words+": verdict not-done", why, "a result with the shape is the card's, as any budget end's")
}
