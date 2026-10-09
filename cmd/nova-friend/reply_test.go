package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/bus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNativeReplyVerbQueuesPrivatelyWithoutOpeningBusOrControlState(t *testing.T) {
	t.Parallel()
	r := newRig(t, "freddy", "stella")
	w := r.world()
	w.open = func(context.Context, string) (bus.Store, func(), error) {
		return nil, nil, errors.New("reply must never open a bus socket")
	}
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "outbox"), 0o700))
	re := "01M4F39QAT9N7621G6XKC3R9AA"
	capability := strings.Repeat("a", 64)
	args := []string{"reply", "--as", "freddy", "--dir", dir, "--session", "ses_native", "--re", re,
		"--capability", capability, "--subject", "Freddy answer", "--body", "private reply body"}
	cli := cliOf(w)
	cli.Do(t, append(args, "--dry-run")...).Exit(0).Out("REPLY OK", "dry_run=true").NotOut("private reply body")
	_, err := os.Stat(filepath.Join(dir, "outbox", "native-replies"))
	assert.True(t, os.IsNotExist(err), "dry run creates no spool")
	cli.Do(t, args...).Exit(0).Out("REPLY OK", "state=queued-for-daemon").NotOut("private reply body")
	info, err := os.Stat(filepath.Join(dir, "outbox", "native-replies", re+".json"))
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	cli.Do(t, args...).Exit(2).Err("already has a native reply queued").NotOut("private reply body")
}

func TestLocalPongVerbQueuesWithoutBusAndNeverClaimsDelivered(t *testing.T) {
	t.Parallel()
	r := newRig(t, "freddy", "stella")
	w := r.world()
	w.open = func(context.Context, string) (bus.Store, func(), error) {
		return nil, nil, errors.New("local pong must never open a bus socket")
	}
	dir := t.TempDir()
	require.NoError(t, os.Mkdir(filepath.Join(dir, "outbox"), 0o700))
	args := []string{"local-pong", "--as", "freddy", "--dir", dir, "--session", "ses_native", "--nonce", "abc123",
		"--capability", strings.Repeat("a", 64), "--width", "32"}
	cli := cliOf(w)
	cli.Do(t, append(args, "--dry-run")...).Exit(0).Out("LOCAL-PONG OK", "dry_run=true")
	_, err := os.Stat(filepath.Join(dir, "outbox", "native-pongs"))
	assert.True(t, os.IsNotExist(err))
	cli.Do(t, args...).Exit(0).Out("LOCAL-PONG OK", "state=queued-for-daemon")
	assert.Zero(t, r.store.Len(bus.LogKey), "native tool has only queued a file")
}
