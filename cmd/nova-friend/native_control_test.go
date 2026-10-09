package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/friend"
	"github.com/stretchr/testify/require"
)

func TestNativeControlDefaultStateIsPrivateAndLegacyOwnershipIsPreserved(t *testing.T) {
	t.Parallel()
	r := newRig(t, "sender", "worker")
	w := r.world()
	w.wall = func(_ friend.Wall, e friend.Exec) friend.Exec { return e }
	work := t.TempDir()
	cli := cliOf(w)
	cli.Do(t, "run", "--as", "worker", "--harness", "opencode", "--dir", work, "--dry-run").Exit(0).Out("state="+friend.DefaultStateDir(r.home, "worker"))
	legacy := friend.StateDirIn(work)
	require.NoError(t, friend.WriteLanes(legacy, friend.LaneState{Sessions: map[int]string{1: "existing-session"}}))
	before, err := os.ReadFile(filepath.Join(legacy, friend.LanesFile))
	require.NoError(t, err)
	cli.Do(t, "run", "--as", "worker", "--harness", "opencode", "--dir", work).Exit(2).Err("legacy in-work daemon state remains", "private --state-dir")
	after, err := os.ReadFile(filepath.Join(legacy, friend.LanesFile))
	require.NoError(t, err)
	require.Equal(t, before, after)
}
