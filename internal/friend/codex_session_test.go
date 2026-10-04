package friend

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func codexSessions(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	for _, dir := range []string{"project", "other", "missing"} {
		require.NoError(t, os.MkdirAll(filepath.Join(home, dir), 0o755))
	}
	require.NoError(t, os.MkdirAll(filepath.Join(home, "sessions"), 0o755))
	for name, header := range map[string]string{
		"old":   `{"type":"session_meta","payload":{"id":"old","cwd":"/w/project","timestamp":"2026-10-01T00:00:00Z"}}`,
		"new":   `{"type":"session_meta","payload":{"id":"new","cwd":"/w/project","timestamp":"2026-10-02T00:00:00Z"}}`,
		"other": `{"type":"session_meta","payload":{"id":"other","cwd":"/w/other","timestamp":"2026-10-03T00:00:00Z"}}`,
	} {
		header = strings.ReplaceAll(header, `"/w/project"`, strconv.Quote(filepath.Join(home, "project")))
		header = strings.ReplaceAll(header, `"/w/other"`, strconv.Quote(filepath.Join(home, "other")))
		require.NoError(t, os.WriteFile(filepath.Join(home, "sessions", name+".jsonl"), []byte(header+"\nnot JSON: chat body must not be parsed\n"), 0o600))
	}
	require.NoError(t, os.WriteFile(filepath.Join(home, "session_index.jsonl"), []byte("{\"id\":\"old\",\"updated_at\":\"2026-10-04T00:00:00Z\"}\n"), 0o600))
	return home
}

func TestCodexNewestThreadAndRolloutAreResolvedTogether(t *testing.T) {
	t.Parallel()
	home := codexSessions(t)
	id, rollout, err := ResolveCodexSession(home, filepath.Join(home, "project"), "")
	require.NoError(t, err)
	assert.Equal(t, "old", id)
	assert.Equal(t, filepath.Join(home, "sessions", "old.jsonl"), rollout)
}

func TestCodexNoSavedThreadRefusesWithoutSpawning(t *testing.T) {
	t.Parallel()
	fe := &fakeExec{}
	home := codexSessions(t)
	c := &Codex{Home: home, Dir: filepath.Join(home, "missing"), Run: fe.run}
	_, err := c.Deliver(context.Background(), "hello")
	require.ErrorContains(t, err, "start a thread there or name one with --session")
	assert.Empty(t, fe.calls)
}

func TestCodexNewestSessionMatchesPhysicalDirectoryAliases(t *testing.T) {
	t.Parallel()
	home := codexSessions(t)
	alias := filepath.Join(home, "alias")
	require.NoError(t, os.Symlink(filepath.Join(home, "project"), alias))
	cwd, err := os.Getwd()
	require.NoError(t, err)
	relative, err := filepath.Rel(cwd, filepath.Join(home, "project"))
	require.NoError(t, err)
	for name, dir := range map[string]string{"symlink": alias, "relative": relative} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			id, err := NewestCodexSession(home, dir)
			require.NoError(t, err)
			assert.Equal(t, "old", id)
		})
	}
}

func TestCodexNamedSessionIgnoresCommandDirectory(t *testing.T) {
	t.Parallel()
	home := codexSessions(t)
	id, path, err := ResolveCodexSession(home, filepath.Join(home, "does-not-exist"), "other")
	require.NoError(t, err)
	assert.Equal(t, "other", id)
	assert.Equal(t, filepath.Join(home, "sessions", "other.jsonl"), path)
}

func TestCodexReceiptRequiresTheExactNewCompleteUserRecord(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	header := `{"type":"session_meta","payload":{"id":"thread-1"}}` + "\n"
	old := `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"same"}]}}` + "\n"
	require.NoError(t, os.WriteFile(path, []byte(header+old), 0o600))
	found, boundary, err := CodexReceipt(path, "thread-1", "same", 0)
	require.NoError(t, err)
	assert.True(t, found, "the reader reports an old match, while the adapter uses only its EOF boundary")
	assert.Equal(t, int64(len(header+old)), boundary)

	partial := `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"same"}]}}`
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	require.NoError(t, err)
	_, err = f.WriteString(partial)
	require.NoError(t, err)
	require.NoError(t, f.Close())
	found, next, err := CodexReceipt(path, "thread-1", "same", boundary)
	require.NoError(t, err)
	assert.False(t, found, "a partial final record is not a receipt")
	assert.Equal(t, boundary, next)
	f, err = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0)
	require.NoError(t, err)
	_, err = f.WriteString("\n")
	require.NoError(t, err)
	require.NoError(t, f.Close())
	found, next, err = CodexReceipt(path, "thread-1", "same", boundary)
	require.NoError(t, err)
	assert.True(t, found)
	assert.Greater(t, next, boundary)
}

func TestCodexReceiptRejectsAnotherSessionAndSkipsOversizedLines(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	receipt := `{"type":"response_item","payload":{"type":"message","role":"user","content":[{"type":"input_text","text":"wanted"}]}}` + "\n"
	raw := `{"type":"session_meta","payload":{"id":"thread-1"}}` + "\n" + strings.Repeat("x", CodexReceiptLineLimit+1) + "\n" + receipt
	require.NoError(t, os.WriteFile(path, []byte(raw), 0o600))
	found, _, err := CodexReceipt(path, "thread-1", "wanted", 0)
	require.NoError(t, err)
	assert.True(t, found)
	_, _, err = CodexReceipt(path, "thread-2", "wanted", 0)
	require.ErrorContains(t, err, "session_meta")
}

func TestCodexReceiptAllowsWorstCaseJSONExpansionOfTheExpectedText(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "rollout.jsonl")
	text := strings.Repeat("\"", 1<<20)
	body, err := json.Marshal(map[string]any{"type": "response_item", "payload": map[string]any{
		"type": "message", "role": "user", "content": []map[string]string{{"type": "input_text", "text": text}},
	}})
	require.NoError(t, err)
	assert.Greater(t, len(body), CodexReceiptLineLimit)
	raw := `{"type":"session_meta","payload":{"id":"thread-1"}}` + "\n" + string(body) + "\n"
	require.NoError(t, os.WriteFile(path, []byte(raw), 0o600))
	found, _, err := CodexReceipt(path, "thread-1", text, 0)
	require.NoError(t, err)
	assert.True(t, found)
}
