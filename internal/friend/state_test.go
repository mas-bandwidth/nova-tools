package friend

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTheStateFilesRoundTripAndTheQueueFileCounts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, found, err := ReadStatus(dir)
	require.NoError(t, err)
	assert.False(t, found)
	s := Status{Friend: "bob", Harness: "opencode", At: t0, Connection: Connected, Challenge: Quiet, Width: 4}
	require.NoError(t, WriteStatus(dir, s))
	got, found, err := ReadStatus(dir)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, s, got)
	p := Pong{Nonce: "n1", At: t0.Add(time.Second), To: "ada", Queue: 2, Working: 1, Width: 4}
	require.NoError(t, WritePong(dir, p))
	gp, found, err := ReadPong(dir)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, p, gp)

	q, w, err := ReadQueue(dir)
	require.NoError(t, err)
	assert.Equal(t, [2]int{0, 0}, [2]int{q, w}, "no queue file counts zero")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "inbox"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", "QUEUE.json"), []byte(`{"tasks":[{"id":"a","state":"queued"},{"id":"b","state":"working"},{"id":"c","state":"done"},{"id":"d"}]}`), 0o644))
	q, w, err = ReadQueue(dir)
	require.NoError(t, err)
	assert.Equal(t, [2]int{2, 1}, [2]int{q, w}, "no state is queued")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", "QUEUE.json"), []byte(`[{"id":"a","state":"working"}]`), 0o644))
	q, w, err = ReadQueue(dir)
	require.NoError(t, err)
	assert.Equal(t, [2]int{0, 1}, [2]int{q, w}, "a bare list is a queue too")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", "QUEUE.json"), []byte(`nope`), 0o644))
	_, _, err = ReadQueue(dir)
	assert.Error(t, err)

	require.NoError(t, Record(dir, "one"))
	require.NoError(t, Record(dir, "two"))
	raw, err := os.ReadFile(LogPath(dir))
	require.NoError(t, err)
	assert.Equal(t, "one\ntwo\n", string(raw))
}

// The delivery record round-trips, ClearBroken removes the mark and keeps the
// counts, and a broken friend's presence file carries broken and the refusal as
// its reason while a reader of PresenceStatus still reads it.
func TestTheDeliveryRecordRoundTripsAndABrokenPresenceCarriesTheReason(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, found, err := ReadDelivery(dir)
	require.NoError(t, err)
	assert.False(t, found)
	_, had, err := ClearBroken(dir)
	require.NoError(t, err)
	assert.False(t, had, "no record, nothing marked")
	mark := BrokenMark{At: t0, Reason: "invalid_request_error: bad", SessionID: "ses_x", FirstSeen: t0.Add(-time.Minute), Count: 3, Told: true}
	rec := Delivery{Failed: map[string]int{"1-1": 2}, Streak: 3, StreakFirst: t0.Add(-time.Minute), Broken: &mark}
	require.NoError(t, WriteDelivery(dir, rec))
	got, found, err := ReadDelivery(dir)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, rec, got)
	cleared, had, err := ClearBroken(dir)
	require.NoError(t, err)
	assert.True(t, had)
	assert.Equal(t, mark, cleared)
	got, _, err = ReadDelivery(dir)
	require.NoError(t, err)
	assert.Nil(t, got.Broken)
	assert.Equal(t, map[string]int{"1-1": 2}, got.Failed)
	assert.Zero(t, got.Streak)

	require.NoError(t, WritePresenceBroken(dir, PresenceStatus{Friend: "bob", Presence: PresenceUp, At: t0}, mark))
	p, found, err := ReadPresence(dir)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, PresenceDown, p.Presence)
	assert.Equal(t, mark.Reason, p.Reason)
	raw, err := os.ReadFile(filepath.Join(dir, PresenceFile))
	require.NoError(t, err)
	assert.Contains(t, string(raw), `"broken": "`+t0.Format(time.RFC3339))
}
