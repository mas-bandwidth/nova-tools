//go:build functional

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

// roles/health/files/nova-disk-clean, run on a disposable tree: what it removes and what it must keep.

// cleanTree is a temp dir and a Go-cache-shaped dir, each entry's files and directories dated `old` ago unless fresh.
func cleanTree(t *testing.T) (tmp, cache string) {
	t.Helper()
	root := t.TempDir()
	tmp, cache = filepath.Join(root, "tmp"), filepath.Join(root, "go-build")
	old := time.Now().Add(-3 * time.Hour)
	for _, f := range []struct {
		path  string
		fresh bool
	}{
		{"tmp/stale/a/b.txt", false},
		{"tmp/stale-file", false},
		{"tmp/mapped/x.txt", false},
		{"tmp/held/x.txt", false},
		{"tmp/socket/x.txt", false},
		{"go-build/ab/old-d", false},
		{"go-build/ab/new-d", true},
		{"go-build/README", false},
	} {
		p := filepath.Join(root, f.path)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := syscall.Mknod(filepath.Join(tmp, "socket", "s"), syscall.S_IFSOCK|0o600, 0); err != nil {
		t.Fatal(err)
	}
	// date every path old, deepest first, then the fresh files again (a write dates its directory)
	var paths []string
	_ = filepath.Walk(root, func(p string, _ os.FileInfo, _ error) error { paths = append(paths, p); return nil }) // ignored: built above
	for i := len(paths) - 1; i > 0; i-- {
		if err := os.Chtimes(paths[i], old, old); err != nil {
			t.Fatal(err)
		}
	}
	for _, p := range []string{"go-build/ab/new-d"} {
		now := time.Now()
		if err := os.Chtimes(filepath.Join(root, p), now, now); err != nil {
			t.Fatal(err)
		}
	}
	return tmp, cache
}

func runClean(t *testing.T, env ...string) string {
	t.Helper()
	cmd := exec.Command("bash", "roles/health/files/nova-disk-clean")
	cmd.Env = append(os.Environ(), append([]string{"DISK_NO_LOG=1"}, env...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("nova-disk-clean: %v: %s", err, out)
	}
	return string(out)
}

// The age is judged by ctime, which nothing can set back, so a tree made just now with old mtimes (an unpacked archive)
// is kept, and a test reaches "old" with an age of 0 (no ctime is less than 0 minutes ago). The cache is judged by mtime,
// which Go refreshes on use.
func TestFunctionalDiskCleanRemovesOnlyWhatNothingUses(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("nova-disk-clean reads /proc and uses GNU find: Linux only")
	}
	tmp, cache := cleanTree(t)
	sleeper := exec.Command("sleep", "60")
	sleeper.Dir = filepath.Join(tmp, "held")
	if err := sleeper.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sleeper.Process.Kill(); _ = sleeper.Wait() }) // ignored: the test is over
	// mapped: a file this process maps and no longer holds open
	f, err := os.Open(filepath.Join(tmp, "mapped", "x.txt"))
	if err != nil {
		t.Fatal(err)
	}
	m, err := syscall.Mmap(int(f.Fd()), 0, 1, syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		t.Fatal(err)
	}
	_ = f.Close()                               // ignored: read-only
	t.Cleanup(func() { _ = syscall.Munmap(m) }) // ignored: the test is over
	dirs := []string{"DISK_CLEAN_DIRS=" + tmp, "DISK_WARN_PCT=100"}
	gone := []string{"stale", "stale-file"}
	kept := []string{"held", "socket", "mapped"}

	if out := runClean(t, append(dirs, "DISK_CLEAN_AGE_MIN=60")...); strings.Contains(out, "CLEAN /") {
		t.Errorf("a tree made just now with 3-hour-old mtimes went at a 60-minute age: %s", out)
	}
	out := runClean(t, append(dirs, "DISK_CLEAN_AGE_MIN=0", "DISK_CLEAN_DRY=1")...)
	for _, e := range gone {
		if !strings.Contains(out, "WOULD "+filepath.Join(tmp, e)+" ") {
			t.Errorf("dry run does not name %s: %s", e, out)
		}
	}
	if _, err := os.Stat(filepath.Join(tmp, "stale")); err != nil {
		t.Errorf("dry run removed stale: %v", err)
	}
	out = runClean(t, append(dirs, "DISK_CLEAN_AGE_MIN=0")...)
	for _, e := range gone {
		if _, err := os.Lstat(filepath.Join(tmp, e)); !os.IsNotExist(err) {
			t.Errorf("%s kept (%v): %s", e, err, out)
		}
	}
	for _, e := range kept {
		if _, err := os.Stat(filepath.Join(tmp, e)); err != nil {
			t.Errorf("%s removed: %v: %s", e, err, out)
		}
	}
	if !strings.Contains(out, "CLEAN-DONE ") {
		t.Errorf("no CLEAN-DONE line: %s", out)
	}

	caches := []string{"DISK_CLEAN_CACHES=" + cache, "DISK_WARN_PCT=100", "DISK_CLEAN_AGE_MIN=60"}
	if out := runClean(t, append(caches, "DISK_CLEAN_DRY=1")...); !strings.Contains(out, "WOULD "+cache+" 1 files ") {
		t.Errorf("dry run does not name the cache's one old file: %s", out)
	}
	out = runClean(t, caches...)
	for p, want := range map[string]bool{"ab/old-d": false, "ab/new-d": true, "README": true} {
		if _, err := os.Stat(filepath.Join(cache, p)); (err == nil) != want {
			t.Errorf("cache %s present=%v, want %v: %s", p, err == nil, want, out)
		}
	}
	if out := runClean(t, "DISK_CLEAN_CACHES="+tmp, "DISK_CLEAN_AGE_MIN=0"); !strings.Contains(out, "CLEAN-REFUSED "+tmp) {
		t.Errorf("a cache root not named go-build was not refused: %s", out)
	}
}

