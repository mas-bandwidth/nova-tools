//go:build linux || darwin

package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
	cli.Do(t, "run", "--as", "worker", "--harness", "opencode", "--dir", work, "--dry-run").Exit(0).Out("state=" + friend.DefaultStateDir(r.home, "worker"))
	legacy := friend.StateDirIn(work)
	require.NoError(t, friend.WriteLanes(legacy, friend.LaneState{Sessions: map[int]string{1: "existing-session"}}))
	before, err := os.ReadFile(filepath.Join(legacy, friend.LanesFile))
	require.NoError(t, err)
	cli.Do(t, "run", "--as", "worker", "--harness", "opencode", "--dir", work).Exit(2).Err("legacy in-work daemon state remains", "private --state-dir")
	after, err := os.ReadFile(filepath.Join(legacy, friend.LanesFile))
	require.NoError(t, err)
	require.Equal(t, before, after)
}

func TestNativeControlProductionResolverDoesNotSplitWithDifferentHOME(t *testing.T) {
	t.Parallel()
	account, work := t.TempDir(), t.TempDir()
	require.NoError(t, os.Chmod(account, 0700))
	resolver := func(dir string) (string, error) { return friend.NativeControlDir(account, dir) }
	first := world{home: t.TempDir(), nativeControlRoot: resolver}
	second := world{home: t.TempDir(), nativeControlRoot: resolver}
	root, err := first.nativeControlDir(work)
	require.NoError(t, err)
	other, err := second.nativeControlDir(work)
	require.NoError(t, err)
	require.Equal(t, root, other)
}

// CLI capability inspection uses native code too, so its context must use the
// same authority exclusion as later provider turns (SPEC-FRIEND control roots).
func TestNativeControlOpenCodePreflightIsWalledBeforeFirstNativeExecution(t *testing.T) {
	t.Parallel()
	r := newRig(t, "sender", "worker")
	w := r.world()
	require.NoError(t, os.Chmod(r.home, 0700))
	work := t.TempDir()
	calls := 0
	w.wall = func(wall friend.Wall, next friend.Exec) friend.Exec {
		return func(ctx context.Context, dir, prog string, args []string, stdin string) (string, int, error) {
			calls++
			require.True(t, friend.InLane(ctx), "preflight must not bypass the native wall")
			require.NotEmpty(t, wall.ControlDir)
			require.Equal(t, friend.DefaultStateDir(r.home, "worker"), wall.ControlState)
			require.False(t, strings.HasPrefix(wall.ControlDir, work+string(os.PathSeparator)))
			return next(ctx, dir, prog, args, stdin)
		}
	}
	w.exec = func(_ context.Context, _ string, _ string, args []string, _ string) (string, int, error) {
		if len(args) == 1 && args[0] == "--version" {
			return "synthetic-version", 0, nil
		}
		require.Equal(t, []string{"run", "--help"}, args)
		// Force the normal capability refusal after inspection; no provider or
		// bus daemon starts in this fixture.
		return "--synthetic-unrelated-flag", 0, nil
	}
	cliOf(w).Do(t, "run", "--as", "worker", "--harness", "opencode", "--dir", work).Exit(2).Err("synthetic-version")
	require.Equal(t, 2, calls)
}
