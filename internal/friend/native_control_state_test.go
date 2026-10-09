//go:build linux || darwin

package friend

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNativeControlRestartCannotHideDebtInAnotherEmptyState(t *testing.T) {
	t.Parallel()
	for _, change := range []string{"different-state", "replaced-state", "missing-reverse", "corrupt-reverse", "truncated-membership", "another-friend"} {
		t.Run(change, func(t *testing.T) {
			t.Parallel()
			home, work := t.TempDir(), t.TempDir()
			require.NoError(t, os.Chmod(home, 0700))
			root, err := NativeControlDir(home, work)
			require.NoError(t, err)
			state := filepath.Join(home, "first-state")
			d := &Daemon{Friend: "worker", Dir: work, StateDir: state, NativeControlDir: root, NativeControlBinding: func() (string, error) { return NativeControlDir(home, work) }}
			require.NoError(t, d.validateNativeControl())
			require.NoError(t, WriteLanes(state, LaneState{Started: map[string]Started{"owned": {Card: Card{ID: "owned"}}}, StopReturns: []StopReturn{{Job: "owed", Ended: true}}}))
			before, err := os.ReadFile(filepath.Join(state, LanesFile))
			require.NoError(t, err)
			original := state
			switch change {
			case "different-state":
				d.StateDir = filepath.Join(t.TempDir(), "empty-state")
			case "replaced-state":
				require.NoError(t, os.Rename(state, state+"-prior"))
				original = state + "-prior"
			case "missing-reverse":
				require.NoError(t, os.Remove(nativeControlStatePath(root, d.Friend)))
				d.StateDir = filepath.Join(t.TempDir(), "empty-state")
			case "corrupt-reverse":
				require.NoError(t, os.WriteFile(nativeControlStatePath(root, d.Friend), []byte("corrupt"), 0600))
				d.StateDir = filepath.Join(t.TempDir(), "empty-state")
			case "truncated-membership":
				path := filepath.Join(filepath.Dir(root), "INDEX.json")
				raw, err := os.ReadFile(path)
				require.NoError(t, err)
				var index nativeControlIndex
				require.NoError(t, json.Unmarshal(raw, &index))
				index.Bindings[0].States = nil
				require.NoError(t, writeNativeControlIndex(path, &index))
				d.StateDir = filepath.Join(t.TempDir(), "empty-state")
			case "another-friend":
				d.Friend = "other"
			}
			err = d.validateNativeControl()
			assert.ErrorIs(t, err, errMachineLaunchHeld, "no new native start can hide existing Started/StopReturns")
			after, err := os.ReadFile(filepath.Join(original, LanesFile))
			require.NoError(t, err)
			assert.Equal(t, before, after)
		})
	}
}

func TestNativeControlSharedWorkDifferentFriendsKeepSeparateStateAndClaims(t *testing.T) {
	t.Parallel()
	home, work := t.TempDir(), t.TempDir()
	require.NoError(t, os.Chmod(home, 0700))
	root, err := NativeControlDir(home, work)
	require.NoError(t, err)
	for _, name := range []string{"first", "second"} {
		d := &Daemon{Friend: name, Dir: work, StateDir: filepath.Join(home, name), NativeControlDir: root, NativeControlBinding: func() (string, error) { return NativeControlDir(home, work) }}
		require.NoError(t, d.validateNativeControl())
		require.NoError(t, d.validateNativeControl(), "same state restart remains valid")
	}
}

func TestNativeControlLegacyPresentInvalidLaneIsHeldBeforeLaunch(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"malformed", "empty-ended", "malformed-ended", "oversized", "directory", "symlink", "fifo", "absent", "canonical-ended"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			work := t.TempDir()
			path := laneMarkPath(work, "owned")
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0700))
			switch kind {
			case "absent":
			case "canonical-ended":
				require.NoError(t, os.WriteFile(path, []byte(LaneMarkEnded("old")), 0600))
			case "fifo":
				require.NoError(t, syscall.Mkfifo(path, 0600))
			case "directory":
				require.NoError(t, os.Mkdir(path, 0700))
			case "symlink":
				require.NoError(t, os.Symlink(filepath.Join(t.TempDir(), "missing"), path))
			default:
				raw := []byte("unknown owned claim")
				if kind == "empty-ended" {
					raw = []byte(LaneMarkEnded(""))
				}
				if kind == "malformed-ended" {
					raw = []byte(LaneMarkEnded("old") + "junk\n")
				}
				if kind == "oversized" {
					raw = []byte(strings.Repeat("x", 4097))
				}
				require.NoError(t, os.WriteFile(path, raw, 0600))
			}
			d := &Daemon{Dir: work, StateDir: filepath.Join(t.TempDir(), "state"), NativeControlDir: filepath.Join(t.TempDir(), "control")}
			if kind == "absent" || kind == "canonical-ended" {
				assert.NoError(t, d.ValidateLegacyNativeAuthority())
				return
			}
			assert.ErrorContains(t, d.ValidateLegacyNativeAuthority(), "legacy native lane claim")
			_, err := os.Lstat(path)
			assert.NoError(t, err, "refusal preserves the old marker")
		})
	}
}

func TestNativeControlEmptyStateIsReservedForOneFriendBeforeForwardProof(t *testing.T) {
	t.Parallel()
	home, work, state := t.TempDir(), t.TempDir(), t.TempDir()
	require.NoError(t, os.Chmod(home, 0700))
	root, err := NativeControlDir(home, work)
	require.NoError(t, err)
	require.NoError(t, bindNativeControlOwnerState("first", state, root))
	assert.ErrorContains(t, bindNativeControlOwnerState("second", state, root), "another friend")
	require.NoError(t, bindNativeControlStateForFriend(state, root, "first"))
	require.NoError(t, bindNativeControlOwnerState("first", state, root), "same empty-state crash continuation stays valid")
}
