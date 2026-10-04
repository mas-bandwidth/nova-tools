package friend

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func codexSessions(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, "sessions"), 0o755))
	for name, header := range map[string]string{
		"old":   `{"type":"session_meta","payload":{"id":"old","cwd":"/w/project","timestamp":"2026-10-01T00:00:00Z"}}`,
		"new":   `{"type":"session_meta","payload":{"id":"new","cwd":"/w/project","timestamp":"2026-10-02T00:00:00Z"}}`,
		"other": `{"type":"session_meta","payload":{"id":"other","cwd":"/w/other","timestamp":"2026-10-03T00:00:00Z"}}`,
	} {
		require.NoError(t, os.WriteFile(filepath.Join(home, "sessions", name+".jsonl"), []byte(header+"\nnot JSON: chat body must not be parsed\n"), 0o600))
	}
	require.NoError(t, os.WriteFile(filepath.Join(home, "session_index.jsonl"), []byte("{\"id\":\"old\",\"updated_at\":\"2026-10-04T00:00:00Z\"}\n"), 0o600))
	return home
}

func TestCodexNewestThreadIsResolvedBeforeItsLockIsProbed(t *testing.T) {
	t.Parallel()
	home := codexSessions(t)
	fe := &fakeExec{}
	var probed string
	c := &Codex{Home: home, Dir: "/w/project", Run: fe.run, Held: func(path string) bool { probed = path; return true }}
	_, err := c.Deliver(context.Background(), "hello")
	require.ErrorContains(t, err, "thread old")
	assert.Equal(t, LockPath(home, "old"), probed)
	assert.Empty(t, fe.calls)
}

func TestCodexNoSavedThreadRefusesWithoutSpawning(t *testing.T) {
	t.Parallel()
	fe := &fakeExec{}
	c := &Codex{Home: codexSessions(t), Dir: "/w/missing", Run: fe.run}
	_, err := c.Deliver(context.Background(), "hello")
	require.ErrorContains(t, err, "start a thread there or name one with --session")
	assert.Empty(t, fe.calls)
}
