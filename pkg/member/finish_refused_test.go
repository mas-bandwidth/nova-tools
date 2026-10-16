package member

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A finish the store refuses after its push is sent again, byte for byte, and the pushed
// generation is never run again: the card's finish is the member's to send, and the work is
// already on origin (launch.push: a finish is reported again, never pushed again). Red at
// BASE: recoverWorking ran the card at the same generation and pushed the same branch again.
func TestARefusedFinishIsSentAgainNeverTheRun(t *testing.T) {
	t.Parallel()
	pu := &fakePusher{def: Push{Sha: fullSha}}
	g, _ := pushRig(t, pu, Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: "0123456", Report: "landed the thing"})

	g.s.set("finish", 1, "REFUSED finish: no such column")
	_, err := g.m.Tick(time.Unix(0, 0))
	require.NoError(t, err)
	assert.Equal(t, []string{"c1"}, g.r.started(), "the refused finish never runs the card again")
	assert.Equal(t, []string{"c1"}, pu.cards(), "the pushed generation is never pushed again")
	finish := g.s.lines("finish")
	require.Len(t, finish, 1)
	assert.Contains(t, finish[0], "--head "+fullSha)
	assert.Contains(t, finish[0], "--branch work/c1")
	assert.Contains(t, g.out.String(), "NOTE finish c1 refused: REFUSED finish: no such column")

	// a pass before the retry is due: the finish is not sent again and the card is held
	g.s.set("finish", 0, "")
	_, err = g.m.Tick(time.Unix(30, 0))
	require.NoError(t, err)
	assert.Len(t, g.s.lines("finish"), 1, "not due yet: one finish so far")
	assert.Equal(t, []string{"c1"}, g.r.started(), "no relaunch between the refusals")
	assert.Contains(t, g.out.String(), "recover c1 held: generation 1 pushed "+fullSha+"; its finish is sent again, never the run")

	// past FinishRefusedRetry: the same finish is sent again, byte for byte, and taken
	_, err = g.m.Tick(time.Unix(0, 0).Add(FinishRefusedRetry + 11*time.Second))
	require.NoError(t, err)
	finish = g.s.lines("finish")
	require.Len(t, finish, 2, "the finish is sent again")
	assert.Equal(t, finish[0], finish[1], "the second finish is the first, again")
	assert.Equal(t, []string{"c1"}, g.r.started(), "the run is never repeated")
	assert.Equal(t, []string{"c1"}, pu.cards(), "pushed once across every pass")
}

// The same refusal FinishRefusedMax times hands the card back with stop-return instead of
// running it: its run is refused, so the store must deal it again at the next generation.
func TestTheSameRefusalHandsTheCardBackAfterFinishRefusedMax(t *testing.T) {
	t.Parallel()
	pu := &fakePusher{def: Push{Sha: fullSha}}
	g, _ := pushRig(t, pu, Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: "0123456", Report: "landed"})
	g.s.set("finish", 1, "REFUSED finish: no such column")

	_, err := g.m.Tick(time.Unix(0, 0))
	require.NoError(t, err)
	_, err = g.m.Tick(time.Unix(0, 0).Add(FinishRefusedRetry + time.Second))
	require.NoError(t, err)
	_, err = g.m.Tick(time.Unix(0, 0).Add(4*FinishRefusedRetry + time.Second))
	require.NoError(t, err)

	assert.Equal(t, []string{"stop-return --as m c1@1 --epoch 7 --reason finish refused 3 times: REFUSED finish: no such column"}, g.s.lines("stop-return"))
	assert.Equal(t, []string{"c1"}, g.r.started(), "no second start")
	assert.Len(t, g.s.lines("finish"), 3, "no fourth finish")

	// the memory goes with the hand-back: nothing more is sent
	_, err = g.m.Tick(time.Unix(0, 0).Add(20 * time.Minute))
	require.NoError(t, err)
	assert.Len(t, g.s.lines("finish"), 3)
	assert.Len(t, g.s.lines("stop-return"), 1)
}

// A different refusal on the next send starts the count again at 1: two reasons over three
// refusals never reach FinishRefusedMax, so the card is never handed back yet.
func TestADifferentRefusalStartsTheCountAgain(t *testing.T) {
	t.Parallel()
	pu := &fakePusher{def: Push{Sha: fullSha}}
	g, _ := pushRig(t, pu, Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: "0123456", Report: "landed"})

	g.s.set("finish", 1, "REFUSED finish: reason A")
	_, err := g.m.Tick(time.Unix(0, 0))
	require.NoError(t, err)
	g.s.set("finish", 1, "REFUSED finish: reason B")
	_, err = g.m.Tick(time.Unix(0, 0).Add(FinishRefusedRetry + time.Second))
	require.NoError(t, err)
	_, err = g.m.Tick(time.Unix(0, 0).Add(2*FinishRefusedRetry + time.Second))
	require.NoError(t, err)

	assert.Empty(t, g.s.lines("stop-return"), "two reasons over three refusals: the count is not yet FinishRefusedMax")
	assert.Len(t, g.s.lines("finish"), 3)
	assert.Equal(t, []string{"c1"}, g.r.started())
}

