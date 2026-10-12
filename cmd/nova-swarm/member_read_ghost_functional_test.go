//go:build functional

package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/pkg/member"
	"github.com/mas-bandwidth/nova-tools/pkg/sprintwire"
)

// ghostSprint is a sprint server as the member sees it (pkg/sprintwire: POST /verbs), holding
// one read card dealt to member m1's fleet row. It answers as nova-sprint's server does in the
// one way that matters here: the queue sends a working card's packet only when the member
// does not name it in --have (the member's queue sends no packet for a card it holds).
type ghostSprint struct {
	mu       sync.Mutex
	read     member.Packet
	taken    bool     // the take handed the read out
	returned []string // each `read --return` the member sent, as one line
	reported []string // each read verdict the member sent
	resent   int      // packets the queue sent for the read after the take
	queues   []string // every queue verb, as one line
}

func (s *ghostSprint) verb(args []string) sprintwire.Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	ok := func(v any) sprintwire.Result {
		b, _ := json.Marshal(v) // ignored: plain maps and packets marshal
		return sprintwire.Result{Stdout: string(b)}
	}
	switch {
	case args[0] == "queue":
		s.queues = append(s.queues, strings.Join(args, " "))
		var cards []map[string]any
		if s.taken && len(s.returned) == 0 && len(s.reported) == 0 {
			c := map[string]any{"id": s.read.Card, "table": "fleet", "row": "m1", "col": "working", "gen": 1, "attempt": 1}
			have := ""
			if i := slices.Index(args, "--have"); i >= 0 && i+1 < len(args) {
				have = args[i+1]
			}
			if !slices.Contains(strings.Split(have, ","), s.read.Card) {
				p := s.read
				c["packet"] = p
				s.resent++
			}
			cards = append(cards, c)
		} else if !s.taken {
			cards = append(cards, map[string]any{"id": s.read.Card, "table": "fleet", "row": "m1", "col": "ready"})
		}
		return ok(map[string]any{"as": "m1", "epoch": 1, "width": 2, "cards": cards, "machine": "RUNNING"})
	case args[0] == "take":
		if s.taken {
			return ok(map[string]any{"packets": []member.Packet{}})
		}
		s.taken = true
		return ok(map[string]any{"packets": []member.Packet{s.read}})
	case args[0] == "read" && slices.Contains(args, "--return"):
		s.returned = append(s.returned, strings.Join(args, " "))
	case args[0] == "read":
		s.reported = append(s.reported, strings.Join(args, " "))
	}
	return sprintwire.Result{}
}

func (s *ghostSprint) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req sprintwire.Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var resp sprintwire.Response
	for _, v := range req.Verbs {
		resp.Results = append(resp.Results, s.verb(v))
	}
	_ = json.NewEncoder(w).Encode(resp) // ignored: a member that hung up reads nothing
}

// A real `nova-swarm member` process, over a sprint server, is dealt a read whose stage
// fails (a remote repo with no bench mirror, as native stages it: no child runs). The member
// waits ReadStageRetry and runs the read again from its own packet, though the queue sends
// it none (it names the read in --have); the second stage failure hands the read back with
// `read --return`. Before the fix (nova-sprint #112, ported) the retry waited for the queue's
// packet, which never came: the member held the read with no child, named in every queue's
// --have, until the server's 60-minute read deadline, a ghost (2026-10-11, about 20 of 42
// cards in review).
func TestARealMemberRunsAReadWhoseStageFailedAgainNeverHoldsItAsAGhost(t *testing.T) {
	t.Parallel()
	began := time.Now()
	require.NoError(t, buildShared(), "building the binaries these tests run")
	root, home := t.TempDir(), t.TempDir()
	// a remote repo no bench mirror holds, named after this test's own directory so no
	// /tmp/<repo>-mirror.git of another run can stand in for it (swarm.FindBenchMirror):
	// staging refuses it before any network is reached
	repo := "https://example.com/o/ghost-read-" + filepath.Base(root) + ".git"
	sp := &ghostSprint{read: member.Packet{
		Card: "p1.r1.m1", Kind: "read", As: "m1", Primary: "p1", Worker: "m2", Attempt: 1, Gen: 1, Epoch: 1,
		Head: fullSha, WorkBranch: "sprint/p1",
		Brief: "p1: a card whose read cannot stage\nREPO: " + repo + "\n",
	}}
	srv := httptest.NewServer(sp)
	defer srv.Close()

	cmd := exec.Command(builtTool, "member", "--as", "m1", "--server", strings.TrimPrefix(srv.URL, "http://"),
		"--harness", builtHarness, "--model", "p/m", "--tokens", "unmetered", "--deadline", "60s",
		"--root", root, "--every", "1s", "--no-wall", "--disk-floor", "0")
	env := []string{"HOME=" + home, "PATH=" + builtPath, mirrorsEnv + "="}
	for _, kv := range os.Environ() {
		if n, _, _ := strings.Cut(kv, "="); n != "HOME" && n != "PATH" && n != mirrorsEnv {
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
		// the wait is on the member's exit; the allowance is only the safety net past it, at
		// the wall-clock law's thirty seconds (a drained member exits within a pass)
		select {
		case <-exited:
		case <-time.After(30 * time.Second):
			_ = cmd.Process.Kill() // ignored: only this test's own process, by its pid
			<-exited
		}
	}
	defer stop()

	// the read is handed back within the retry and a few passes, never held to the deadline
	bound := ReadGhostBound
	deadline := time.Now().Add(bound)
	for time.Now().Before(deadline) {
		sp.mu.Lock()
		done := len(sp.returned) > 0 || len(sp.reported) > 0
		sp.mu.Unlock()
		if done {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	// what the member did while it ran, read before it is stopped: a stopped member drains,
	// and a drain hands back every read it holds, a ghost too, which is not what is under test
	sp.mu.Lock()
	returned := slices.Clone(sp.returned)
	sp.mu.Unlock()
	stop()
	sp.mu.Lock()
	defer sp.mu.Unlock()
	require.True(t, sp.taken, "the member took the read\nstdout:\n%s\nstderr:\n%s", out.String(), errb.String())
	require.Len(t, returned, 1, "the read whose stage failed was run again from the member's own packet and, failing again, handed back within %s; none means the member held it with no child, a ghost\nstdout:\n%s\nstderr:\n%s", bound, out.String(), errb.String())
	assert.Contains(t, returned[0], "read --as m1 --return p1.r1.m1 --reason=")
	assert.Contains(t, returned[0], "staging refused: no bench mirror for "+repo, "the stage's own reason")
	assert.Empty(t, sp.reported, "no verdict: neither run staged")
	assert.Zero(t, sp.resent, "the queue never sent the read's packet again: the member named it in --have throughout, so the retry ran from its own packet")
	assert.Equal(t, 1, strings.Count(out.String(), "read p1.r1.m1: stage failed ("), "the first stage failure waits ReadStageRetry, said once\n%s", out.String())
	t.Logf("wall time %s", time.Since(began).Round(time.Millisecond))
}

// ReadGhostBound is how long the functional test waits for the read to be handed back: the
// retry's wait and a few one-second passes, inside the 30 s a functional test may take.
const ReadGhostBound = member.ReadStageRetry + 10*time.Second
