package sprint

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A friend's beat itself never makes her up, and the session proof it carries does
// while it is under FriendProofLive old: the proof is her session's own answer to her
// daemon's SESSION CHECK nonce (or its own bus message), which her daemon proves on
// every beat (FriendEvidence; docs/SPEC-FRIEND.md, "Presence is her session's
// evidence"). The finding of 2026-10-06: three friends read down for twenty minutes
// with their sessions answering, because nothing but a hand friend health read it.
func TestAFriendIsUpOnTheSessionProofHerBeatCarries(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 5, 22, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name  string
		proof time.Time
		want  string
	}{
		{"no proof carried: her beat alone", time.Time{}, Down},
		{"the proof just in its window", now.Add(-FriendProofLive + time.Second), Up},
		{"the proof at its bound", now.Add(-FriendProofLive), Down},
		{"the proof lapsed", now.Add(-FriendProofLive - time.Second), Down},
		{"a proof from the future", now.Add(time.Minute), Down},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			b := Beat{At: now.Add(-time.Second), Proof: c.proof}
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
	assert.Contains(t, why, "no session proof on her beat within 15m0s (last 20m0s ago)")
	// the friend beat record keeps the proof under "pong"; the beat read from it carries it
	var b Beat
	require.NoError(t, json.Unmarshal([]byte(`{"at":"2026-10-05T21:59:59Z","pong":"2026-10-05T21:55:00Z"}`), &b))
	assert.Equal(t, time.Date(2026, 10, 5, 21, 55, 0, 0, time.UTC), b.Proof.UTC())
	assert.Equal(t, Up, FriendStatus(FriendPresence{Beat: b}, now))
}
