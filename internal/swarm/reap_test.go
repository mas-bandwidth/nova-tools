package swarm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// issue #1048: a native job downloads the Go toolchain and every module into its own
// sandboxed data home, up to 5 GB per slot. These tests pin the two halves of the fix: the
// child environment carries ONE cache root for every job under a root, and reap frees the
// finished slots' data while keeping the evidence.

func cacheEnvValue(t *testing.T, env []string, name string) string {
	t.Helper()
	for _, kv := range env {
		if n, v, _ := strings.Cut(kv, "="); n == name {
			return v
		}
	}
	t.Fatalf("%s is not in the child environment: %v", name, env)
	return ""
}

// The child env of two fake jobs on one root carries the same GOMODCACHE.
func TestTwoJobsOnOneRootShareTheGoModuleCache(t *testing.T) {
	root := t.TempDir()
	w := Worker{WorkerDir: filepath.Join(root, "worker")}
	a := childEnv(w, 1, "job-a", "", root)
	b := childEnv(w, 2, "job-b", "", root)
	for _, name := range []string{"GOMODCACHE", "GOCACHE", "NPM_CONFIG_CACHE"} {
		got, want := cacheEnvValue(t, a, name), cacheEnvValue(t, b, name)
		if got != want {
			t.Errorf("%s differs between two jobs on one root: %q vs %q", name, got, want)
		}
	}
	if got, want := cacheEnvValue(t, a, "GOMODCACHE"), GoModCacheDir(root); got != want {
		t.Errorf("GOMODCACHE=%q, want %q", got, want)
	}
	if got, want := cacheEnvValue(t, a, "GOCACHE"), GoBuildCacheDir(root); got != want {
		t.Errorf("GOCACHE=%q, want %q", got, want)
	}
}

func makeSlotFile(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, n), 0o644); err != nil {
		t.Fatal(err)
	}
}

func makeFinishedSlot(t *testing.T, root, slot string) (slotPath, jobPath string) {
	t.Helper()
	slotPath = filepath.Join(root, slot)
	jobPath = filepath.Join(slotPath, "jobs", "card-a")
	makeSlotFile(t, filepath.Join(slotPath, "data", "go-build.bin"), 4096)
	makeSlotFile(t, filepath.Join(slotPath, "tmp", "card-a", "scratch.tmp"), 1024)
	makeSlotFile(t, filepath.Join(jobPath, "scratch", "work"), 2048)
	makeSlotFile(t, filepath.Join(jobPath, "RESULT.md"), 128)
	makeSlotFile(t, filepath.Join(jobPath, "usage.tsv"), 64)
	makeSlotFile(t, filepath.Join(jobPath, "harness.log"), 32)
	return slotPath, jobPath
}

// Reap on a temp root with a finished slot removes data/ and keeps RESULT.md and reports
// freed bytes.
func TestReapRemovesAFinishedSlotAndKeepsTheEvidence(t *testing.T) {
	root := t.TempDir()
	slotPath, jobPath := makeFinishedSlot(t, root, "1")
	slots, freed, err := ReapSlots(ReapInput{Root: root, Older: time.Hour, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	if slots != 1 {
		t.Errorf("reaped slots=%d, want 1", slots)
	}
	if freed < 4096+1024+2048 {
		t.Errorf("freed=%d bytes, want at least the removed bytes", freed)
	}
	for _, gone := range []string{
		filepath.Join(slotPath, "data"),
		filepath.Join(slotPath, "tmp"),
		filepath.Join(jobPath, "scratch"),
	} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("%s was not removed (stat err=%v)", gone, err)
		}
	}
	for _, keep := range []string{"RESULT.md", "usage.tsv", "harness.log"} {
		if _, err := os.Stat(filepath.Join(jobPath, keep)); err != nil {
			t.Errorf("reap removed %s, which is evidence it must keep: %v", keep, err)
		}
	}
}

// A live slot (fresh log, no RESULT) is untouched.
func TestReapLeavesALiveSlotAlone(t *testing.T) {
	root := t.TempDir()
	slotPath := filepath.Join(root, "2")
	makeSlotFile(t, filepath.Join(slotPath, "data", "live.bin"), 512)
	makeSlotFile(t, filepath.Join(slotPath, "jobs", "card-b", "harness.log"), 16)
	slots, freed, err := ReapSlots(ReapInput{Root: root, Older: time.Hour, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	if slots != 0 || freed != 0 {
		t.Errorf("a live slot was reaped: slots=%d freed=%d", slots, freed)
	}
	if _, err := os.Stat(filepath.Join(slotPath, "data")); err != nil {
		t.Errorf("a live slot's data/ was removed: %v", err)
	}
}

// keep_data leaves data/ alone.
func TestReapKeepDataLeavesData(t *testing.T) {
	root := t.TempDir()
	slotPath, _ := makeFinishedSlot(t, root, "3")
	slots, freed := ReapAtTaskEnd(root, Worker{KeepData: true}, time.Now)
	if slots != 0 || freed != 0 {
		t.Errorf("keep_data reaped slots=%d freed=%d, want 0 and 0", slots, freed)
	}
	if _, err := os.Stat(filepath.Join(slotPath, "data")); err != nil {
		t.Errorf("keep_data must leave data/ alone: %v", err)
	}
}

// An old harness log with no RESULT is a finished slot too.
func TestReapRemovesASlotWhoseLogIsOlder(t *testing.T) {
	root := t.TempDir()
	slotPath := filepath.Join(root, "4")
	log := filepath.Join(slotPath, "jobs", "card-c", "harness.log")
	makeSlotFile(t, log, 32)
	makeSlotFile(t, filepath.Join(slotPath, "data", "old.bin"), 256)
	old := time.Now().Add(-3 * time.Hour)
	if err := os.Chtimes(log, old, old); err != nil {
		t.Fatal(err)
	}
	slots, _, err := ReapSlots(ReapInput{Root: root, Older: time.Hour, Now: time.Now})
	if err != nil {
		t.Fatal(err)
	}
	if slots != 1 {
		t.Errorf("reaped slots=%d, want 1 for a slot whose log is older than --older", slots)
	}
	if _, err := os.Stat(filepath.Join(slotPath, "data")); !os.IsNotExist(err) {
		t.Errorf("the stale slot's data/ was not removed")
	}
}

// A reaped target that is a symlink out of the slot is a derivation the reaper must not
// act on: safepath.RemoveUnder refuses the link, and the directory it pointed at survives.
func TestReapRefusesASlotScratchSymlinkToOutside(t *testing.T) {
	root := t.TempDir()
	slotPath := filepath.Join(root, "5")
	jobPath := filepath.Join(slotPath, "jobs", "card-e")
	makeSlotFile(t, filepath.Join(slotPath, "data", "keep.bin"), 256)
	makeSlotFile(t, filepath.Join(jobPath, "RESULT.md"), 64)

	outside := t.TempDir()
	makeSlotFile(t, filepath.Join(outside, "survivor"), 128)
	link := filepath.Join(jobPath, "scratch")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}

	if _, _, err := ReapSlots(ReapInput{Root: root, Older: time.Hour, Now: time.Now}); err == nil {
		t.Fatalf("a slot scratch that is a symlink out of the slot was removed; it must be refused")
	}
	if _, err := os.Stat(filepath.Join(outside, "survivor")); err != nil {
		t.Errorf("the directory outside the slot was removed: %v", err)
	}
	if fi, err := os.Lstat(link); err != nil || fi.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the refused symlink %s did not survive: err=%v", link, err)
	}
}
