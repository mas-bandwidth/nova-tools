package sprint

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A friend's beat, whatever session proof it carries, never makes her up: the beat proves
// a daemon, and she is up only on evidence from her own session (FriendEvidence;
// docs/SPEC-FRIEND.md, "Presence is her session's evidence"). The proof a beat carries is
// kept and read, and is none.
func TestAFriendWhoseBeatCarriesAProofIsStillDownWithoutEvidence(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 5, 22, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name  string
		proof time.Time
	}{
		{"no proof carried", time.Time{}},
		{"the proof at its bound", now.Add(-FriendProofLive)},
		{"the proof lapsed", now.Add(-FriendProofLive - time.Second)},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			b := Beat{At: now.Add(-time.Second), Proof: c.proof}
			assert.Equal(t, Down, FriendStatus(FriendPresence{Beat: b}, now))
			assert.Equal(t, Held, FriendStatus(FriendPresence{Held: true, Beat: b}, now))
		})
	}
	// the friend beat record keeps the proof under "pong"; the beat read from it carries it
	var b Beat
	require.NoError(t, json.Unmarshal([]byte(`{"at":"2026-10-05T21:59:59Z","pong":"2026-10-05T21:40:00Z"}`), &b))
	assert.Equal(t, time.Date(2026, 10, 5, 21, 40, 0, 0, time.UTC), b.Proof.UTC())
	assert.Equal(t, Down, FriendStatus(FriendPresence{Beat: b}, now))
}
