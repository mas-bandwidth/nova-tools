package bus

// The lock's per-function table had one statement no unit test reached: the
// production clock's Sleep (internal/bus/lock.go:131) sat at 0.0%, because every
// bounded wait in the package is driven on the injected step clock. The tests
// here close that line and the other uncovered blocks of internal/bus/lock.go
// with the package's own helpers: a dead holder staged by pid arithmetic rather
// than by starting a child, and a wait budget that costs the machine no wall
// time at all.

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// syntheticDeadPID names a pid above every pid the kernel hands out, so it
// names a holder that is not running, and no child is started to stage one.
// The same processAlive the product asks is asked here, and a pid that answers
// alive skips the test rather than standing in for a dead one.
func syntheticDeadPID(t *testing.T) int {
	t.Helper()
	const pid = 1 << 30
	if processAlive(pid) {
		t.Skipf("pid %d answers alive on this platform, and no child may be started to stage a dead holder", pid)
	}
	return pid
}

// realLockClock is the machine's clock, and its Sleep is the one statement of
// the lock file no unit test reached. Now is asked for a live time, and Sleep
// is asked for a zero wait, the only wait it can be given that costs the
// machine none.
func TestLockCoverRealClockAnswersNowAndSleepsNotAtAll(t *testing.T) {
	t.Parallel()

	clk := realLockClock{}
	assert.False(t, clk.Now().IsZero(), "realLockClock.Now answered the zero time")
	clk.Sleep(0)
}

// removeLockFile is the removal every stale clear goes through. A missing file
// is already gone, a present file is removed, and a removal the filesystem
// refuses for a reason no platform calls transient is returned as the error it
// is. The retry over a transient collision is the Windows build's path; on a
// platform whose collisions are always false it is not reachable.
func TestLockCoverRemoveLockFile(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		stage  func(t *testing.T, dir string) string
		wantOk bool
	}{
		{
			name: "a file that is there",
			stage: func(t *testing.T, dir string) string {
				path := filepath.Join(dir, LockName)
				require.NoError(t, os.WriteFile(path, nil, 0o644))
				return path
			},
			wantOk: true,
		},
		{
			name: "a file that is already gone",
			stage: func(t *testing.T, dir string) string {
				return filepath.Join(dir, LockName)
			},
			wantOk: true,
		},
		{
			name: "a directory no remove takes",
			stage: func(t *testing.T, dir string) string {
				path := filepath.Join(dir, LockName)
				require.NoError(t, os.Mkdir(path, 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(path, "kept"), nil, 0o644))
				return path
			},
			wantOk: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := tc.stage(t, t.TempDir())
			err := removeLockFile(path)
			if tc.wantOk {
				assert.NoError(t, err, "removeLockFile(%q)", path)
				return
			}
			assert.Error(t, err, "removeLockFile(%q) removed what no remove takes", path)
		})
	}
}

// clearStaleSentinel answers whether the sentinel beside a lock names a holder
// that is gone. The table stages every answer the pid can give: a dead
// holder's sentinel is cleared, a live holder's is left, a record with no pid
// to judge is never cleared on a guess, a sentinel no remove takes is left for
// a person, and no sentinel is nothing to clear.
func TestLockCoverClearStaleSentinel(t *testing.T) {
	t.Parallel()

	dead := syntheticDeadPID(t)
	tests := []struct {
		name      string
		holder    string
		stage     func(t *testing.T, lockPath string)
		wantClear bool
	}{
		{
			name:      "a holder that is gone",
			holder:    fmt.Sprintf("pid=%d at=2000-01-01T00:00:00Z", dead),
			stage:     writeSentinel,
			wantClear: true,
		},
		{
			name:      "a holder that is this live process",
			holder:    fmt.Sprintf("pid=%d", os.Getpid()),
			stage:     writeSentinel,
			wantClear: false,
		},
		{
			name:      "a holder with no pid to judge",
			holder:    "unwritten",
			stage:     writeSentinel,
			wantClear: false,
		},
		{
			name:   "a sentinel no remove takes",
			holder: fmt.Sprintf("pid=%d", dead),
			stage: func(t *testing.T, lockPath string) {
				sentinel := sentinelPath(lockPath)
				require.NoError(t, os.Mkdir(sentinel, 0o755))
				require.NoError(t, os.WriteFile(filepath.Join(sentinel, "kept"), nil, 0o644))
			},
			wantClear: false,
		},
		{
			name:      "no sentinel at all",
			holder:    fmt.Sprintf("pid=%d", dead),
			wantClear: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lockPath := filepath.Join(t.TempDir(), LockName)
			writeHolder(t, lockPath, tc.holder)
			if tc.stage != nil {
				tc.stage(t, lockPath)
			}
			assert.Equal(t, tc.wantClear, clearStaleSentinel(lockPath), "clearStaleSentinel(%q) with holder %q", lockPath, tc.holder)
			if tc.stage == nil {
				return
			}
			_, statErr := os.Stat(sentinelPath(lockPath))
			if tc.wantClear {
				assert.ErrorIs(t, statErr, os.ErrNotExist, "a cleared sentinel is gone")
				return
			}
			assert.NoError(t, statErr, "a sentinel left standing")
		})
	}
}

