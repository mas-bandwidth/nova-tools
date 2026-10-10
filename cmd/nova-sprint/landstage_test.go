package main

import (
	"context"
	"fmt"
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

// Through the land loop's bench seam: the bench at the stream's slot refuses every stage
// (no mirror). The gate steps to the ring's next slot and runs there instead of in the
// clone, and the refusing bench is asked at most twice in the pass.
func TestARefusedStageStepsTheGateToTheNextSlot(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.git(r.worker, "switch", "-q", "--detach", "origin/main")
	r.files("the module", goModule)
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
	r.git(r.worker, "fetch", "-q", "origin")
	r.ok("add --stream s1 --count 3")
	heads := map[string]string{}
	for i := 1; i <= 3; i++ {
		id := fmt.Sprintf("s1-%d", i)
		heads[id] = r.card(id, map[string]string{fmt.Sprintf("ok%d.go", i): fmt.Sprintf("package main\n\nfunc ok%d() {}\n", i)})
	}
	r.queued(heads, "s1-1", "s1-2", "s1-3")
	for _, m := range []string{"vision", "space"} {
		r.ok("fleet beat " + m + " --load 1 --cores 8")
		r.ok("fleet up " + m)
	}
	var w whereView
	r.json("where", &w)
	for name, row := range w.Tables["fleet"] {
		if name != "vision" && name != "space" && row["status"] == sprint.Up {
			r.ok("fleet down " + name)
		}
	}
	st, err := r.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(t, err)
	s, err := st.Load(context.Background(), []string{sprint.Fleet}, nil)
	require.NoError(t, err)
	hosts := s.UpMembers()
	require.ElementsMatch(t, []string{"vision", "space"}, hosts)
	ring := benchRing("s1", hosts)
	bad, good := ring[0], ring[1]

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
		if host == bad {
			return "", 0, &bench.StageError{Host: bad, Step: "staging 0123456789ab from nova-bench/mirror/nova-tools.git at nova-bench/runs/run.x/repo", Code: bench.NoMirror,
				Tail: "no mirror at nova-bench/mirror/nova-tools.git", Wall: 2 * time.Second}
		}
		return "", 0, nil
	}
	b.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var out beatBuf
	deadline := time.Now().Add(2 * time.Minute)
	r.a.sleep = func(time.Duration) {
		text := out.String()
		if strings.Contains(text, "LAND REFUSED") || strings.Contains(text, "LAND OK") || strings.Contains(text, "LAND FAILED") || time.Now().After(deadline) {
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
	nBad, nGood := 0, 0
	for _, h := range got {
		switch h {
		case bad:
			nBad++
		case good:
			nGood++
		}
	}
	require.NotZero(t, nBad, "the stream's slot is asked first: %v\n%s", got, text)
	assert.LessOrEqual(t, nBad, bench.SkipAfter, "a bench refused twice is not asked again this pass: %v", got)
	assert.Equal(t, len(got)-nBad, nGood, "every gate the bad slot refused ran on the next: %v", got)
	assert.Regexp(t, regexp.MustCompile(`LAND OK stream=s1 .* bench=`+good+` wall=\d+\.\ds ring=2\b`), text, "the gate ran on the next slot, never in the clone")
	r.clean()
}

// benchGate itself, three gates of one pass on a ring of two whose first slot refuses
// every stage: each refusal is said with its reason and the gate runs on the next slot;
// after the second refusal the first slot is passed over, said, and not asked again; a
// new pass asks it again; and when every slot refuses, the gate runs here.
func TestATwiceRefusedBenchIsPassedOverForThePass(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	for _, m := range []string{"vision", "space"} {
		r.ok("fleet beat " + m + " --load 1 --cores 8")
		r.ok("fleet up " + m)
	}
	st, err := r.a.store(common{redis: "mem:0", actor: "tester"})
	require.NoError(t, err)
	hosts := []string{"vision", "space"}
	ring := benchRing("s1", hosts)
	bad, good := ring[0], ring[1]
	refusal := func(h string) *bench.StageError {
		return &bench.StageError{Host: h, Step: "staging 0123456789ab from nova-bench/mirror/nova-tools.git at nova-bench/runs/run.x/repo", Code: bench.NoMirror,
			Tail: "no mirror at nova-bench/mirror/nova-tools.git", Wall: 2 * time.Second}
	}
	said := refusal(bad).Error()
	var asked []string
	refuses := map[string]bool{bad: true}
	b := r.a.landState()
	b.mu.Lock()
	b.gateBench = func(ctx context.Context, host, dir string, runs [][]string, withGit bool) (string, int, error) {
		asked = append(asked, host)
		if refuses[host] {
			return "", 0, refusal(host)
		}
		return "", 0, nil
	}
	b.mu.Unlock()
	runs := gateRuns(false, nil)

	l := &lander{a: r.a, st: st, gateKey: "s1"}
	for i := 0; i < 3; i++ {
		why, ran := l.benchGate(context.Background(), hosts, r.clone, runs, false)
		assert.Empty(t, why)
		assert.True(t, ran, "gate %d ran on a bench", i+1)
	}
	assert.Equal(t, []string{bad, good, bad, good, good}, asked, "the first slot is asked twice, then passed over")
	skip := "skip " + bad + ": its stage failed 2 times this pass, last: " + said
	assert.Equal(t, []string{"tree gate: " + said, "tree gate: " + said, "tree gate: " + skip, "tree gate: " + skip}, l.ledgerLog,
		"each refusal said; the pass-over said once by each gate it shapes, the second (where it began) and the third")
	assert.Equal(t, good, l.gateHost)
	assert.Equal(t, 2, l.gateRing)
	lanes := r.ok("lane list")
	assert.NotContains(t, lanes, "lander", "every lane taken is given back: %s", lanes)

	asked = nil
	next := &lander{a: r.a, st: st, gateKey: "s1"}
	_, ran := next.benchGate(context.Background(), hosts, r.clone, runs, false)
	assert.True(t, ran)
	assert.Equal(t, []string{bad, good}, asked, "a new pass starts with every bench")

	asked = nil
	refuses[good] = true
	_, ran = next.benchGate(context.Background(), hosts, r.clone, runs, false)
	assert.False(t, ran, "every slot refused: the gate runs here")
	assert.Equal(t, []string{bad, good}, asked)
	r.clean()
}
