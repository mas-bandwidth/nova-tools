package friend

import (
	"context"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSprint is the sprint server as the beat's proof reaches it: the health fence
// (sprint.NotHealth) applied on its own read, the beat's proof read as the health path
// reads it (sprint.ProofOfBeat, sprint.ProofOwed), and her row's status by the friends'
// rule (sprint.FriendStatus). No socket, no store: the rules alone.
type fakeSprint struct {
	holder     string
	generation uint64
	row        sprint.FriendHealth
	writes     int
	refused    []string
	pongs      []time.Time // the --pong each up beat carried
}

// beat is one friend beat from bob carrying p at now (the server's clock): what the
// row holds after, and her status.
func (f *fakeSprint) beat(now time.Time, p Proof) (sprint.FriendHealth, string) {
	var rep sprint.FriendReport
	var pong time.Time
	if p.State == PresenceUp {
		pong = p.Seen
		f.pongs = append(f.pongs, p.Seen)
	} else {
		rep.Until, rep.Reason = p.Until, p.Reason
	}
	if obs, ok := sprint.ProofOfBeat(rep, pong, now, f.generation); ok && sprint.ProofOwed(f.row, obs) {
		req := sprint.HealthReq{Friend: "bob", Who: "bob", Obs: obs, Prev: f.row, Known: true}
		if why := sprint.NotHealth(f.holder, f.generation, now, req); why != "" {
			f.refused = append(f.refused, why)
		} else {
			f.row, f.writes = obs, f.writes+1
		}
	}
	return f.row, sprint.FriendStatus(sprint.FriendPresence{Health: f.row, Generation: f.generation}, now)
}

func (f *fakeSprint) status(now time.Time) string {
	return sprint.FriendStatus(sprint.FriendPresence{Health: f.row, Generation: f.generation}, now)
}

// The finding of 2026-10-06: three friends read down on the sprint server for twenty
// minutes with their sessions answering, because the daemon's beat carried no proof the
// server read and the daemon never recorded one; a hand friend health put them up at
// once. The daemon is the one that proves its session: every time the presence file's
// last_heard moves, the next beat carries it (--pong) and the server records it through
// the health path as her own proof, state up, seen last_heard; when the session stops
// answering past the bound the beat says down with the reason, recorded the same way;
// and the daemon keeps what the server answered (nova-friend check prints it). An
// answering session is never down on the server for more than one beat.
func TestAnAnsweredCheckIsProvedToTheServerByTheDaemon(t *testing.T) {
	t.Parallel()
	r := newPresenceRig(t)
	r.server = &fakeSprint{holder: "ada", generation: 3}
	ctx := context.Background()

	r.step(t, BeatEvery)
	require.Len(t, r.app.got(), 1, "the first step delivers a session check")
	assert.Equal(t, sprint.Down, r.server.status(r.now), "a daemon that started proves nothing about the session")
	assert.Equal(t, sprint.Down, r.server.row.State, "the beat says down, and the server records it as her own word")
	assert.Equal(t, NotYetAnswered, r.server.row.Reason)
	assert.Empty(t, r.server.refused, "the health fence takes the friend's own proof: %v", r.server.refused)

	// the session answers the check: the next beat carries the proof, and the row is up on it
	answered := r.now.Add(BeatEvery)
	r.send(t, r.direct, "bob", PongSubject, PongLine("n1", 0, 0, 4)+"\n")
	r.step(t, BeatEvery)
	up, _ := r.present(t)
	require.True(t, up, "the session's answer brings the daemon's presence up")
	require.NotEmpty(t, r.server.pongs, "the beat carries the session's proof (--pong) to the server")
	assert.Equal(t, answered, r.server.pongs[len(r.server.pongs)-1], "the proof the beat carries is the presence file's last_heard")
	assert.Equal(t, sprint.Up, r.server.row.State, "recorded through the health path as her own proof")
	assert.Equal(t, answered.UTC(), r.server.row.Seen, "seen is last_heard")
	assert.Equal(t, uint64(3), r.server.row.Generation, "under the seat's generation of the server's own read")
	assert.Equal(t, sprint.Up, r.server.status(r.now), "her row is up on it, within one beat of the answer")
	assert.Empty(t, r.server.refused, "%v", r.server.refused)
	assert.Equal(t, PresenceUp, r.saved.ProofState, "the presence file keeps the proof the server recorded")
	assert.Equal(t, answered.UTC(), r.saved.ProofSeen)
	assert.Equal(t, sprint.Up, r.saved.ServerStatus, "and the server's view of her row")
	assert.Equal(t, r.now, r.saved.ProofAt)

	// a session that talks on the bus moves last_heard: each move is proved once, and a
	// beat with nothing new writes nothing
	writes := r.server.writes
	r.send(t, r.direct, "bob", "status", "working on it\n")
	r.step(t, 2*time.Minute)
	assert.Equal(t, writes+1, r.server.writes, "a bus message from the session is a new proof")
	assert.Equal(t, r.now.UTC(), r.server.row.Seen)
	r.step(t, BeatEvery)
	r.step(t, BeatEvery)
	assert.Equal(t, writes+1, r.server.writes, "the same proof again writes nothing")
	assert.Equal(t, sprint.Up, r.server.status(r.now))

	// quiet, a check unanswered past the bound: the beat says down with the reason, and
	// the server records that word at once, naming why
	r.step(t, SessionQuiet)
	require.Len(t, r.app.got(), 2, "a fresh check after the quiet")
	r.step(t, SessionBound)
	up, reason := r.present(t)
	require.False(t, up)
	assert.Equal(t, NoSessionAnswer, reason)
	assert.Equal(t, sprint.Down, r.server.row.State, "down is recorded, not left to the proof's window")
	assert.Equal(t, NoSessionAnswer, r.server.row.Reason, "with the reason")
	assert.Equal(t, r.now.UTC(), r.server.row.Seen, "dated by the server's clock at the beat")
	assert.Equal(t, sprint.Down, r.server.status(r.now))
	assert.Equal(t, PresenceDown, r.saved.ProofState)
	assert.Equal(t, sprint.Down, r.saved.ServerStatus)
	writes = r.server.writes
	r.step(t, BeatEvery)
	assert.Equal(t, writes, r.server.writes, "down again is not written again")

	// the next answer brings the row up again within one beat
	r.send(t, r.direct, "bob", PongSubject, PongLine("n2", 0, 0, 4)+"\n")
	r.step(t, BeatEvery)
	assert.Equal(t, sprint.Up, r.server.status(r.now), "an answering session is never down on the server for more than one beat")
	assert.Equal(t, sprint.Up, r.server.row.State)
	assert.Empty(t, r.server.refused, "%v", r.server.refused)
	require.NoError(t, r.beat(ctx))
}
