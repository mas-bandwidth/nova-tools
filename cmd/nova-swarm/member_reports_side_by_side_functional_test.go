//go:build functional

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
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

// sideSprint is a sprint server as the member sees it (pkg/sprintwire: POST /verbs), holding
// four read cards dealt to member m1's fleet row (width 2: four reads fill its four half
// slots) and a fifth read ready behind them. Each `read --return` the member sends is held
// until all four are in flight at once (a barrier, bounded), and the one whose arrival made
// four is held further, until the test releases it: a member that reports one card after
// another never has four in flight and never takes the fifth read while one report is held.
type sideSprint struct {
	mu       sync.Mutex
	reads    []member.Packet // the four dealt first
	next     member.Packet   // the fifth, taken into a lane a report freed
	dealt    bool
	took     bool            // the fifth read was taken
	returned map[string]bool // each read whose `read --return` the server answered
	inFlight int
	most     int
	allIn    chan struct{} // closed when four returns are in flight at once
	hit      bool
	held     string        // the return whose arrival made four, held until release
	release  chan struct{} // closed by the test
	// heldAtTake is whether the held return was still in flight when the fifth read was
	// taken: the freed lane was filled without waiting for every report
	heldAtTake bool
}

// barrierWait bounds the barrier: a member that reports one after another meets it alone and
// is let through after this, so the serial member's four returns take four of these.
const barrierWait = 4 * time.Second

func (s *sideSprint) queue(args []string) sprintwire.Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	have := ""
	if i := slices.Index(args, "--have"); i >= 0 && i+1 < len(args) {
		have = args[i+1]
	}
	held := strings.Split(have, ",")
	var cards []map[string]any
	list := func(p member.Packet, taken bool) {
		if !taken {
			cards = append(cards, map[string]any{"id": p.Card, "table": "fleet", "row": "m1", "col": "ready"})
			return
		}
		if s.returned[p.Card] {
			return
		}
		c := map[string]any{"id": p.Card, "table": "fleet", "row": "m1", "col": "working", "gen": 1, "attempt": 1}
		if !slices.Contains(held, p.Card) {
			c["packet"] = p
		}
		cards = append(cards, c)
	}
	for _, p := range s.reads {
		list(p, s.dealt)
	}
	list(s.next, s.took)
	b, _ := json.Marshal(map[string]any{"as": "m1", "epoch": 1, "width": 2, "cards": cards, "machine": "RUNNING"}) // ignored: plain maps and packets marshal
	return sprintwire.Result{Stdout: string(b)}
}

func (s *sideSprint) take() sprintwire.Result {
	s.mu.Lock()
	defer s.mu.Unlock()
	var packets []member.Packet
	switch {
	case !s.dealt:
		s.dealt = true
		packets = s.reads
	case !s.took && len(s.returned) > 0:
		s.took = true
		s.heldAtTake = s.held != "" && !s.returned[s.held]
		packets = []member.Packet{s.next}
	}
	b, _ := json.Marshal(map[string]any{"packets": append([]member.Packet{}, packets...)}) // ignored: packets marshal
	return sprintwire.Result{Stdout: string(b)}
}

// giveBack answers one `read --return`, held at the barrier and, for the fourth, until the
// test releases it; the lock is never held while it waits.
func (s *sideSprint) giveBack(args []string) sprintwire.Result {
	card := ""
	if i := slices.Index(args, "--return"); i >= 0 && i+1 < len(args) {
		card = args[i+1]
	}
	s.mu.Lock()
	s.inFlight++
	s.most = max(s.most, s.inFlight)
	if s.inFlight == len(s.reads) && !s.hit {
		s.hit, s.held = true, card
		close(s.allIn)
	}
	allIn, isHeld := s.allIn, s.held == card
	s.mu.Unlock()
	select {
	case <-allIn:
	case <-time.After(barrierWait):
	}
	if isHeld {
		select {
		case <-s.release:
		case <-time.After(30 * time.Second):
		}
	}
	s.mu.Lock()
	s.inFlight--
	s.returned[card] = true
	s.mu.Unlock()
	return sprintwire.Result{}
}

func (s *sideSprint) verb(args []string) sprintwire.Result {
	switch {
	case args[0] == "queue":
		return s.queue(args)
	case args[0] == "take":
		return s.take()
	case args[0] == "read" && slices.Contains(args, "--return"):
		return s.giveBack(args)
	}
	return sprintwire.Result{}
}

