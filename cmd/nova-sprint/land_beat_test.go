package main

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/bench"
)

// beatBuf is the land loop's stdout. The test reads it from another goroutine.
type beatBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *beatBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *beatBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

// The land loop prints one line a cycle while a landing runs, raises one stuck
// judgment naming the gate and the go command it waits on, and sends that gate
// to an up bench's Go lane. The bench is a fake: the live fleet is not touched.
func TestTheLandLoopBeatsAndRaisesAStuckLanding(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.git(r.worker, "switch", "-q", "--detach", "origin/main")
	r.files("the module", goModule)
	r.files("a red base", map[string]string{"bad.go": buildRed})
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
	r.git(r.worker, "fetch", "-q", "origin")
	r.ok("add --stream s1 --count 1 --one")
	heads := map[string]string{"s1-1": r.card("s1-1", map[string]string{"ok.go": "package main\n\nfunc ok() {}\n"})}
	r.queued(heads, "s1-1")

	r.ok("fleet beat vision --load 1 --cores 8")
	r.ok("fleet up vision")
	var w whereView
	r.json("where", &w)
	for name, row := range w.Tables["fleet"] {
		if name != "vision" && row["status"] == sprint.Up {
			r.ok("fleet down " + name)
		}
	}
	r.json("where", &w)
	require.Equal(t, sprint.Up, w.Tables["fleet"]["vision"]["status"], "vision is the bench: %+v", w.Tables["fleet"]["vision"])

	var clockMu sync.Mutex
	now := r.a.now()
	r.a.now = func() time.Time {
		clockMu.Lock()
		defer clockMu.Unlock()
		return now
	}

	type call struct {
		host    string
		argv    string
		withGit bool
		runs    int
	}
	var callMu sync.Mutex
	var calls []call
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	b := r.a.landState()
	b.mu.Lock()
	b.hostName = func() (string, error) { return "studio.local", nil }
	b.landMore = []string{"--repo-dir", r.clone, "--base", "main"}
	b.gateBench = func(ctx context.Context, host, dir string, runs [][]string, withGit bool) (string, int, error) {
		joined := strings.Join(runs[0], " ")
		callMu.Lock()
		calls = append(calls, call{host, joined, withGit, len(runs)})
		nBuild := 0
		for _, c := range calls {
			if c.argv == "go build ./..." {
				nBuild++
			}
		}
		callMu.Unlock()
		// the first gate (the base's) blocks; a later one is the card's own gate
		if joined == "go build ./..." && nBuild == 1 {
			once.Do(func() { close(entered) })
			select {
			case <-release:
			case <-ctx.Done():
				return "", 1, ctx.Err()
			}
		}
		_ = dir
		return "", 0, nil
	}
	b.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out beatBuf
	parked := make(chan struct{})
	wake := make(chan struct{})
	var sleepMu sync.Mutex
	var jumped bool
	r.a.sleep = func(d time.Duration) {
		if !sleepMu.TryLock() {
			return // a beat inside a command this cycle runs
		}
		defer sleepMu.Unlock()
		if ctx.Err() != nil {
			return
		}
		if !jumped {
			clockMu.Lock()
			now = now.Add(d)
			clockMu.Unlock()
		}
		select {
		case <-ctx.Done():
			return
		case parked <- struct{}{}:
		}
		select {
		case <-ctx.Done():
		case <-wake:
		}
	}
	loopDone := make(chan struct{})
	go func() {
		r.a.landLoop(ctx, "mem:0", &out)
		close(loopDone)
	}()
	park := func() bool {
		t.Helper()
		select {
		case <-parked:
			return false
		case <-loopDone:
			return true
		case <-t.Context().Done():
			return true
		}
	}
	nudge := func() bool {
		t.Helper()
		select {
		case wake <- struct{}{}:
		case <-loopDone:
			return true
		case <-ctx.Done():
			return true
		}
		return park()
	}

	sawEntered := false
	select {
	case <-entered:
		sawEntered = true
	case <-loopDone:
	case <-t.Context().Done():
	}
	if !sawEntered {
		select {
		case <-entered:
			sawEntered = true
		default:
		}
	}
	require.True(t, sawEntered, "the fake bench never saw the gate\n%s", out.String())
	require.False(t, park(), "the loop ended before a beat\n%s", out.String())
	laneSnap := r.ok("lane list")
	clockMu.Lock()
	now = now.Add(LandDeadline)
	clockMu.Unlock()
	jumped = true
	require.False(t, nudge(), "the loop ended before the stuck beat\n%s", out.String())
	for range 2 {
		require.False(t, nudge(), "the loop ended while the gate was held\n%s", out.String())
	}
	close(release)

	steps := 0
	tooLong := false
	for {
		if nudge() {
			break
		}
		text := out.String()
		if strings.Contains(text, "LAND REFUSED") || strings.Contains(text, "LAND FAILED") ||
			(strings.Contains(text, "LAND OK") && strings.Contains(text, "bench=vision")) {
			cancel()
			nudge()
			break
		}
		steps++
		if steps > 100000 {
			tooLong = true
			cancel()
			nudge()
			break
		}
	}
	select {
	case <-loopDone:
	case <-t.Context().Done():
	}
	text := out.String()
	assert.False(t, tooLong, "the landing did not finish\n%s", text)
	assert.Contains(t, laneSnap, "machine=vision", "lane while the gate waited: %s", laneSnap)
	assert.Contains(t, laneSnap, "held=lander", "lane while the gate waited: %s", laneSnap)

	callMu.Lock()
	got := append([]call(nil), calls...)
	callMu.Unlock()
	var sawBuild bool
	for _, c := range got {
		assert.Equal(t, "vision", c.host, "gate host: %+v", got)
		if c.argv == "go build ./..." {
			sawBuild = true
		}
	}
	// the base's gate asks the tree tests, which read the history: its one bench run
	// takes the clone's .git with the tree (a copy without it fails internal/ci)
	if assert.NotEmpty(t, got) {
		assert.True(t, got[0].withGit, "the base's gate copies .git: %+v", got[0])
	}
	assert.True(t, sawBuild, "the fake never ran go build ./...: %+v", got)

	inbox := r.ok("inbox")
	assert.Equal(t, 1, strings.Count(inbox, sprint.NOpStuck), "one stuck judgment:\n%s", inbox)
	assert.Equal(t, 1, strings.Count(inbox, "landing stuck at step=gate"), "one stuck line:\n%s", inbox)
	assert.Contains(t, inbox, "go build ./...", "the judgment names the process:\n%s", inbox)

	assert.Regexp(t, regexp.MustCompile(`LAND OK stream=s1 .* bench=vision wall=\d+\.\ds`), text)
	idle := regexp.MustCompile(`LAND IDLE queued=([1-9]\d*) step=gate since=(\S+)`)
	var long bool
	for _, m := range idle.FindAllStringSubmatch(text, -1) {
		d, err := time.ParseDuration(m[2])
		require.NoError(t, err, m[2])
		if d >= LandDeadline {
			long = true
		}
	}
	assert.True(t, long, "an IDLE line while the gate ran past the deadline:\n%s", text)
	assert.NotContains(t, text, "LAND DONE", "the loop does not reprint LAND DONE")
	r.clean()
}

