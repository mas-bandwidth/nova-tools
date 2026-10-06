package sprint

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The push proof's rules: a check is due when none was delivered, a failure
// PushRetry ago, an answer past its bound, or a proof PushProofEvery old; a
// pong counts only for the last check delivered, once; the seat is live only
// inside PushProofLive and while the last delivery did not fail.
func TestThePushProofIsLiveOnlyAfterThePongOfTheLastCheck(t *testing.T) {
	t.Parallel()
	t0 := time.Date(2026, 10, 5, 18, 0, 0, 0, time.UTC)
	rec := PushRecord{Name: "rowan", Harness: "opencode", Target: "/w"}
	assert.True(t, PushDue(rec, t0), "nothing delivered yet")
	assert.False(t, PushDue(PushRecord{Name: "rowan"}, t0), "no harness: nothing to deliver through")
	assert.Contains(t, PushDown("rowan", rec, false, t0), "PUSH DOWN: rowan has no push target recorded")
	assert.Contains(t, PushDown("rowan", rec, true, t0), "no push check has been delivered into rowan's opencode session yet")

	_, why := PushPong(rec, true, "n1", t0)
	assert.Contains(t, why, "nothing to answer")
	rec = PushSent(rec, "n1", "", t0)
	assert.False(t, PushDue(rec, t0.Add(PushAnswerBound-time.Second)), "the check waits its answer bound")
	assert.True(t, PushDue(rec, t0.Add(PushAnswerBound)), "an unanswered check is asked again")
	assert.Contains(t, PushWhy("rowan", rec, true, t0), "the push check went into rowan's opencode session and no pong")
	assert.NotContains(t, PushWhy("rowan", rec, true, t0), "n1")
	_, why = PushPong(rec, true, "n0", t0)
	assert.Contains(t, why, "only the session's answer to the last check counts")

	at := t0.Add(time.Minute)
	rec, why = PushPong(rec, true, "n1", at)
	require.Empty(t, why)
	assert.True(t, PushLive(rec, true, at.Add(PushProofLive)))
	assert.Empty(t, PushDown("rowan", rec, true, at))
	_, why = PushPong(rec, true, "n1", at)
	assert.Contains(t, why, "counted already")
	assert.False(t, PushDue(rec, at.Add(PushProofEvery-time.Second)))
	assert.True(t, PushDue(rec, at.Add(PushProofEvery)), "re-proven every PushProofEvery")
	assert.False(t, PushLive(rec, true, at.Add(PushProofLive+time.Second)))
	assert.Contains(t, PushWhy("rowan", rec, true, at.Add(PushProofLive+time.Second)), "rowan's last pong is 15m1s old")

	failed := PushSent(rec, "n2", "opencode's deliver command exited 1", at.Add(PushProofEvery))
	assert.False(t, PushLive(failed, true, at.Add(PushProofEvery)), "a failed delivery is down at once")
	assert.Contains(t, PushWhy("rowan", failed, true, at.Add(PushProofEvery)), "failed: opencode's deliver command exited 1")
	assert.False(t, PushDue(failed, at.Add(PushProofEvery+PushRetry-time.Second)))
	assert.True(t, PushDue(failed, at.Add(PushProofEvery+PushRetry)))
	_, why = PushPong(failed, true, "n2", at.Add(PushProofEvery))
	assert.Contains(t, why, "a pong to it is no proof")

	assert.Contains(t, PushCheckText("rowan", "n3"), PushCheckPrefix+"n3\n")
	assert.Contains(t, PushCheckText("rowan", "n3"), "nova-sprint seat pong n3 --actor rowan")
	assert.Equal(t, "nova-sprint seat install --actor rowan --harness opencode --target /w", PushSetup("rowan", rec, true))
	assert.Contains(t, NotPushTarget(PushRecord{Name: "rowan", Target: "/w"}), "--harness <name> is required")
	assert.Contains(t, NotPushTarget(PushRecord{Name: "rowan", Harness: "codex"}), "--target <dir> is required")
	assert.Contains(t, NotPushTarget(PushRecord{Harness: "codex", Target: "/w"}), "wants --actor <name>")
}

