package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/pkg/friend"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPongOmittedCountsPreserveKnownWorkAndWidth(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	state := t.TempDir()
	dir := t.TempDir()
	require.NoError(t, friend.WriteStatus(state, friend.Status{Friend: "bob", Seat: "ada", Width: 32}))
	require.NoError(t, friend.WritePong(state, friend.Pong{Nonce: "old", Queue: 7, Working: 3, Width: 8}))
	cli := r.cli()
	cli.Do(t, "pong", "--as", "bob", "--nonce", "first", "--state-dir", state).Exit(0)
	p, found, err := friend.ReadPong(state)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, [3]int{7, 3, 32}, [3]int{p.Queue, p.Working, p.Width})
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "inbox"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "inbox", "QUEUE.json"), []byte(`{"tasks":[{"id":"q1","state":"queued"},{"id":"w1","state":"working"}]}`), 0o644))
	cli.Do(t, "pong", "--as", "bob", "--nonce", "second", "--state-dir", state, "--dir", dir).Exit(0)
	p, found, err = friend.ReadPong(state)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, [3]int{1, 1, 32}, [3]int{p.Queue, p.Working, p.Width})
}

func TestGeneratedPongCommandQuotesSpacedWorkingDirectory(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	w := r.world()
	line := w.pongCommand("bob", "fresh", "/tmp/proof", "store.test:6379", "/work/project one")
	assert.Contains(t, line, "--dir '/work/project one'")
	assert.NotContains(t, line, "--dir /work/project one")
	state := t.TempDir()
	var out, errs strings.Builder
	code := run([]string{"check", "--as", "bob", "--harness", "codex", "--dir", "/work/project one", "--state-dir", state, "--to", "ada", "--dry-run"}, strings.NewReader(""), &out, &errs, w)
	require.Zero(t, code, errs.String())
	assert.Contains(t, out.String(), "--dir '/work/project one'")
}