// stampLockHolder writes the holder's pid into the lock file it is handed. A
// writable file carries the stamp this process can be named by, and a file
// opened read-only is left exactly as it was, the stamp being advice for a
// reader of a held lock rather than the lock itself.
func TestLockCoverStampLockHolderNamesTheHolder(t *testing.T) {
	t.Parallel()

	stamped := filepath.Join(t.TempDir(), LockName)
	f, err := os.OpenFile(stamped, os.O_RDWR|os.O_CREATE, 0o644)
	require.NoError(t, err, "the lock file could not be opened for writing")
	stampLockHolder(f)
	require.NoError(t, f.Close(), "closing the stamped lock file")
	assert.Equal(t, strconv.Itoa(os.Getpid()), ReadLockHolder(stamped), "the stamped lock file does not name this process")

	sealed := filepath.Join(t.TempDir(), "sealed.lock")
	require.NoError(t, os.WriteFile(sealed, []byte("pid=1 at=2000-01-01T00:00:00Z\n"), 0o444))
	r, err := os.Open(sealed)
	require.NoError(t, err, "the sealed lock file could not be opened for reading")
	stampLockHolder(r)
	require.NoError(t, r.Close(), "closing the sealed lock file")
	assert.Equal(t, "1", ReadLockHolder(sealed), "a stamp that cannot truncate changed the record")
}

// ReadLockHolder answers "-" unless the record names a pid: the pid= token of
// the stamp wins, a bare number is the pid of an older record, and a record
// with no number in it names no holder at all.
func TestLockCoverReadLockHolder(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		record string
		want   string
	}{
		{name: "the stamp's pid token", record: "pid=4242 at=2000-01-01T00:00:00Z\n", want: "4242"},
		{name: "a bare pid", record: "77\n", want: "77"},
		{name: "a pid token that is no number", record: "pid=later at=now\n", want: "-"},
		{name: "a record with no number in it", record: "someone else\n", want: "-"},
		{name: "a record that is not there", record: "", want: "-"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), LockName)
			if tc.record != "" {
				require.NoError(t, os.WriteFile(path, []byte(tc.record), 0o644))
			}
			assert.Equal(t, tc.want, ReadLockHolder(path), "ReadLockHolder of %q", tc.record)
		})
	}
}

// A bus that is not a git checkout has nothing to lock: the take answers a
// release that is safe to defer and no error, and writes no lock file anywhere.
func TestLockCoverLockCheckoutWithoutAGitCheckout(t *testing.T) {
	t.Parallel()

	release, err := lockCheckoutAt(t.TempDir(), 0, nil)
	require.NoError(t, err, "a bus that is not a checkout was refused: %v", err)
	require.NotNil(t, release, "the nothing-to-lock answer has a release to defer")
	release()
}

// A lock file that cannot be opened is a refusal that names the lock, not a
// wait for a holder: the checkout's git directory holds a nova-bus.lock that
// is a directory, and the take refuses over it rather than working beside
// whatever left it there.
func TestLockCoverLockCheckoutRefusesAnUnopenableLock(t *testing.T) {
	t.Parallel()
	hermetic(t)

	clone := cloneBus(t, bareBus(t))
	gd, err := GitDir(clone)
	require.NoError(t, err, "the clone's git directory: %v", err)
	require.NoError(t, os.Mkdir(filepath.Join(gd, LockName), 0o755), "staging the unopenable lock")

	_, err = lockCheckoutAt(clone, 0, nil)
	require.Error(t, err, "a lock that cannot be opened was taken")
	assert.Contains(t, err.Error(), "could not be opened", "the refusal names the unopenable lock: %v", err)
	assert.NotErrorIs(t, err, ErrLockHeld, "an unopenable lock is not a held one")
}