// The memory goes when the queue no longer lists the card at that generation, working on
// this row: absent here, at another generation below. Absent, the finish is not sent again;
// at gen 2 the card is run as a recovered card does today.
func TestTheMemoryGoesWhenTheQueueMovesTheCard(t *testing.T) {
	t.Parallel()
	pu := &fakePusher{def: Push{Sha: fullSha}}
	g, _ := pushRig(t, pu, Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: "0123456", Report: "landed"})
	g.s.set("finish", 1, "REFUSED finish: no such column")
	_, err := g.m.Tick(time.Unix(0, 0))
	require.NoError(t, err)

	// the queue stops listing the card: the memory goes, and past the retry nothing is sent
	g.s.set("finish", 0, "")
	g.s.set("queue", 0, queueJSON(t, 7))
	_, err = g.m.Tick(time.Unix(0, 0).Add(2*FinishRefusedRetry + time.Second))
	require.NoError(t, err)
	assert.Len(t, g.s.lines("finish"), 1, "the memory is gone: the finish is not sent again")
	assert.Equal(t, []string{"c1"}, g.r.started())

	// the queue lists it at gen 2, working on this row: it runs as a recovered card does
	p2 := pk("c1")
	p2.Gen = 2
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 2, &p2)))
	_, err = g.m.Tick(time.Unix(0, 0).Add(3*FinishRefusedRetry + time.Second))
	require.NoError(t, err)
	assert.Equal(t, []string{"c1", "c1"}, g.r.started(), "the card is recovered and run again at the moved generation")
}

// A refused finish of a run that pushed nothing is held exactly as a pushed one is: the same
// finish is sent again on its backoff, the card is never run again at that generation, and
// after FinishRefusedMax of the same refusal it is handed back once. Red at BASE: the memory
// was kept only for a pushed generation, so recoverWorking ran the card again on the very next
// pass and every run's finish met the same refusal (hetzner1, 2026-10-15: the receipt-contract
// refusal below, one card run 78 times in 22 minutes on a paid route). The store answers that
// refusal with exit 2 (nova-sprint's refuse()), as hetzner1's log shows (finish ... exit=2):
// retryFinishes once read every exit 2 as no answer and sent the finish again every pass,
// never counting it, never handing it back; exit 1 is the other refusal code.
func TestARefusedFinishWithNothingPushedIsHeldNotRunAgain(t *testing.T) {
	t.Parallel()
	const refusal = "nova-sprint receipt-contract REFUSED: invalid raw usage JSON; run: nova-sprint receipt-contract -h"
	for _, code := range []int{2, 1} {
		t.Run(fmt.Sprintf("exit%d", code), func(t *testing.T) {
			t.Parallel()
			pu := &fakePusher{def: Push{Sha: fullSha}}
			g, _ := pushRig(t, pu, Result{Ran: false, Report: "native refused: pool has no identity row in identity.tsv"})
			g.s.set("finish", code, refusal)

			// the member's every=3s, for a minute: one run, one finish, the card held
			for i := 0; i < 20; i++ {
				_, err := g.m.Tick(time.Unix(int64(3*i), 0))
				require.NoError(t, err)
			}
			assert.Empty(t, pu.cards(), "nothing was pushed")
			assert.Equal(t, []string{"c1"}, g.r.started(), "the refused finish never runs the card again")
			assert.Len(t, g.s.lines("finish"), 1, "not due yet: one finish in the first minute")
			assert.Contains(t, g.out.String(), "NOTE finish c1 refused: "+refusal)
			assert.Contains(t, g.out.String(), "recover c1 held: generation 1 pushed nothing; its finish is sent again, never the run")

			// every 3s for 20 minutes: the backoff (1m, then 2m) sends the same finish twice
			// more, then the one hand-back, and nothing after it
			for i := 20; i < 400; i++ {
				_, err := g.m.Tick(time.Unix(int64(3*i), 0))
				require.NoError(t, err)
			}
			finish := g.s.lines("finish")
			require.Len(t, finish, FinishRefusedMax, "the same refusal is counted, never read as no answer")
			assert.Equal(t, finish[0], finish[FinishRefusedMax-1], "the finish is sent again byte for byte")
			assert.Equal(t, []string{"stop-return --as m c1@1 --epoch 7 --reason finish refused 3 times: " + refusal}, g.s.lines("stop-return"))
			assert.Equal(t, []string{"c1"}, g.r.started(), "one run across every pass")
		})
	}
}
