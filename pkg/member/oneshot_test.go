package member

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A one-shot friend row is dealt and finishes through the ordinary member
// path (the card, TEST: "a one-shot friend row is dealt and finishes through
// the member path"): the member runs --as friend.<name>, the server's take
// hands her row's card, and the finish goes back under her name, so the work
// is credited to the friend's identity from the server claim (Must hold 4),
// with no friend daemon, lane or link anywhere in the loop.
func TestAOneShotFriendRowIsDealtAndFinishesThroughTheMemberPath(t *testing.T) {
	t.Parallel()
	g := newWidthRig(1, Config{As: "friend.fay"})
	g.m.pusher = &fakePusher{def: Push{Sha: fullSha}}
	p := Packet{Card: "c1", Kind: "work", As: "friend.fay", Primary: "p-c1", Stream: "a", Attempt: 1, Gen: 1, Epoch: 7, Branch: "work/c1"}
	g.s.set("queue", 0, queueJSON(t, 7, ready("c1")))
	g.s.set("take", 0, takeJSON(t, p))
	_, err := g.tick(t)
	require.NoError(t, err)
	require.Equal(t, []string{"take --as friend.fay --limit 1 --json --epoch 7"}, g.s.lines("take"), "the one-shot row is taken under the friend's name")
	require.Equal(t, []string{"c1"}, g.r.started(), "the taken card is started by the member runner")

	g.r.child("c1").end(Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: "abc123", Report: "# Result\n\nlanded the thing"})
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 1, &p)))
	g.s.reset()
	_, err = g.tick(t)
	require.NoError(t, err)
	fin := g.s.lines("finish")
	require.Len(t, fin, 1, "finish lines: %v", fin)
	assert.True(t, strings.HasPrefix(fin[0], "finish --as friend.fay c1@1 "), "the finish is credited to the friend's claim, got %q", fin[0])
	assert.Equal(t, 0, g.m.Running(), "nothing left running after the finish")
}

// A one-shot friend has no presence state (the card, TEST: "no presence state
// exists for a one-shot"): a full member cycle for her row speaks only the
// sprint's fleet verbs (beat, queue, take, finish) and never writes or reads
// a friend presence, wake or pong record — there is no daemon to keep one.
func TestAOneShotMemberCycleCreatesNoPresenceState(t *testing.T) {
	t.Parallel()
	g := newWidthRig(1, Config{As: "friend.fay"})
	g.m.pusher = &fakePusher{def: Push{Sha: fullSha}}
	p := Packet{Card: "c1", Kind: "work", As: "friend.fay", Primary: "p-c1", Stream: "a", Attempt: 1, Gen: 1, Epoch: 7, Branch: "work/c1"}
	g.s.set("queue", 0, queueJSON(t, 7, ready("c1")))
	g.s.set("take", 0, takeJSON(t, p))
	_, err := g.tick(t)
	require.NoError(t, err)
	g.r.child("c1").end(Result{Ran: true, OK: true, Shaped: true, Verdict: "ok", Head: "abc123", Report: "# Result\n\nlanded the thing"})
	g.s.set("queue", 0, queueJSON(t, 7, working("c1", 1, &p)))
	_, err = g.tick(t)
	require.NoError(t, err)
	require.NotEmpty(t, g.s.lines("finish"), "the cycle ran to a finish, so the absence below is not vacuous")

	g.s.mu.Lock()
	defer g.s.mu.Unlock()
	require.NotEmpty(t, g.s.calls, "the cycle spoke to the sprint server")
	for _, c := range g.s.calls {
		line := strings.Join(c, " ")
		for _, banned := range []string{"presence", "wakepath", "wake", "pong", "friend:friend.fay:beat"} {
			assert.NotContains(t, line, banned, "a one-shot member cycle touched presence state: %q", line)
		}
	}
}
