//go:build functional

package main

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// The bare-remote half of rule 16 under contention. receive-pack's automatic `git gc
// --auto` repacks a bare repository's objects directory after a push; in a detached
// maintenance process that keeps running once push returns, it mutates the remote
// asynchronously and a snapshot taken before it settles sees loose objects become a pack
// file, which is the intermittent "the remote changed" mutation #205 saw in CI. Eight
// writers push to one bare remote concurrently, and the remote's tree -- per relative path
// -- must not move once they have all returned. The fixture turns receive.autogc, gc.auto
// and maintenance.auto off (the repair), and this test pins that: any later change is one
// this tool may not make.
func TestConcurrentWritersDoNotMutateTheBareRemote(t *testing.T) {
	t.Parallel()

	realGit, _ := exec.LookPath("git")
	if realGit == "" || runtime.GOOS == "windows" {
		t.Skip("the fixture wants a real git to build the checkout")
	}
	dir := t.TempDir()
	bare := filepath.Join(dir, "remote.git")
	gitRun(t, realGit, dir, "init", "--bare", "-q", bare)
	gitRun(t, realGit, bare, "config", "receive.autogc", "false")
	gitRun(t, realGit, bare, "config", "gc.auto", "0")
	gitRun(t, realGit, bare, "config", "maintenance.auto", "false")

	const writers = 8
	buses := make([]string, writers)
	for i := range buses {
		buses[i] = mkdir(t, filepath.Join(dir, fmt.Sprintf("bus%d", i)))
	}
	for i, bus := range buses {
		gitRun(t, realGit, bus, "init", "-q")
		write(t, filepath.Join(bus, "note.md"), fmt.Sprintf("writer %d\n", i))
		gitRun(t, realGit, bus, "add", "-A")
		gitRun(t, realGit, bus, "commit", "-q", "-m", "writer")
		gitRun(t, realGit, bus, "remote", "add", "origin", bare)
	}

	errs := make(chan error, writers)
	var wg sync.WaitGroup
	for i, bus := range buses {
		wg.Add(1)
		go func(bus string, i int) {
			defer wg.Done()
			errs <- gitRunErr(realGit, bus, "push", "-q", "origin", fmt.Sprintf("HEAD:refs/heads/w%d", i))
		}(bus, i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}

	before := readTree(t, bare)
	deadline := time.Now().Add(time.Second)
	for {
		if after := readTree(t, bare); after.digest != before.digest {
			t.Errorf("the remote changed under concurrent writers; this tool does not push, fetch or talk to a network (rule 16): diff: %s", diffTrees(before, after))
			return
		}
		if time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
}
