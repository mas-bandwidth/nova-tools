package friend

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/filelock"
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

func TestSessionStateMissingAndDurableUpdates(t *testing.T) {
	t.Parallel()
	parent := t.TempDir()
	dir := filepath.Join(parent, "not-created")
	got, err := ReadSessionState(dir)
	require.NoError(t, err)
	assert.Equal(t, SessionState{}, got, "a new state directory starts with no operator choice")
	_, err = os.Stat(dir)
	assert.ErrorIs(t, err, os.ErrNotExist, "a read must not create a state directory")

	got, err = UpdateSessionState(dir, func(s *SessionState) error {
		s.Coordinator, s.Asleep, s.WakeBarrier = "coordinator", true, "123-4"
		return nil
	})
	require.NoError(t, err)
	want := SessionState{Coordinator: "coordinator", Asleep: true, WakeBarrier: "123-4"}
	assert.Equal(t, want, got)
	got, err = ReadSessionState(dir)
	require.NoError(t, err)
	assert.Equal(t, want, got, "the state and ordering barrier survive a fresh read")

	got, err = UpdateSessionState(dir, func(s *SessionState) error {
		s.Asleep = false
		return nil
	})
	require.NoError(t, err)
	assert.Equal(t, SessionState{Coordinator: "coordinator", WakeBarrier: "123-4"}, got, "wake retains coordinator and barrier")
}

func TestSessionStateRefusesMalformedAndFailedWrites(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(sessionPath(dir), []byte("{"), 0o600))
	_, err := ReadSessionState(dir)
	assert.Error(t, err, "malformed durable state must not silently become awake")

	dir = t.TempDir()
	require.NoError(t, os.Mkdir(sessionPath(dir), 0o700))
	got, err := UpdateSessionState(dir, func(s *SessionState) error { s.Asleep = true; return nil })
	assert.Error(t, err, "failed atomic write must be surfaced so callers do not start a job")
	assert.Equal(t, SessionState{}, got, "failed writes do not return uncommitted state")
}

func TestNoOpSessionUpdateDoesNotCreateStateFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	got, err := UpdateSessionState(dir, func(*SessionState) error { return nil })
	require.NoError(t, err)
	assert.Equal(t, SessionState{}, got)
	_, err = os.Stat(sessionPath(dir))
	assert.ErrorIs(t, err, os.ErrNotExist, "an unchanged state must not be rewritten")
}

func TestNoOpSessionUpdatePreservesExistingFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	_, err := UpdateSessionState(dir, func(s *SessionState) error { s.Coordinator = "coordinator"; return nil })
	require.NoError(t, err)
	before, err := os.Stat(sessionPath(dir))
	require.NoError(t, err)
	_, err = UpdateSessionState(dir, func(*SessionState) error { return nil })
	require.NoError(t, err)
	after, err := os.Stat(sessionPath(dir))
	require.NoError(t, err)
	assert.True(t, os.SameFile(before, after), "an unchanged state must retain the atomically replaced file")
}

func TestSessionStateUpdatesAreSerializedAndDaemonIsSingleton(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	const writers, increments = 4, 5
	var wg sync.WaitGroup
	errs := make(chan error, writers*increments)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < increments; j++ {
				_, err := UpdateSessionState(dir, func(s *SessionState) error {
					if s.Coordinator == "" {
						s.Coordinator = "coordinator"
					}
					s.Asleep = !s.Asleep
					return nil
				})
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	got, err := ReadSessionState(dir)
	require.NoError(t, err)
	assert.Equal(t, "coordinator", got.Coordinator)
	assert.False(t, got.Asleep, "twenty serialized toggles must not lose a write")

	lock, err := TakeDaemonLock(dir, "friend")
	require.NoError(t, err)
	defer func() { require.NoError(t, lock.Unlock()) }()
	_, err = TakeDaemonLock(dir, "friend")
	assert.ErrorIs(t, err, filelock.ErrHeld, "a second daemon sharing this state directory must refuse")
}
