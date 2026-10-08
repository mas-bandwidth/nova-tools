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

	q, w, known, err := ReadQueue(dir)
	require.NoError(t, err)
	assert.False(t, known, "no queue file supplies no current snapshot")
	assert.Equal(t, [2]int{0, 0}, [2]int{q, w})
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "inbox"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", "QUEUE.json"), []byte(`{"tasks":[{"id":"a","state":"queued"},{"id":"b","state":"working"},{"id":"c","state":"done"},{"id":"d"}]}`), 0o644))
	q, w, known, err = ReadQueue(dir)
	require.NoError(t, err)
	assert.False(t, known, "legacy states do not prove current ownership")
	assert.Equal(t, [2]int{0, 0}, [2]int{q, w})
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", "QUEUE.json"), []byte(`[{"id":"a","state":"working"}]`), 0o644))
	q, w, known, err = ReadQueue(dir)
	require.NoError(t, err)
	assert.False(t, known, "a bare list is historical too")
	assert.Equal(t, [2]int{0, 0}, [2]int{q, w})
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", "QUEUE.json"), []byte(`{"version":2,"current":{"a":{"gen":1,"job":"a~15"},"b":{"gen":2,"job":"b~15.g2"}},"tasks":[{"id":"a","state":"queued","gen":1,"job":"a~15"},{"id":"b","state":"working","gen":2,"job":"b~15.g2"},{"id":"old","state":"working","gen":1,"job":"old~14"}]}`), 0o644))
	q, w, known, err = ReadQueue(dir)
	require.NoError(t, err)
	assert.True(t, known)
	assert.Equal(t, [2]int{1, 1}, [2]int{q, w})
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", "QUEUE.json"), []byte(`nope`), 0o644))
	_, _, _, err = ReadQueue(dir)
	assert.Error(t, err)

	require.NoError(t, Record(dir, "one"))
	require.NoError(t, Record(dir, "two"))
	raw, err := os.ReadFile(LogPath(dir))
	require.NoError(t, err)
	assert.Equal(t, "one\ntwo\n", string(raw))
}
