//go:build functional

package main

import (
	"bytes"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/member"
)

// A real `nova-swarm member` process, over a sprint server, is dealt a read whose child
// -- the harness -- dies at 134 (an abort an environment death, not a no-result). The
// member hands the read back with the child's real exit code and the harness that ran it
// (`read --return ... --exit 134 --harness <h>`), so the store tells the death from a
// no-result and never spends a redeal on one it could have named (nova-sprint #129, finish
// --exit/--harness; the member's a-member-reports-a-childs-exit-and-harness). The server is
// this test's own (--server) and the member's environment is scrubbed, so no seat on the
// host can forward a verb to a live sprint. Red before the fix: the 134 arrived as a bare
// no-result, the read --return carrying no --exit and no --harness.
func TestARealMemberReportsAChildThatExits134WithItsExitAndHarness(t *testing.T) {
	t.Parallel()
	require.NoError(t, buildShared(), "building the binaries these tests run")
	e := newCaseEnv(t, false)        // a local bare origin, staged with no mirror
	head := pushedWork(t, e)         // the head under review, on sprint/a-1.w1
	abort := e.script(t, "exit 134") // the child the member runs: a harness that aborts
	sp := &ghostSprint{read: member.Packet{
		Card: "a-1.r1.m1", Kind: "read", As: "m1", Primary: "a-1", Worker: "m2",
		Attempt: 1, Gen: 1, Epoch: 1, Head: head, WorkBranch: "sprint/a-1.w1",
		Brief: "a-1: a read whose child dies\nREPO: " + e.repoURL + "\n",
	}}
	srv := httptest.NewServer(sp)
	defer srv.Close()

	// the store is this test's own server, named explicitly (--server), and the environment
	// is scrubbed: a private HOME and XDG_CONFIG_HOME and no NOVA_* variable but the ones set
	// here, so no seat configuration on the host can forward a verb to a live sprint server
	root, home, xdg := t.TempDir(), t.TempDir(), t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, "slots"), 0o755))
	write(t, filepath.Join(root, "identity.tsv"), "owner\tname\temail\ntest-owner\tPool Worker\tpool@example.com\n")
	cmd := exec.Command(builtTool, "member", "--as", "m1", "--server", strings.TrimPrefix(srv.URL, "http://"),
		"--harness", abort, "--model", "p/m", "--tokens", "unmetered", "--deadline", "60s",
		"--root", root, "--every", "1s", "--no-wall", "--disk-floor", "0")
	env := []string{"HOME=" + home, "XDG_CONFIG_HOME=" + xdg, "PATH=" + builtPath, mirrorsEnv + "="}
	for _, kv := range os.Environ() {
		n, _, _ := strings.Cut(kv, "=")
		if n != "HOME" && n != "XDG_CONFIG_HOME" && n != "PATH" && !strings.HasPrefix(n, "NOVA_") {
			env = append(env, kv)
		}
	}
	cmd.Env = env
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	require.NoError(t, cmd.Start())
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		_ = cmd.Process.Signal(syscall.SIGTERM) // ignored: a member that already exited
		select {
		case <-exited:
		case <-time.After(30 * time.Second):
			_ = cmd.Process.Kill() // ignored: only this test's own process, by its pid
			<-exited
		}
	}
	defer stop()

	// the child aborts and the member hands the read back within a few one-second passes:
	// the stage is local and the harness exits at once
	bound := member.ReadStageRetry + 20*time.Second
	deadline := time.Now().Add(bound)
	var returned []string
	for time.Now().Before(deadline) {
		sp.mu.Lock()
		if len(sp.returned) > 0 {
			returned = slices.Clone(sp.returned)
			sp.mu.Unlock()
			break
		}
		sp.mu.Unlock()
		time.Sleep(100 * time.Millisecond)
	}
	stop()
	sp.mu.Lock()
	defer sp.mu.Unlock()
	logs := fmt.Sprintf("\nstdout:\n%s\nstderr:\n%s", out.String(), errb.String())
	require.True(t, sp.taken, "the member took the read"+logs)
	require.Len(t, returned, 1, "the read whose child aborted was handed back within %s; none means the member held it or reported it as a no-result"+logs, bound)
	assert.Contains(t, returned[0], "--exit 134", "the child's real exit code, an environment death the store can name"+logs)
	assert.Contains(t, returned[0], "--harness "+abort, "the harness that ran it"+logs)
}