// A configured remote bench that does not answer refuses the gate without running Go here.
func TestTheLandLoopRefusesWhenTheBenchDoesNotAnswer(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.git(r.worker, "switch", "-q", "--detach", "origin/main")
	r.files("the module", goModule)
	r.files("a red base", map[string]string{"bad.go": buildRed})
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
	r.git(r.worker, "fetch", "-q", "origin")
	r.ok("add --stream s1 --count 1 --one")
	heads := map[string]string{"s1-1": r.card("s1-1", map[string]string{"ok.go": "package main\n\nfunc ok() {}\n"})}
	r.queued(heads, "s1-1")
	bin := t.TempDir()
	marker := filepath.Join(bin, "local-go-ran")
	require.NoError(t, os.WriteFile(filepath.Join(bin, "go"), []byte("#!/bin/sh\nprintf ran > \"$GATE_MARKER\"\nexit 1\n"), 0o755))
	r.a.gitEnv = append(r.env, "PATH="+bin+":"+os.Getenv("PATH"), "GATE_MARKER="+marker, "GOCACHE="+t.TempDir())
	r.ok("fleet beat vision --load 1 --cores 8")
	r.ok("fleet up vision")
	var w whereView
	r.json("where", &w)
	for name, row := range w.Tables["fleet"] {
		if name != "vision" && row["status"] == sprint.Up {
			r.ok("fleet down " + name)
		}
	}

	var asked sync.Mutex
	var benches []string
	b := r.a.landState()
	b.mu.Lock()
	b.hostName = func() (string, error) { return "studio.local", nil }
	b.landMore = []string{"--repo-dir", r.clone, "--base", "main"}
	b.gateBench = func(ctx context.Context, host, dir string, runs [][]string, withGit bool) (string, int, error) {
		asked.Lock()
		benches = append(benches, host)
		asked.Unlock()
		return "", 0, bench.ErrNoBench
	}
	b.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out beatBuf
	deadline := time.Now().Add(2 * time.Minute)
	// the loop's sleep yields until the landing in flight is done, so each cycle has
	// something new to say; it ends the loop once the landing's line is out
	r.a.sleep = func(time.Duration) {
		text := out.String()
		if strings.Contains(text, "LAND REFUSED") || strings.Contains(text, "LAND OK") || time.Now().After(deadline) {
			cancel()
			return
		}
		for time.Now().Before(deadline) {
			b.mu.Lock()
			f := b.flight
			b.mu.Unlock()
			if f == nil {
				return
			}
			f.mu.Lock()
			done := f.done
			f.mu.Unlock()
			if done {
				return
			}
			runtime.Gosched()
		}
	}
	r.a.landLoop(ctx, "mem:0", &out)
	text := out.String()

	asked.Lock()
	got := append([]string(nil), benches...)
	asked.Unlock()
	assert.NotEmpty(t, got, "the gate asked the bench first:\n%s", text)
	for _, h := range got {
		assert.Equal(t, "vision", h, "the only bench up: %v", got)
	}
	assert.Contains(t, text, "the configured remote bench did not run the tree gate")
	assert.Contains(t, text, "no card is blamed and nothing was pushed or reported")
	assert.NotContains(t, text, "fails the tree gate at its tip", "bench infrastructure is not a red base:\n%s", text)
	assert.NotContains(t, text, "bench=here", "the gate must not run on this host:\n%s", text)
	assert.NotContains(t, text, "bench=vision", "a bench that did not answer ran nothing:\n%s", text)
	_, err := os.Stat(marker)
	assert.True(t, os.IsNotExist(err), "local Go executed after the remote bench refused: %v", err)
	assert.NotContains(t, r.ok("lane list"), "lander", "the lane was given back")
	r.clean()
}