func TestFunctionalDiskCleanTakesThePressedAgeOverTheWarnPercent(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("nova-disk-clean reads /proc and uses GNU find: Linux only")
	}
	tmp, _ := cleanTree(t)
	env := []string{"DISK_CLEAN_DIRS=" + tmp, "DISK_CLEAN_AGE_MIN=2880", "DISK_CLEAN_PRESSED_MIN=0"}
	if out := runClean(t, append(env, "DISK_WARN_PCT=100")...); strings.Contains(out, "CLEAN /") {
		t.Errorf("under the warn percent, an entry went at a 2-day age: %s", out)
	}
	out := runClean(t, append(env, "DISK_WARN_PCT=-1")...)
	if _, err := os.Stat(filepath.Join(tmp, "stale")); !os.IsNotExist(err) {
		t.Errorf("over the warn percent, stale kept at the pressed age (%v): %s", err, out)
	}
	if !strings.Contains(out, " unchanged 0m") {
		t.Errorf("the lines do not name the pressed age: %s", out)
	}
}

// Never removed however old: a git repository (maybe unpushed work), quarantine, a pid or lock file, a FIFO.
func TestFunctionalDiskCleanKeepsWorkQuarantineAndProcessState(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("nova-disk-clean reads /proc and uses GNU find: Linux only")
	}
	tmp := filepath.Join(t.TempDir(), "tmp")
	old := time.Now().Add(-3 * time.Hour)
	for _, p := range []string{"checkout/.git/HEAD", "bare/HEAD", "q/nova-quarantine/x", "daemon/run.pid", "locked/a.lock", "stale/x"} {
		if err := os.MkdirAll(filepath.Join(tmp, filepath.Dir(p)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(tmp, p), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(tmp, "fifo"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(filepath.Join(tmp, "fifo", "p"), 0o600); err != nil {
		t.Fatal(err)
	}
	var paths []string
	_ = filepath.Walk(tmp, func(p string, _ os.FileInfo, _ error) error { paths = append(paths, p); return nil }) // ignored: built above
	for i := len(paths) - 1; i > 0; i-- {
		if err := os.Chtimes(paths[i], old, old); err != nil {
			t.Fatal(err)
		}
	}
	out := runClean(t, "DISK_CLEAN_DIRS="+tmp, "DISK_WARN_PCT=100", "DISK_CLEAN_AGE_MIN=0")
	for _, e := range []string{"checkout", "bare", "q", "daemon", "locked", "fifo"} {
		if _, err := os.Stat(filepath.Join(tmp, e)); err != nil {
			t.Errorf("%s removed: %v: %s", e, err, out)
		}
	}
	if _, err := os.Stat(filepath.Join(tmp, "stale")); !os.IsNotExist(err) {
		t.Errorf("stale kept (%v): %s", err, out)
	}
}

// A root that is the home or above it is refused whole; a root reached through a symlink is resolved first, so a held
// entry under it is still seen as held.
func TestFunctionalDiskCleanRefusesHomeAndResolvesSymlinkedRoots(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("nova-disk-clean reads /proc and uses GNU find: Linux only")
	}
	tmp, _ := cleanTree(t)
	home := filepath.Dir(tmp)
	out := runClean(t, "HOME="+filepath.Join(tmp, "stale"), "DISK_CLEAN_DIRS="+home, "DISK_WARN_PCT=100", "DISK_CLEAN_AGE_MIN=0")
	if !strings.Contains(out, "CLEAN-REFUSED "+home) {
		t.Errorf("an ancestor of the home was not refused: %s", out)
	}
	if _, err := os.Stat(filepath.Join(tmp, "stale")); err != nil {
		t.Errorf("an entry under a refused root went: %v: %s", err, out)
	}
	link := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(tmp, link); err != nil {
		t.Fatal(err)
	}
	sleeper := exec.Command("sleep", "60")
	sleeper.Dir = filepath.Join(tmp, "held")
	if err := sleeper.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sleeper.Process.Kill(); _ = sleeper.Wait() }) // ignored: the test is over
	out = runClean(t, "DISK_CLEAN_DIRS="+link, "DISK_WARN_PCT=100", "DISK_CLEAN_AGE_MIN=0")
	if _, err := os.Stat(filepath.Join(tmp, "held")); err != nil {
		t.Errorf("a held entry under a symlinked root went: %v: %s", err, out)
	}
	if _, err := os.Stat(filepath.Join(tmp, "stale")); !os.IsNotExist(err) {
		t.Errorf("stale kept under a symlinked root (%v): %s", err, out)
	}
}

