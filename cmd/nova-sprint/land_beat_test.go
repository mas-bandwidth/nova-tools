package main

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
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
		host string
		argv string
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
	b.gateBench = func(ctx context.Context, host, dir string, argv []string) (string, int, error) {
		joined := strings.Join(argv, " ")
		callMu.Lock()
		calls = append(calls, call{host, joined})
		nBuild := 0
		for _, c := range calls {
			if c.argv == "go build ./..." {
				nBuild++
			}
		}
		callMu.Unlock()
		// the first go build blocks; a later one is the card's own gate
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
