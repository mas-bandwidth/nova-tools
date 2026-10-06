package sprint

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A friend's beat itself never makes her up, and the session proof her record keeps
// does while it is under FriendPongWindow old and her beat is fresh: the proof is the
// server's time of her session's answer to a check her daemon asked (ProveBeat), so a
// daemon that stopped beating proves nothing more (FriendEvidence; docs/SPEC-FRIEND.md,
// "Presence is her session's evidence").
func TestAFriendIsUpOnTheSessionProofHerBeatCarries(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 5, 22, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name  string
		beat  time.Time
		proof time.Time
		want  string
	}{
		{"no proof carried: her beat alone", now.Add(-time.Second), time.Time{}, Down},
		{"the proof just in its window", now.Add(-time.Second), now.Add(-FriendPongWindow + time.Second), Up},
		{"the proof at its bound", now.Add(-time.Second), now.Add(-FriendPongWindow), Down},
		{"a proof from the future", now.Add(-time.Second), now.Add(time.Minute), Down},
		{"a fresh proof, and her beats stopped", now.Add(-BeatDeadline - time.Second), now.Add(-BeatDeadline - 2*time.Second), Down},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			b := Beat{At: c.beat, Proof: c.proof}
			assert.Equal(t, c.want, FriendStatus(FriendPresence{Beat: b}, now))
			assert.Equal(t, Held, FriendStatus(FriendPresence{Held: true, Beat: b}, now), "held is the hold alone")
			down := b
			down.Friend = &FriendReport{Until: now.Add(time.Minute), Reason: "no session answer"}
			assert.Equal(t, Down, FriendStatus(FriendPresence{Beat: down}, now), "a beat that says down is down whatever proof it carries")
		})
	}
	word, why := FriendEvidence(FriendPresence{Beat: Beat{At: now.Add(-time.Second), Proof: now.Add(-3 * time.Minute)}}, now)
	assert.Equal(t, Up, word)
	assert.Equal(t, "session proof 3m0s ago", why)
	_, why = FriendEvidence(FriendPresence{Beat: Beat{At: now.Add(-time.Second), Proof: now.Add(-20 * time.Minute)}}, now)
	assert.Contains(t, why, "no session proof on her beat within 10m0s (last 20m0s ago)")
	_, why = FriendEvidence(FriendPresence{Beat: Beat{At: now.Add(-time.Minute), Proof: now.Add(-2 * time.Minute)}}, now)
	assert.Contains(t, why, "(last 2m0s ago, her beat stopped 1m0s ago)")
	// the friend beat record keeps the proof under "pong"; the beat read from it carries it
	var b Beat
	require.NoError(t, json.Unmarshal([]byte(`{"at":"2026-10-05T21:59:59Z","pong":"2026-10-05T21:55:00Z"}`), &b))
	assert.Equal(t, time.Date(2026, 10, 5, 21, 55, 0, 0, time.UTC), b.Proof.UTC())
	assert.Equal(t, Up, FriendStatus(FriendPresence{Beat: b}, now))
}

// ProveBeat counts an answer only to a check the same run asked, once, within
// CheckAnswerWithin: a time, a nonce never asked, another run's, one answered already
// and one asked too long ago are each a beat with no proof.
func TestAnAnswerProvesOnlyACheckItsRunAsked(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 6, 18, 0, 0, 0, time.UTC)
	asked, proved, why := ProveBeat(nil, BeatWords{Run: "r1", Check: "n1"}, now)
	require.False(t, proved)
	require.Empty(t, why)
	require.Len(t, asked, 1)
	for _, c := range []struct {
		name string
		w    BeatWords
	}{
		{"a time", BeatWords{Run: "r1", Pong: now.Format(time.RFC3339)}},
		{"a nonce never asked", BeatWords{Run: "r1", Pong: "n2"}},
		{"another run's answer", BeatWords{Run: "r2", Pong: "n1"}},
	} {
		next, proved, why := ProveBeat(asked, c.w, now)
		assert.False(t, proved, c.name)
		assert.Contains(t, why, NoProof, c.name)
		assert.Equal(t, asked, next, "%s: the ask still stands", c.name)
	}
	next, proved, _ := ProveBeat(asked, BeatWords{Run: "r1", Pong: "n1"}, now.Add(time.Minute))
	assert.True(t, proved, "the answer to the check its run asked")
	assert.Empty(t, next)
	_, proved, _ = ProveBeat(next, BeatWords{Run: "r1", Pong: "n1"}, now.Add(time.Minute))
	assert.False(t, proved, "answered once")
	_, proved, _ = ProveBeat(asked, BeatWords{Run: "r1", Pong: "n1"}, now.Add(CheckAnswerWithin+time.Second))
	assert.False(t, proved, "too long after the ask")
	many := asked
	for i := range MaxAsked + 2 {
		many, _, _ = ProveBeat(many, BeatWords{Run: "r1", Check: fmt.Sprintf("m%d", i)}, now)
	}
	assert.Len(t, many, MaxAsked, "the oldest asks are dropped")
}