func (s *sideSprint) ServeHTTP(w http.ResponseWriter, r *http.Request) {
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

// A real `nova-swarm member` process, over a sprint server, holds four ended cards at once:
// four reads whose stage fails twice (a remote repo with no bench mirror), each handed back
// with `read --return` after ReadStageRetry. The member reports them side by side: all four
// returns are in flight together, and while the last of them is still held by the server the
// member takes a fifth read into a lane a finished report freed. Before the fix the pass sent
// each report one after another and took only after the last: the four returns never met, and
// the held report kept every freed lane empty.
func TestARealMemberReportsItsEndedCardsSideBySideAndFillsAFreedLane(t *testing.T) {
	t.Parallel()
	began := time.Now()
	require.NoError(t, buildShared(), "building the binaries these tests run")
	root, home, xdg := t.TempDir(), t.TempDir(), t.TempDir()
	read := func(n int) member.Packet {
		// a remote repo no bench mirror holds, named after this test's own directory so no
		// /tmp/<repo>-mirror.git of another run can stand in for it (swarm.FindBenchMirror)
		repo := fmt.Sprintf("https://example.com/o/side-by-side-%d-%s.git", n, filepath.Base(root))
		return member.Packet{
			Card: fmt.Sprintf("p%d.r1.m1", n), Kind: "read", As: "m1", Primary: fmt.Sprintf("p%d", n), Worker: "m2",
			Attempt: 1, Gen: 1, Epoch: 1, Head: fullSha, WorkBranch: fmt.Sprintf("sprint/p%d", n),
			Brief: fmt.Sprintf("p%d: a card whose read cannot stage\nREPO: %s\n", n, repo),
		}
	}
	sp := &sideSprint{reads: []member.Packet{read(1), read(2), read(3), read(4)}, next: read(5),
		returned: map[string]bool{}, allIn: make(chan struct{}), release: make(chan struct{})}
	srv := httptest.NewServer(sp)
	defer srv.Close()
	var releaseOnce sync.Once
	releaseHeld := func() { releaseOnce.Do(func() { close(sp.release) }) }
	defer releaseHeld()

	// the store is this test's own server, named explicitly (--server), and the environment is
	// scrubbed: a private HOME and XDG_CONFIG_HOME and no NOVA_* variable but the one set here,
	// so no seat configuration on the host can forward a verb to a live sprint server
	cmd := exec.Command(builtTool, "member", "--as", "m1", "--server", strings.TrimPrefix(srv.URL, "http://"),
		"--harness", builtHarness, "--model", "p/m", "--tokens", "unmetered", "--deadline", "60s",
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
		releaseHeld()
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

	// the four reads fail staging, wait ReadStageRetry, fail again and are handed back; the
	// fifth is taken once a lane is free, all within the retry and a few one-second passes
	deadline := time.Now().Add(SideBySideBound)
	for time.Now().Before(deadline) {
		sp.mu.Lock()
		took := sp.took
		sp.mu.Unlock()
		if took {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	sp.mu.Lock()
	dealt, hit, most, took, heldAtTake, held := sp.dealt, sp.hit, sp.most, sp.took, sp.heldAtTake, sp.held
	sp.mu.Unlock()
	stop()

	logs := fmt.Sprintf("\nstdout:\n%s\nstderr:\n%s", out.String(), errb.String())
	require.True(t, dealt, "the member took the four reads"+logs)
	assert.True(t, hit, "all four returns were in flight at once (most %d); one after another they never meet%s", most, logs)
	assert.Equal(t, len(sp.reads), most, "the most returns in flight at once"+logs)
	require.True(t, took, "the member took the fifth read into a lane a report freed within %s%s", SideBySideBound, logs)
	assert.True(t, heldAtTake, "the fifth read was taken while %s's return was still in flight: a freed lane is filled without waiting for every report%s", held, logs)
	t.Logf("wall time %s", time.Since(began).Round(time.Millisecond))
}

// SideBySideBound is how long the functional test waits for the fifth read to be taken: the
// retry's wait and a few one-second passes, inside the 30 s a functional test may take.
const SideBySideBound = member.ReadStageRetry + 10*time.Second
