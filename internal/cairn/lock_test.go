package cairn

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/filelock"
)

// fakeClock is time the test owns: sleeping advances it and runs a hook, so a
// ten-second wait takes no time at all and the test decides what happens in it.
type fakeClock struct {
	t      time.Time
	sleeps int
	onNap  func(n int)
}

func (f *fakeClock) clock() lockClock {
	return lockClock{
		now: func() time.Time { return f.t },
		sleep: func(d time.Duration) {
			f.sleeps++
			f.t = f.t.Add(d)
			if f.onNap != nil {
				f.onNap(f.sleeps)
			}
		},
		poll: lockPoll,
	}
}

// A store whose lock another process holds refuses the append after the bounded
// wait, naming the holder; nothing is written and no real time passes.
func TestAppendRefusesNamingTheHolderWhenTheLockStaysHeld(t *testing.T) {
	t.Parallel()
	store := openedBenchStore(t)
	held, err := filelock.TryLock(filepath.Join(store, lockFileName), "a test holder")
	if err != nil {
		t.Fatal(err)
	}
	defer held.Unlock()
	before, _ := os.ReadFile(benchFile(store, "NEW"))
	fc := &fakeClock{t: benchNow}
	_, err = appendOn(fc.clock(), lockWait, store, "NEW", "e1", "words", "", benchNow, PublishManual)
	var le *LockedError
	if !errors.As(err, &le) {
		t.Fatalf("want a LockedError, got %v", err)
	}
	for _, want := range []string{"cannot append an entry", "holds the lock on store", "a test holder", "pid=" + strconv.Itoa(os.Getpid()), "10s", "run the same command again"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q lacks %q", err, want)
		}
	}
	if fc.sleeps != int(lockWait/lockPoll) {
		t.Fatalf("polled %d times, want %d", fc.sleeps, int(lockWait/lockPoll))
	}
	if after, _ := os.ReadFile(benchFile(store, "NEW")); string(after) != string(before) {
		t.Fatal("a refused append wrote")
	}
}

// The wait ends as soon as the holder lets go: the append then reads what the
// holder wrote, decides on it, and writes.
func TestAppendWaitsForTheHolderAndThenReadsWhatItWrote(t *testing.T) {
	t.Parallel()
	store := openedBenchStore(t)
	held, err := filelock.TryLock(filepath.Join(store, lockFileName), "a test holder")
	if err != nil {
		t.Fatal(err)
	}
	fc := &fakeClock{t: benchNow}
	fc.onNap = func(n int) {
		if n == 3 { // the holder files e1 and lets go
			if _, err := appendLocked(store, "NEW", "e1", "the holder's words", "", benchNow, PublishManual); err != nil {
				t.Errorf("holder: %v", err)
			}
			held.Unlock()
		}
	}
	_, err = appendOn(fc.clock(), lockWait, store, "NEW", "e1", "other words", "", benchNow, PublishManual)
	var ce *ConflictError
	if !errors.As(err, &ce) || fc.sleeps != 3 {
		t.Fatalf("after waiting %d polls want a conflict with what the holder wrote, got %v", fc.sleeps, err)
	}
	res, err := appendOn(fc.clock(), lockWait, store, "NEW", "e1", "the holder's words", "", benchNow, PublishManual)
	if err != nil || !res.Duplicate {
		t.Fatalf("same words after the holder: %+v %v", res, err)
	}
}

// A store directory that cannot be written cannot hold the lock file: the
// append refuses naming the cause and the next action, and writes nothing.
func TestAppendRefusesNamingTheCauseInAReadOnlyStoreDirectory(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("a read-only directory does not stop this account")
	}
	store := openedBenchStore(t)
	if err := os.Chmod(store, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(store, 0o755)
	if _, err := os.OpenFile(filepath.Join(store, "probe"), os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		t.Skip("the directory stayed writable")
	}
	before, _ := os.ReadFile(benchFile(store, "NEW"))
	_, err := Append(store, "NEW", "e1", "words", "", benchNow, PublishManual)
	var le *LockedError
	if !errors.As(err, &le) {
		t.Fatalf("want a LockedError, got %v", err)
	}
	for _, want := range []string{"cannot append an entry", "cannot create the lock file", "permission denied on", store, "make the store directory writable"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("%q lacks %q", err, want)
		}
	}
	if after, _ := os.ReadFile(benchFile(store, "NEW")); string(after) != string(before) {
		t.Fatal("a refused append wrote")
	}
}