// The work-clone clean: a home entry by a work name goes whole when nothing under it changed and nothing holds it; a
// container stays and its children are judged one by one; a friend's, sdk and a held clone stay. Age 0 reaches "old" (ctime).
func TestFunctionalDiskCleanWorkClones(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("nova-disk-clean reads /proc and uses GNU find: Linux only")
	}
	home := t.TempDir()
	for _, d := range []string{"rowan-old/.git", "rowan-held", "emma-work", "sdk", "other", "work/child-a", "work/emma-x"} {
		if err := os.MkdirAll(filepath.Join(home, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	sleeper := exec.Command("sleep", "60")
	sleeper.Dir = filepath.Join(home, "rowan-held")
	if err := sleeper.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sleeper.Process.Kill(); _ = sleeper.Wait() }) // ignored: the test is over
	env := []string{"DISK_CLEAN_WORK_HOME=" + home, "DISK_CLEAN_WORK_AGE_MIN=0", "DISK_CLEAN_WORK_NAMES=rowan-*:w",
		"DISK_CLEAN_WORK_CONTAINERS=work", "DISK_CLEAN_WORK_NEVER=emma-*:sdk"}
	if out := runClean(t, append(env, "DISK_CLEAN_DRY=1")...); !strings.Contains(out, "WOULD "+filepath.Join(home, "rowan-old")+" ") {
		t.Errorf("dry run does not name rowan-old: %s", out)
	}
	if _, err := os.Stat(filepath.Join(home, "rowan-old")); err != nil {
		t.Errorf("dry run removed rowan-old: %v", err)
	}
	out := runClean(t, env...)
	for p, want := range map[string]bool{"rowan-old": false, "work/child-a": false, "work": true, "rowan-held": true,
		"emma-work": true, "work/emma-x": true, "sdk": true, "other": true} {
		if _, err := os.Stat(filepath.Join(home, p)); (err == nil) != want {
			t.Errorf("%s present=%v, want %v: %s", p, err == nil, want, out)
		}
	}
	fresh := runClean(t, "DISK_CLEAN_WORK_HOME="+home, "DISK_CLEAN_WORK_AGE_MIN=60", "DISK_CLEAN_WORK_NAMES=*", "DISK_CLEAN_WORK_NEVER=sdk")
	if strings.Contains(fresh, "CLEAN /") {
		t.Errorf("a tree changed just now went at a 60-minute age: %s", fresh)
	}
}