// A bench refusal is not cached as a class failure: the next pass can gate that base.
func TestUnavailableBenchLeavesTheBaseGateRetryOpen(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	for file, content := range goModule {
		path := filepath.Join(r.clone, file)
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o755))
		require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	}
	r.ok("fleet beat vision --load 1 --cores 8")
	r.ok("fleet up vision")
	var w whereView
	r.json("where", &w)
	for name, row := range w.Tables["fleet"] {
		if name != "vision" && row["status"] == sprint.Up {
			r.ok("fleet down " + name)
		}
	}
	b := r.a.landState()
	b.mu.Lock()
	b.flight = &landFlight{}
	b.hostName = func() (string, error) { return "coordinator.local", nil }
	b.gateBench = func(context.Context, string, string, [][]string, bool) (string, int, error) {
		return "", 0, bench.ErrNoBench
	}
	b.mu.Unlock()
	st, err := r.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(t, err)
	l := &lander{a: r.a, st: st, gateKey: "s1", base: "main"}
	var why string
	var stop bool
	for range 2 {
		why, stop = l.treeGateBase(t.Context(), r.clone, "base-1")
		assert.Equal(t, benchGateUnavailableWhy, why)
		assert.False(t, stop)
	}
	assert.Empty(t, l.baseGateFails, "bench refusal must not spend a red-base retry")
	assert.Empty(t, l.baseGateCache, "bench refusal must not be cached")
	_, _, env, why := l.gateBase(t.Context(), r.clone, "s1", []landCard{{base: "main"}}, "base-1")
	assert.Equal(t, benchGateUnavailableWhy, env)
	assert.Empty(t, why)
	assert.Empty(t, l.baseGateFails)
	b.mu.Lock()
	b.gateBench = func(context.Context, string, string, [][]string, bool) (string, int, error) {
		return "build failed", 1, nil
	}
	b.mu.Unlock()
	why, stop = l.treeGateBase(t.Context(), r.clone, "base-1")
	assert.Contains(t, why, "build failed")
	assert.False(t, stop)
	require.NotNil(t, l.baseGateFails["base-1"])
	assert.Equal(t, 1, l.baseGateFails["base-1"].n, "the first real red gate starts the retry count")
	r.clean()
}

