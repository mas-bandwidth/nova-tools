package cairn

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
)

// The three races a cold read ran as shell loops, each in a temp directory. A
// background writer re-points a path in a tight loop while a fixed number of
// appends run (the count bounds the test, no clock does). Whatever each append
// answers, the place a swap points at gains no byte, and an append that answered
// OK is in the store.

// race appends to session x of store while swap loops, until n appends have
// answered OK (refusals are fast and many, so the loop is bounded by a count of
// attempts too, never by a clock). It returns how many answered OK.
func race(store string, n int, swap func(i int)) (ok int) {
	quit, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; ; i++ {
			select {
			case <-quit:
				return
			default:
				swap(i)
				runtime.Gosched()
			}
		}
	}()
	for i := 0; ok < n && i < 20000; i++ {
		if _, err := Append(store, "x", "e"+strconv.Itoa(i), "words", "", benchNow, PublishManual); err == nil {
			ok++
		}
	}
	close(quit)
	<-done
	return ok
}

var spins atomic.Int64

// spin holds a state for a while without a clock: real work, not a wait.
func spin() {
	for i := 0; i < 100000; i++ {
		spins.Add(1)
	}
}

// repoint atomically makes link a symbolic link to target.
func repoint(link, target string) {
	os.Remove(link + ".tmp")
	if os.Symlink(target, link+".tmp") == nil {
		os.Rename(link+".tmp", link)
	}
}

func sections(path string) int {
	raw, _ := os.ReadFile(path)
	return strings.Count(string(raw), "\n## ")
}

func size(t *testing.T, path string) int64 {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return fi.Size()
}

// The record's own name alternates between the real file and a link to a file
// outside; the append must not follow the link the moment it appears.
func TestARecordSwappedForALinkNeverCarriesAWriteOutside(t *testing.T) {
	t.Parallel()
	for _, own := range []bool{false, true} {
		t.Run(map[bool]string{false: "bench", true: "own"}[own], func(t *testing.T) {
			t.Parallel()
			store := laidStore(t, own)
			record := benchFile(store, "x")
			if own {
				record = sessionFile(store, "x")
			}
			outside := filepath.Join(t.TempDir(), "outside.md")
			if err := os.WriteFile(outside, nil, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.Link(record, record+".real"); err != nil {
				t.Skipf("no hard links here: %v", err)
			}
			race(store, 8, func(i int) {
				if i%2 == 0 {
					repoint(record, outside)
				} else if os.Remove(record + ".tmp"); os.Link(record+".real", record+".tmp") == nil {
					os.Rename(record+".tmp", record)
				}
			})
			if n := size(t, outside); n != 0 {
				t.Fatalf("%d bytes reached the file outside the store", n)
			}
		})
	}
}

// An ancestor of the store path, not the store's own name, is re-pointed
// between two directories that both hold the store. The tool follows an ancestor
// it is given, so either may take an append; each append is in one of them or
// neither, and an OK append is there.
func TestAnAncestorOfTheStoreSwappedUnderAppendsLosesAndInventsNothing(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, d := range []string{"realpar", "evilpar"} {
		if err := os.MkdirAll(filepath.Join(root, d, "st"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, d, "st", "x.md"), []byte("# x\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	par := filepath.Join(root, "par")
	if err := os.Symlink("realpar", par); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	ok := race(filepath.Join(par, "st"), 8, func(i int) { repoint(par, []string{"evilpar", "realpar"}[i%2]) })
	if got := sections(filepath.Join(root, "realpar", "st", "x.md")) + sections(filepath.Join(root, "evilpar", "st", "x.md")); got != ok {
		t.Fatalf("%d appends answered OK and %d sections are in the two stores", ok, got)
	}
}

// The store is a real directory that is moved aside and replaced, for a moment,
// by a link to another directory; nothing reaches the other directory.
func TestAStoreSwappedForALinkNeverCarriesAWriteOutside(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	store, evil := filepath.Join(root, "st"), filepath.Join(root, "evil")
	for _, d := range []string{store, evil} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(evil, "x.md"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(store, "x.md"), []byte("# x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(evil, filepath.Join(root, "probe")); err != nil {
		t.Skipf("no symlinks here: %v", err)
	}
	ok := race(store, 8, func(int) {
		os.Rename(store, store+".real")
		os.Symlink(evil, store)
		spin() // the link stands long enough to be met
		os.Remove(store)
		os.Rename(store+".real", store)
		spin()
	})
	if n := size(t, filepath.Join(evil, "x.md")); n != 0 {
		t.Fatalf("%d bytes reached the directory the store was swapped for", n)
	}
	if got := sections(filepath.Join(store, "x.md")); got != ok {
		t.Fatalf("%d appends answered OK and %d sections are in the store", ok, got)
	}
}

// A directory the verb has checked and that then changes identity is refused,
// deterministically: the same name, another directory.
func TestADirectoryThatChangesAfterItWasCheckedIsRefused(t *testing.T) {
	t.Parallel()
	store := t.TempDir()
	dir := filepath.Join(store, "sessions")
	d := newDirs()
	if err := os.Mkdir(dir, 0o755); err != nil || d.checkDir(dir) != nil {
		t.Fatal("cannot check a directory")
	}
	if err := os.Rename(dir, filepath.Join(store, "moved")); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "x.md")
	if err := os.WriteFile(path, []byte("# imposter\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := d.openFile(path, os.O_RDWR|os.O_APPEND, 0)
	if err == nil {
		f.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "changed while the command was running") {
		t.Fatalf("want a refusal of a directory that changed, got %v", err)
	}
}