// The seat check says PUSH DOWN with the setup while the seat has no live proof,
// up with the proof's age when it has one, and nothing when it was not measured.
func TestTheSeatCheckSaysPushDownWithTheRemedy(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 5, 18, 0, 0, 0, time.UTC)
	line := func(p PushM) (SeatCheckLine, bool) {
		for _, l := range JudgeSeatCheck(SeatCheckMeasures{Push: p, Errs: map[string]string{}}, now).Lines {
			if l.Thing == SeatCheckPush {
				return l, true
			}
		}
		return SeatCheckLine{}, false
	}
	_, ok := line(PushM{})
	assert.False(t, ok, "an unmeasured push says no line")
	l, ok := line(PushM{Measured: true, Holder: "rowan"})
	require.True(t, ok)
	assert.Equal(t, `MACHINERY push DOWN holder=rowan harness=- adapter=- proven=- why="rowan has no push target recorded: the push loop cannot reach the session" remedy="nova-sprint seat install --actor rowan --harness <harness> --target <session dir>"`, l.String())
	rec := PushRecord{Name: "rowan", Harness: "opencode", Target: "/w", Nonce: "n1", Sent: now.Add(-2 * time.Minute), Proven: now.Add(-time.Minute), PongOf: "n1"}
	l, _ = line(PushM{Measured: true, Holder: "rowan", Record: rec, Recorded: true})
	assert.Equal(t, "MACHINERY push OK holder=rowan harness=opencode adapter=opencode proven=1m0s ago", l.String())
}

// A seat on the folder adapter is refused with the setup and the two commands
// the session runs, the Monitor on its folder and the answer to the last check
// written there; the seat check names the adapter.
func TestAFolderSeatIsRefusedWithItsTwoCommands(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 6, 13, 0, 0, 0, time.UTC)
	rec := PushRecord{Name: "rowan", Harness: "claude", Adapter: AdapterFolder, Target: "/w/rowan's inbox"}
	watch := "nova-sprint seat watch '/w/rowan'\\''s inbox'"
	assert.Equal(t, watch, FolderWatch(rec.Target))
	assert.Equal(t, "PUSH DOWN: no push check has been delivered into rowan's claude session yet: is inbox --wait --push seat running?; a coordinator that cannot be reached is not a coordinator, and nothing was changed; run: nova-sprint seat install --actor rowan --harness claude --target /w/rowan's inbox; then, from inside the session, watch the folder with a Monitor: "+watch+" ; and answer the PROOF-<nonce> file it shows: nova-sprint seat pong <nonce> --actor rowan", PushDown("rowan", rec, true, now))
	down := JudgeSeatCheck(SeatCheckMeasures{Push: PushM{Measured: true, Holder: "rowan", Record: rec, Recorded: true}, Errs: map[string]string{}}, now)
	var downLine string
	for _, x := range down.Lines {
		if x.Thing == SeatCheckPush {
			downLine = x.String()
		}
	}
	assert.Contains(t, downLine, "proven=-")
	assert.Contains(t, downLine, watch)
	assert.Contains(t, downLine, "nova-sprint seat pong <nonce> --actor rowan")
	rec = PushSent(rec, "n1", "", now)
	assert.Contains(t, PushDown("rowan", rec, true, now), "answer the PROOF-<nonce> file it shows: nova-sprint seat pong <nonce> --actor rowan")
	assert.NotContains(t, PushDown("rowan", rec, true, now), "n1")
	assert.NotContains(t, PushWhy("rowan", rec, true, now), "n1")
	assert.NotContains(t, JudgeSeatCheck(SeatCheckMeasures{Push: PushM{Measured: true, Holder: "rowan", Record: rec, Recorded: true}, Errs: map[string]string{}}, now).JSON(), "n1")
	rec, why := PushPong(rec, true, "n1", now)
	require.Empty(t, why)
	assert.Empty(t, PushDown("rowan", rec, true, now), "proven")
	assert.Equal(t, "folder", rec.AdapterName())
	assert.Equal(t, "claude", PushRecord{Harness: "claude"}.AdapterName())
	assert.Equal(t, "-", PushRecord{}.AdapterName())
	l := JudgeSeatCheck(SeatCheckMeasures{Push: PushM{Measured: true, Holder: "rowan", Record: rec, Recorded: true}, Errs: map[string]string{}}, now.Add(time.Minute)).Lines
	var line string
	for _, x := range l {
		if x.Thing == SeatCheckPush {
			line = x.String()
		}
	}
	assert.Equal(t, "MACHINERY push OK holder=rowan harness=claude adapter=folder proven=1m0s ago", line)
}