// The Go build cache cap steps the age down to the floor (DISK_CLEAN_PRESSED_MIN) and never below it: a file a running
// build may use (Go refreshes an entry's mtime only past an hour) stays, and a cache still over the cap says so. Time is
// an argument: DISK_CLEAN_NOW is a fixed instant and every file is dated back from it, so no clock is read.
// hetzner2, 2026-11-24: the old last step, "unchanged 0m", removed 53,420 files under a running `go run`.
func TestFunctionalDiskCleanGoCacheCapKeepsTheFloor(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "linux" {
		t.Skip("nova-disk-clean reads /proc and uses GNU find: Linux only")
	}
	const now = 3_000_000_000 // seconds since the epoch, after the real now: the hourly age clean (real clock) passes them by
	at := func(minutesAgo int) time.Time { return time.Unix(now-int64(minutesAgo)*60, 0) }
	files := map[string]int{"ab/day-d": 2000, "ab/old-d": 400, "ab/live-d": 59, "ab/fresh-d": 5} // minutes before now
	build := func(t *testing.T) string {
		cache := filepath.Join(t.TempDir(), "go-build")
		if err := os.MkdirAll(filepath.Join(cache, "ab"), 0o755); err != nil {
			t.Fatal(err)
		}
		for p, ago := range files {
			f := filepath.Join(cache, p)
			if err := os.WriteFile(f, make([]byte, 64<<10), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Chtimes(f, at(ago), at(ago)); err != nil {
				t.Fatal(err)
			}
		}
		return cache
	}
	env := func(cache, capKB, floor string) []string {
		return []string{"DISK_CLEAN_CACHES=" + cache, "DISK_WARN_PCT=100", "DISK_CLEAN_AGE_MIN=2880",
			"DISK_CLEAN_PRESSED_MIN=" + floor, "DISK_CLEAN_GOCACHE_MAX_KB=" + capKB, "DISK_CLEAN_NOW=3000000000"}
	}
	present := func(cache string) map[string]bool {
		got := map[string]bool{}
		for p := range files {
			_, err := os.Stat(filepath.Join(cache, p))
			got[p] = err == nil
		}
		return got
	}

	// a cap no step can meet: down to the floor (360m) and no further, then CLEAN-OVER-CAP
	cache := build(t)
	out := runClean(t, env(cache, "1", "360")...)
	for p, want := range map[string]bool{"ab/day-d": false, "ab/old-d": false, "ab/live-d": true, "ab/fresh-d": true} {
		if present(cache)[p] != want {
			t.Errorf("over an unmeetable cap, %s present=%v, want %v (never below the 360m floor): %s", p, !want, want, out)
		}
	}
	if !strings.Contains(out, "CLEAN-OVER-CAP "+cache+" ") || !strings.Contains(out, "within 360m") {
		t.Errorf("a cache left over the cap at the floor was not said: %s", out)
	}
	for _, low := range []string{"unchanged 60m", "unchanged 0m"} {
		if strings.Contains(out, low) {
			t.Errorf("a cap step ran below the floor (%s): %s", low, out)
		}
	}

	// a cap the day step meets: nothing younger than a day goes and nothing over the cap is said
	cache = build(t)
	out = runClean(t, env(cache, "230", "360")...)
	for p, want := range map[string]bool{"ab/day-d": false, "ab/old-d": true, "ab/live-d": true, "ab/fresh-d": true} {
		if present(cache)[p] != want {
			t.Errorf("cap met at 1440m, %s present=%v, want %v: %s", p, !want, want, out)
		}
	}
	if strings.Contains(out, "CLEAN-OVER-CAP") || strings.Contains(out, "unchanged 360m") {
		t.Errorf("the cap was met at 1440m and the clean went on: %s", out)
	}

	// a floor under 120m is refused and nothing goes
	cache = build(t)
	out = runClean(t, env(cache, "1", "60")...)
	if !strings.Contains(out, "CLEAN-REFUSED cap 1KiB") {
		t.Errorf("a 60m floor was not refused: %s", out)
	}
	for p, ok := range present(cache) {
		if !ok {
			t.Errorf("a refused cap removed %s: %s", p, out)
		}
	}
}
