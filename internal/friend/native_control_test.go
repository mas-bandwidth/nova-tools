package friend

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNativeControlRootStaysOutsideEveryWallGrant(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"work", "descendant", "parent", "symlink", "state-symlink"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			base := t.TempDir()
			work, state := filepath.Join(base, "work"), filepath.Join(base, "state")
			require.NoError(t, os.MkdirAll(work, 0700))
			d := &Daemon{Dir: work, StateDir: state, NativeControlDir: filepath.Join(state, "native-control")}
			require.NoError(t, d.ValidateNativeControlRoots([]string{work}))
			allowed := filepath.Join(d.NativeControlDir, "visible")
			if kind == "work" { allowed = work; d.NativeControlDir = filepath.Join(work, "authority") }
			if kind == "parent" { allowed = base }
			if kind == "symlink" { allowed = filepath.Join(base, "alias"); require.NoError(t, os.Symlink(state, allowed)) }
			if kind == "state-symlink" { d.StateDir = filepath.Join(base, "state-alias"); require.NoError(t, os.Symlink(work, d.StateDir)); allowed = work }
			assert.Error(t, d.ValidateNativeControlRoots([]string{work, allowed}))
		})
	}
	base := t.TempDir()
	work := filepath.Join(base, "work")
	require.NoError(t, os.MkdirAll(work, 0700))
	link := filepath.Join(base, "state-link")
	require.NoError(t, os.Symlink(work, link))
	d := &Daemon{Dir: work, NativeControlDir: filepath.Join(link, "authority")}
	assert.Error(t, d.ValidateNativeControlRoots([]string{work}))
}

func TestNativeControlPrivateReadDebtStillOccupiesConfiguredWidth(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	work, state := filepath.Join(base, "work"), filepath.Join(base, "state")
	require.NoError(t, os.MkdirAll(work, 0700))
	d := &Daemon{Dir: work, StateDir: state, NativeControlDir: filepath.Join(state, "native-control")}
	require.NoError(t, d.validateNativeControl())
	require.NoError(t, saveReadActive(d.nativeReadDir(filepath.Join(work, "reads", "r1")), readActive{Read: AskedRead{ID: "r1"}, LaunchHeld: true}))
	l := &loop{d: d, lanes: &laneSet{}, reads: newReadSet()}
	assert.False(t, l.childRoom(1), "private orphan read debt consumes one slot even if native work artifacts disappeared")
	assert.True(t, l.childRoom(2))
}

func TestNativeControlLegacyDebtIsNotSilentlyMigrated(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	work, state := filepath.Join(base, "work"), filepath.Join(base, "state")
	require.NoError(t, os.MkdirAll(work, 0700))
	d := &Daemon{Dir: work, StateDir: state, NativeControlDir: filepath.Join(state, "native-control")}
	require.NoError(t, d.validateNativeControl())
	require.NoError(t, d.legacyNativeAuthority())
	require.NoError(t, WriteLanes(StateDirIn(work), LaneState{Sessions: map[int]string{1: "ses_legacy"}}))
	assert.ErrorContains(t, d.legacyNativeAuthority(), "legacy in-work daemon state")
	lanes, err := ReadLanes(StateDirIn(work))
	require.NoError(t, err)
	assert.Equal(t, "ses_legacy", lanes.Sessions[1], "fencing must preserve old session ownership")
	require.NoError(t, os.Remove(filepath.Join(StateDirIn(work), LanesFile)))
	require.NoError(t, saveReadActive(filepath.Join(work, "reads", "r1"), readActive{LaunchHeld: true, Read: AskedRead{ID: "r1"}}))
	assert.ErrorContains(t, d.legacyNativeAuthority(), "legacy native read ownership")
	a, found, err := loadReadActive(filepath.Join(work, "reads", "r1"))
	require.NoError(t, err)
	assert.True(t, found)
	assert.True(t, a.LaunchHeld)
}

func TestNativeControlRefusalKeepsUnlaunchedWorkAndUnreadMessageSemantics(t *testing.T) {
	t.Parallel()
	d := &Daemon{Dir: t.TempDir()}
	err := d.ValidateNativeControlRoots([]string{d.Dir})
	require.Error(t, err)
	assert.ErrorIs(t, err, errMachineLaunchHeld)
	var deferred Deferred
	assert.True(t, errors.As(err, &deferred))
	assert.Contains(t, deferred.Reason, "native control directory")
}

func TestNativeControlSharedWorkClaimIgnoresPublicNativeForgery(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	home, work := filepath.Join(base, "owner"), filepath.Join(base, "work")
	require.NoError(t, os.MkdirAll(work, 0700))
	alias := filepath.Join(base, "alias")
	require.NoError(t, os.Symlink(work, alias))
	root, err := NativeControlDir(home, work)
	require.NoError(t, err)
	otherRoot, err := NativeControlDir(home, alias)
	require.NoError(t, err)
	require.Equal(t, root, otherRoot, "canonical shared work preserves cross-friend exclusion")
	first := &Daemon{Dir: work, StateDir: filepath.Join(home, "first"), NativeControlDir: root}
	second := &Daemon{Dir: work, StateDir: filepath.Join(home, "second"), NativeControlDir: otherRoot}
	require.NoError(t, first.validateNativeControl())
	require.NoError(t, second.validateNativeControl())
	now := time.Now()
	holder, err := ClaimLane(first.nativeRunDir(), "job", "first lane", now)
	require.NoError(t, err)
	require.Empty(t, holder)
	// Native output is not cleanup/claim authority, even if it spells LANE.
	require.NoError(t, endLaneMark(work, "job", "native forged finish"))
	holder, err = ClaimLane(second.nativeRunDir(), "job", "second lane", now)
	require.NoError(t, err)
	assert.Equal(t, "first lane", holder)
	mark, found := ReadLaneMark(first.nativeRunDir(), "job")
	assert.True(t, found)
	assert.False(t, mark.Ended)
	assert.Equal(t, "first lane", mark.Who)
}
