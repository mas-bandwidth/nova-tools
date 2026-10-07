package main

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
)

// Every verb that writes takes --dry-run that writes nothing (docs/STANDARD.md, the
// onboarding standard): ping and pong check their note as send does and send nothing, and
// pong writes no pong file; run checks its flags and harness and starts no daemon, opening
// no store, beating nothing and recording nothing.
func TestTheWritingVerbsDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	r := newRig(t, "ada", "bob")
	w := r.world()
	opened := 0
	open := w.open
	w.open = func(ctx context.Context, addr string) (bus.Store, func(), error) {
		opened++
		return open(ctx, addr)
	}
	beats := 0
	w.beat = func(context.Context, string, string, time.Time, friend.BeatWords) (string, error) {
		beats++
		return "", nil
	}
	cli := cliOf(w)

	cli.Do(t, "ping", "--as", "ada", "--to", "bob", "--nonce", "abc123", "--dry-run").Exit(0).Out("PING OK nonce=abc123 to=bob", "dry_run=true")
	cli.Do(t, "ping", "--as", "ada", "--to", "zed", "--dry-run").Exit(2).Err("zed is no known name")
	state := t.TempDir()
	cli.Do(t, "pong", "--as", "bob", "--nonce", "abc123", "--to", "ada", "--state-dir", state, "--dry-run").Exit(0).Out("PONG OK nonce=abc123 to=ada", "dry_run=true")
	assert.Equal(t, 0, r.store.Len(bus.LogKey), "a dry ping or pong sends nothing")
	_, found, err := friend.ReadPong(state)
	require.NoError(t, err)
	assert.False(t, found, "a dry pong writes no pong file")
	opened = 0

	dir := t.TempDir()
	cli.Do(t, "run", "--server", "127.0.0.1:6390", "--as", "bob", "--harness", "opencode", "--dir", dir, "--state-dir", state, "--dry-run").Exit(0).
		Out("RUN DRY-RUN as=bob harness=opencode", "nothing was started")
	assert.Zero(t, opened, "a dry run opens no store")
	assert.Zero(t, beats, "and beats nothing")
	files, err := os.ReadDir(state)
	require.NoError(t, err)
	assert.Empty(t, files, "and records nothing in the state directory")
	cli.Do(t, "run", "--server", "127.0.0.1:6390", "--as", "bob", "--harness", "nope", "--dir", filepath.Join(dir, "x"), "--dry-run").Exit(2).Err("is no harness")
}

// cliOf is the tool over a world a test changed.
func cliOf(w world) testkit.Main {
	return testkit.Main(func(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
		return run(args, stdin, stdout, stderr, w)
	})
}