// Canceling a cure gate leaves that head eligible when the base is tried again.
func TestCanceledCureGateDoesNotRememberTheHeadAsRed(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.git(r.worker, "switch", "-q", "--detach", "origin/main")
	r.files("the module", goModule)
	r.files("the red base", map[string]string{"bad.go": buildRed})
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
	r.git(r.worker, "fetch", "-q", "origin")
	r.ok("add --stream s1 --count 1 --one")
	head := r.card("s1-1", map[string]string{"bad.go": ""})
	r.queued(map[string]string{"s1-1": head}, "s1-1")
	r.git(r.clone, "fetch", "-q", "origin", "+refs/heads/main:refs/remotes/origin/main", head)
	r.git(r.clone, "switch", "-q", "--no-track", "--force-create", "land/s1", "refs/remotes/origin/main")
	baseSha := r.git(r.clone, "rev-parse", "HEAD")
	r.ok("fleet beat vision --load 1 --cores 8")
	r.ok("fleet up vision")
	var w whereView
	r.json("where", &w)
	for name, row := range w.Tables["fleet"] {
		if name != "vision" && row["status"] == sprint.Up {
			r.ok("fleet down " + name)
		}
	}
	st, err := r.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(t, err)
	l := &lander{a: r.a, st: st, gateKey: "s1", base: "main", prose: map[string][]string{}, diffs: map[string]string{}, scope: map[string][]string{}}
	s, err := st.Load(t.Context(), []string{sprint.Work, sprint.Merge, sprint.Fleet}, nil)
	require.NoError(t, err)
	cards, ok := l.openStream(s, "s1")
	require.True(t, ok)
	ctx, cancel := context.WithCancel(t.Context())
	b := r.a.landState()
	b.mu.Lock()
	b.flight = &landFlight{}
	b.hostName = func() (string, error) { return "coordinator.local", nil }
	b.gateBench = func(context.Context, string, string, [][]string, bool) (string, int, error) {
		cancel()
		return "", 0, bench.ErrNoBench
	}
	b.mu.Unlock()
	cured, env := l.cureBase(ctx, r.clone, "s1", cards, baseSha, "red base")
	assert.Equal(t, -1, cured)
	assert.ErrorIs(t, ctx.Err(), context.Canceled)
	assert.NotEmpty(t, env)
	assert.Empty(t, l.cureTried, "the canceled candidate remains eligible")
	b.mu.Lock()
	b.gateBench = func(context.Context, string, string, [][]string, bool) (string, int, error) {
		return "", 0, nil
	}
	b.mu.Unlock()
	cured, env = l.cureBase(t.Context(), r.clone, "s1", cards, baseSha, "red base")
	assert.Equal(t, 0, cured, "the same head can cure the base on retry")
	assert.Empty(t, env)
	r.clean()
}

// The bench gate is one shell line: each run named, then run, the first red ending it
// with its status; the finding names that run, read off the line's own output.
func TestTheBenchGateScriptStopsAtTheFirstRedRun(t *testing.T) {
	t.Parallel()
	runs := [][]string{{"echo", "it's green"}, {"sh", "-c", "echo red >&2; exit 3"}, {"echo", "never"}}
	script := gateScript(runs)
	cmd := exec.Command("sh", "-c", script)
	raw, err := cmd.CombinedOutput()
	out := string(raw)
	var exit *exec.ExitError
	require.ErrorAs(t, err, &exit, out)
	assert.Equal(t, 3, exit.ExitCode(), out)
	assert.Contains(t, out, gateMark+"echo it's green\nit's green\n", "a word with a quote in it is one word")
	assert.Contains(t, out, "red\n")
	assert.NotContains(t, out, "never")
	assert.Equal(t, runs[1], redRun(runs, out))
	assert.Equal(t, runs[0], redRun(runs, "no mark at all"), "an output that names no run blames the first")
}
