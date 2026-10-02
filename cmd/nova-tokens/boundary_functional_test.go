//go:build functional

// A real git and its detached maintenance: what this test waits for is another process
// (receive-pack's auto gc) settling, so it is a functional test, never a unit one.

package main

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The bare-remote half of rule 16 under contention. receive-pack's automatic `git gc
// --auto` repacks a bare repository's objects directory after a push; in a detached
// maintenance process that keeps running once push returns, it mutates the remote
// asynchronously and a snapshot taken before it settles sees loose objects become a pack
// file, which reads as an intermittent "the remote changed" mutation. Eight
// writers push to one bare remote concurrently, and the remote's tree -- per relative path
// -- must not move for a second once they have all returned. The fixture turns
// receive.autogc, gc.auto and maintenance.auto off (the repair, as
// TestNoVerbTouchesACheckoutOrItsRemote's does), and this test pins that: any later change
// is one this tool may not make. A detached process signals nothing, so the second is
// watched, not waited for.
func TestConcurrentWritersDoNotMutateTheBareRemote(t *testing.T) {
	t.Parallel()

	realGit, _ := exec.LookPath("git")
	if realGit == "" || runtime.GOOS == "windows" {
		t.Skip("the fixture wants a real git to build the checkout")
	}
	dir := t.TempDir()
	bare := filepath.Join(dir, "remote.git")
	gitRun(t, realGit, dir, "init", "--bare", "-q", bare)
	for _, kv := range [][2]string{{"receive.autogc", "false"}, {"gc.auto", "0"}, {"maintenance.auto", "false"}} {
		gitRun(t, realGit, bare, "config", kv[0], kv[1])
	}
	const writers = 8
	buses := make([]string, writers)
	for i := range buses {
		buses[i] = filepath.Dir(testkit.WriteFile(t, filepath.Join(dir, fmt.Sprintf("bus%d", i), "note.md"), fmt.Sprintf("writer %d\n", i)))
		for _, args := range [][]string{{"init", "-q"}, {"add", "-A"}, {"commit", "-q", "-m", "writer"}, {"remote", "add", "origin", bare}} {
			gitRun(t, realGit, buses[i], args...)
		}
	}
	errs := make([]error, writers)
	var wg sync.WaitGroup
	for i, bus := range buses {
		wg.Go(func() {
			errs[i] = gitRunErr(realGit, bus, "push", "-q", "origin", fmt.Sprintf("HEAD:refs/heads/w%d", i))
		})
	}
	wg.Wait()
	require.NoError(t, errors.Join(errs...))

	before := readTree(t, bare)
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if !assert.Equal(t, before, readTree(t, bare), "the remote changed under concurrent writers; this tool does not push, fetch or talk to a network (rule 16)") {
			return
		}
	}
}
