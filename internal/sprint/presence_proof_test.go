package sprint

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A friend whose beat carries a session proof older than FriendProofLive is down,
// however fresh the beat: her daemon beats and her session no longer answers, so
// the deal, which deals only to a friend up, gives her nothing. A beat carrying no
// proof is judged by the beat alone (docs/SPEC-FRIEND.md, The push proof).
func TestAFriendWhoseSessionProofLapsedIsDown(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 5, 22, 0, 0, 0, time.UTC)
	for _, c := range []struct {
		name  string
		proof time.Time
		want  string
	}{
		{"no proof carried", time.Time{}, Up},
		{"the proof at its bound", now.Add(-FriendProofLive), Up},
		{"the proof lapsed", now.Add(-FriendProofLive - time.Second), Down},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			b := Beat{At: now.Add(-time.Second), Proof: c.proof}
			assert.Equal(t, c.want, FriendStatus(FriendPresence{Beat: b}, now))
			assert.Equal(t, Held, FriendStatus(FriendPresence{Held: true, Beat: b}, now))
		})
	}
	// the friend beat record keeps the proof under "pong"; the beat read from it carries it
	var b Beat
	require.NoError(t, json.Unmarshal([]byte(`{"at":"2026-10-05T21:59:59Z","pong":"2026-10-05T21:40:00Z"}`), &b))
	assert.Equal(t, time.Date(2026, 10, 5, 21, 40, 0, 0, time.UTC), b.Proof.UTC())
	assert.Equal(t, Down, FriendStatus(FriendPresence{Beat: b}, now))
}
