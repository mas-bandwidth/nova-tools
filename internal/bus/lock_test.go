package bus

import (
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/filelock"
	"github.com/stretchr/testify/require"
)

// One nova-bus per checkout, both ways: the second concurrent run waits and then refuses
// with a sentence, and the same second run takes the lock the moment the first lets go.
//
// The wait is short here and ten seconds in the binary. What is being asserted is the
// behaviour and the sentence, not the number, which is a policy the caller passes in.
func TestASecondRunOnOneCheckoutWaitsThenRefuses(t *testing.T) {
	t.Parallel()
	hermetic(t)
	bare := bareBus(t)
	clone := cloneBus(t, bare)

	release, err := LockCheckout(clone, 200*time.Millisecond)
	require.NoError(t, err, "the first run could not take the lock: %v", err)

	_, err = LockCheckout(clone, 200*time.Millisecond)
	require.Error(t, err, "two runs took one checkout's lock at once; they would write one OPEN list between them")
	for _, want := range []string{"another nova-bus is already running on this checkout", LockName, "run this again when that one has finished"} {
		require.Contains(t, err.Error(), want, "the refusal does not say %q: %v", want, err)
	}
	require.NotContains(t, err.Error(), "\n", "the refusal is more than one line: %q", err.Error())

	// The other way: once the first lets go, the second takes it.
	release()
	second, err := LockCheckout(clone, time.Second)
	require.NoError(t, err, "the lock was not released: %v", err)
	second()
	// Releasing twice is not an error, because a run releases through a defer and may also
	// have released on its own path out.
	release()

	// The lock is per CHECKOUT and not per bus name: a second clone of one bus is a
	// second checkout and must not be blocked by the first.
	other := cloneBus(t, bare)
	held, err := LockCheckout(clone, time.Second)
	require.NoError(t, err)
	defer held()
	elsewhere, err := LockCheckout(other, 200*time.Millisecond)
	require.NoError(t, err, "a second checkout of the same bus was blocked by the first: %v", err)
	elsewhere()
	// And it lives in the git directory, where it is not a file on the bus that every
	// reader would have to know is not a note.
	gd, err := GitDir(clone)
	require.NoError(t, err)
	{
		_, statErr := os.Stat(filepath.Join(gd, LockName))
		if statErr != nil {
			require.Equal(t, nil, statErr, "the lock is not at %s: %v", filepath.Join(gd, LockName), statErr)
		}
	}
}

// Two runs that genuinely race: whichever gets there second waits for the first rather than
// working beside it, and both eventually run.
func TestTwoConcurrentRunsSerialiseOnOneCheckout(t *testing.T) {
	t.Parallel()
	hermetic(t)
	bare := bareBus(t)
	clone := cloneBus(t, bare)

	var mu sync.Mutex
	inside := 0
	most := 0
	var wg sync.WaitGroup
	errs := make([]error, 2)
	// Each run's wait budget is an hour, so it outlasts the other run's hold, the
	// critical section between acquire and release.
	for i := range 2 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			release, err := LockCheckout(clone, time.Hour)
			if err != nil {
				errs[i] = err
				return
			}
			defer release()
			mu.Lock()
			inside++
			if inside > most {
				most = inside
			}
			mu.Unlock()
			// A hold of bounded WORK, not a wall-clock wait (a sleep here would be
			// the fixed wait the unit-tier rule refuses): a fixed count of atomic
			// adds keeps each run inside the lock long enough that a second run
			// taking it beside the first is caught by most.
			var hold int32
			for range 100000 {
				atomic.AddInt32(&hold, 1)
			}
			mu.Lock()
			inside--
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		require.NoError(t, err, "run %d: %v", i, err)
	}
	require.Equal(t, 1, most, "%d runs were inside the lock at once, want 1", most)
}

// The checkout lock stamps its holder's pid into the lock file, a take with wait=0 fails
// at once while the holder holds, and once the holder lets go the lock is taken again.
func TestTheCheckoutLockStampsItsHolderAndAWaitZeroTakeRefusesAtOnce(t *testing.T) {
	t.Parallel()
	hermetic(t)
	bare := bareBus(t)
	clone := cloneBus(t, bare)

	release, err := LockCheckout(clone, 0)
	require.NoError(t, err, "the first take failed: %v", err)
	defer release()

	// The holder is stamped with our PID (filelock's stamp), so a waiter and a refusal can name it.
	gd, err := GitDir(clone)
	require.NoError(t, err)
	stamp, err := filelock.ReadStamp(filepath.Join(gd, LockName))
	require.NoError(t, err, "the lock file's holder stamp: %v", err)
	require.Equal(t, os.Getpid(), stamp.PID, "holder pid = %d, want this process %d", stamp.PID, os.Getpid())

	// A second take with wait=0 must fail immediately: no wait, the refusal at once.
	_, err2 := LockCheckout(clone, 0)
	require.Error(t, err2, "a second take with wait=0 succeeded, want a refusal")
	require.Contains(t, err2.Error(), "another nova-bus is already running on this checkout", "err = %v, want the held refusal", err2)

	// Release the lock; the next take succeeds.
	release()
	release2, err3 := LockCheckout(clone, time.Second)
	require.Equal(t, nil, err3, "the take after release failed: %v", err3)
	defer release2()
}

// The lock file is created through O_EXCL and taken on a descriptor that refuses a
// symlink, so a symlink standing at the lock path is never locked through: whatever
// left it there is not worked beside silently.
func TestTheCheckoutLockRefusesASymlinkAtTheLockPath(t *testing.T) {
	t.Parallel()
	hermetic(t)
	clone := cloneBus(t, bareBus(t))
	gd, err := GitDir(clone)
	require.NoError(t, err, "the clone's git directory: %v", err)
	target := filepath.Join(t.TempDir(), "elsewhere")
	require.NoError(t, os.WriteFile(target, nil, 0o644), "staging the symlink's target")
	lockPath := filepath.Join(gd, LockName)
	require.NoError(t, os.Symlink(target, lockPath), "staging the symlink at the lock path")

	_, err = LockCheckout(clone, 0)
	require.Error(t, err, "the lock was taken through a symlink at the lock path")
	require.Contains(t, err.Error(), "could not be opened", "the refusal names the lock it could not open: %v", err)
}

// The stamp an earlier binary of this tool wrote -- a bare pid or "pid=<n> at=<stamp>"
// on one line -- is read by the same reader that reads filelock's own stamp, so a lock
// left by an old binary still names its holder.
func TestTheLockFileNamesTheHolderAnEarlierBinaryStamped(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tests := []struct {
		name   string
		record string
		want   int
	}{
		{name: "the old single-line stamp", record: "pid=4242 at=2000-01-01T00:00:00Z\n", want: 4242},
		{name: "a bare pid", record: "77\n", want: 77},
		{name: "a record with no pid in it", record: "someone else\n", want: 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(dir, strconv.Itoa(tc.want)+"-"+tc.name)
			require.NoError(t, os.WriteFile(path, []byte(tc.record), 0o644))
			st, err := filelock.ReadStamp(path)
			require.NoError(t, err, "reading the lock file %s: %v", path, err)
			require.Equal(t, tc.want, st.PID, "the pid read out of %q", tc.record)
		})
	}
}
