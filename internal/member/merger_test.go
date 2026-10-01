package member

import (
	"bytes"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The merger's loop (merger.go; tla/Merger.tla) against a scripted sprint, a
// fake git and a fake checks source: the facts it feeds are pinned here, the
// git it does in cmd/nova-swarm's merger tests.

// mergeCardOf is a queued card of the scripted sprint: its column, score and head.
type mergeCardOf struct {
	id, col string
	score   float64
	head    string
}

// mergeSprint is a sprint as the merger reads it: one stream's state and queue;
// it records every verb, and a --batch fact takes the batch off the queue.
type mergeSprint struct {
	state string
	queue []mergeCardOf
	calls []string
}

func (s *mergeSprint) Run(args ...string) (int, []byte) {
	s.calls = append(s.calls, strings.Join(args, " "))
	var v any
	switch args[0] {
	case "where":
		queued := 0
		for _, c := range s.queue {
			if c.col == "queued" {
				queued++
			}
		}
		v = map[string]any{"epoch": 3, "tables": map[string]any{"merge": map[string]any{"s1": map[string]string{"state": s.state, "queued": strconv.Itoa(queued)}}}}
	case "queue":
		var cards []map[string]any
		for _, c := range s.queue {
			cards = append(cards, map[string]any{"id": c.id, "col": c.col, "score": c.score})
		}
		v = map[string]any{"cards": cards}
	case "card":
		for _, c := range s.queue {
			if c.id == args[1] {
				v = map[string]any{
					"primary":    map[string]any{"Fields": map[string]string{"head": c.head, "attempt": "1", "brief": "the brief"}},
					"work_cards": []any{map[string]any{"Fields": map[string]string{"attempt": "1", "head": c.head, "branch": "sprint/" + c.id + ".w1"}}},
				}
			}
		}
	case "merge":
		fact := false
		for _, a := range args {
			fact = fact || a == "--conflict" || a == "--red" || a == "--rejected"
		}
		if fact {
			s.state = "stopped"
		} else {
			n := 0
			for i := range s.queue {
				if s.queue[i].col == "queued" && n < 1 {
					s.queue[i].col, n = "merged", n+1
				}
			}
		}
		return 0, []byte("MERGE OK\n")
	}
	b, _ := json.Marshal(v)
	return 0, b
}

// merges is the merge verbs the sprint was fed.
func (s *mergeSprint) merges() []string {
	var out []string
	for _, c := range s.calls {
		if strings.HasPrefix(c, "merge ") {
			out = append(out, c)
		}
	}
	return out
}

// fakeMergeGit is the merger's hands, scripted.
type fakeMergeGit struct {
	landed   bool
	conflict string
	pushErr  error
	rejected string
	calls    []string
}

func (g *fakeMergeGit) Landed(b Batch) (bool, error) {
	g.calls = append(g.calls, "landed")
	return g.landed, nil
}
func (g *fakeMergeGit) Build(b Batch) (string, string, error) {
	g.calls = append(g.calls, "build "+strings.Join(b.IDs(), ",")+" on "+b.Branch)
	return strings.Repeat("b", 40), g.conflict, nil
}
func (g *fakeMergeGit) Push(b Batch, head string) error {
	g.calls = append(g.calls, "push")
	return g.pushErr
}
func (g *fakeMergeGit) Land(b Batch, head string) (string, error) {
	g.calls = append(g.calls, "land")
	return g.rejected, nil
}

// fakeChecks answers the checks with the next state of its list (the last repeats).
type fakeChecks struct{ states []string }

func (c *fakeChecks) State(repo, head string) (string, string, error) {
	st := c.states[0]
	if len(c.states) > 1 {
		c.states = c.states[1:]
	}
	if st == "error" {
		return "", "", errors.New("gh: not reachable")
	}
	return st, "ci", nil
}

var mt0 = time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)

// mergerOn is a merger over the scripted sprint, git and checks, batch 1.
func mergerOn(s *mergeSprint, g *fakeMergeGit, c *fakeChecks) (*Merger, *bytes.Buffer) {
	var out bytes.Buffer
	m := NewMerger(MergerConfig{As: "merger", Batch: 1, Deadline: time.Hour, Grace: time.Minute,
		RepoOf: func(string) (string, string) { return "/origin.git", "main" }}, s, g, c, &out)
	return m, &out
}

func sha40(c byte) string { return strings.Repeat(string(c), 40) }

// One card lands: built on sprint/s1.e3, pushed, proved green next pass, landed,
// and fed --batch 1 with the landing in the note, at the epoch read.
func TestTheMergerLandsAGreenBatchAndFeedsTheBatch(t *testing.T) {
	t.Parallel()
	s := &mergeSprint{state: "merging", queue: []mergeCardOf{{"s1-1", "queued", 1, sha40('a')}}}
	g := &fakeMergeGit{}
	m, out := mergerOn(s, g, &fakeChecks{states: []string{CheckGreen}})
	_, err := m.Tick(mt0)
	require.NoError(t, err)
	assert.Equal(t, []string{"landed", "build s1-1 on sprint/s1.e3", "push"}, g.calls)
	assert.Empty(t, s.merges(), "nothing is fed before the checks")
	assert.Equal(t, 1, m.Running())
	acted, err := m.Tick(mt0.Add(time.Second))
	require.NoError(t, err)
	assert.Equal(t, 1, acted)
	assert.Equal(t, []string{"merge --stream s1 --batch 1 --note landed main at " + sha40('b') + "; ci --epoch 3"}, s.merges())
	assert.Equal(t, 0, m.Running())
	assert.Contains(t, out.String(), "merge s1 batch=s1-1 branch=sprint/s1.e3 head="+sha40('b')+" pushed")
}

// A card whose head does not merge stops the batch: --conflict on it, nothing pushed.
func TestAConflictFeedsTheConflictAndPushesNothing(t *testing.T) {
	t.Parallel()
	s := &mergeSprint{state: "merging", queue: []mergeCardOf{{"s1-1", "queued", 1, sha40('a')}}}
	g := &fakeMergeGit{conflict: "s1-1"}
	m, _ := mergerOn(s, g, &fakeChecks{states: []string{CheckGreen}})
	_, err := m.Tick(mt0)
	require.NoError(t, err)
	assert.Equal(t, []string{"merge --stream s1 --batch 1 --conflict s1-1 --note the head of s1-1 did not merge into sprint/s1.e3 --epoch 3"}, s.merges())
	assert.NotContains(t, g.calls, "push")
	assert.Equal(t, 0, m.Running())
}

// Red checks feed --red with the batch as the suspects; nothing lands.
func TestRedChecksFeedRedWithTheBatch(t *testing.T) {
	t.Parallel()
	s := &mergeSprint{state: "merging", queue: []mergeCardOf{{"s1-1", "queued", 1, sha40('a')}}}
	g := &fakeMergeGit{}
	m, _ := mergerOn(s, g, &fakeChecks{states: []string{CheckPending, CheckRed}})
	for i := 0; i < 3; i++ {
		_, err := m.Tick(mt0.Add(time.Duration(i) * time.Second))
		require.NoError(t, err)
	}
	require.Len(t, s.merges(), 1)
	assert.True(t, strings.HasPrefix(s.merges()[0], "merge --stream s1 --batch 1 --red --suspect s1-1 --note checks red on sprint/s1.e3"), s.merges()[0])
	assert.NotContains(t, g.calls, "land")
}

// A landing origin refuses as not a fast-forward feeds --rejected.
func TestANonFastForwardLandingFeedsRejected(t *testing.T) {
	t.Parallel()
	s := &mergeSprint{state: "merging", queue: []mergeCardOf{{"s1-1", "queued", 1, sha40('a')}}}
	g := &fakeMergeGit{rejected: "! refs/heads/main [rejected] (non-fast-forward)"}
	m, _ := mergerOn(s, g, &fakeChecks{states: []string{CheckGreen}})
	m.Tick(mt0)
	m.Tick(mt0.Add(time.Second))
	require.Len(t, s.merges(), 1)
	assert.Contains(t, s.merges()[0], "merge --stream s1 --batch 1 --rejected --note the push of "+sha40('b')+" to main is not a fast-forward")
}

// A stream that is not merging is left alone: no queue read, no git, no fact.
func TestAStoppedStreamIsUntouched(t *testing.T) {
	t.Parallel()
	for _, state := range []string{"stopped", "waiting", "landed"} {
		s := &mergeSprint{state: state, queue: []mergeCardOf{{"s1-1", "queued", 1, sha40('a')}}}
		g := &fakeMergeGit{}
		m, _ := mergerOn(s, g, &fakeChecks{states: []string{CheckGreen}})
		m.Tick(mt0)
		assert.Equal(t, []string{"where --json"}, s.calls, state)
		assert.Empty(t, g.calls, state)
	}
}

// A stuck card is a barrier: nothing at or past it is taken, whatever its score.
func TestAStuckCardIsABarrier(t *testing.T) {
	t.Parallel()
	s := &mergeSprint{state: "merging", queue: []mergeCardOf{{"s1-2", "queued", 2, sha40('a')}, {"s1-1", "stuck", 1, sha40('c')}}}
	g := &fakeMergeGit{}
	m, _ := mergerOn(s, g, &fakeChecks{states: []string{CheckGreen}})
	m.Tick(mt0)
	assert.Empty(t, g.calls, "s1-2 is after the stuck s1-1")
	assert.Empty(t, s.merges())
}

// One batch at a time: while a batch waits for its checks the stream's queue is not read again.
func TestOneBatchAtATime(t *testing.T) {
	t.Parallel()
	s := &mergeSprint{state: "merging", queue: []mergeCardOf{{"s1-1", "queued", 1, sha40('a')}, {"s1-2", "queued", 2, sha40('c')}}}
	g := &fakeMergeGit{}
	m, _ := mergerOn(s, g, &fakeChecks{states: []string{CheckPending}})
	for i := 0; i < 3; i++ {
		m.Tick(mt0.Add(time.Duration(i) * time.Second))
	}
	assert.Equal(t, 1, strings.Count(strings.Join(g.calls, ";"), "build"), "one build, the batch waiting: %v", g.calls)
	assert.Empty(t, s.merges())
}

// A batch the development branch holds already (landed, its fact not fed) is fed --batch without a build.
func TestALandedBatchIsFedWithoutABuild(t *testing.T) {
	t.Parallel()
	s := &mergeSprint{state: "merging", queue: []mergeCardOf{{"s1-1", "queued", 1, sha40('a')}}}
	g := &fakeMergeGit{landed: true}
	m, _ := mergerOn(s, g, &fakeChecks{states: []string{CheckGreen}})
	m.Tick(mt0)
	assert.Equal(t, []string{"landed"}, g.calls)
	assert.Equal(t, []string{"merge --stream s1 --batch 1 --note the development branch main holds the batch already --epoch 3"}, s.merges())
}

// The checks' bounds: a head with no check run within the grace is proved by none
// (said in the note); checks pending past the deadline are red.
func TestTheChecksBounds(t *testing.T) {
	t.Parallel()
	s := &mergeSprint{state: "merging", queue: []mergeCardOf{{"s1-1", "queued", 1, sha40('a')}}}
	m, _ := mergerOn(s, &fakeMergeGit{}, &fakeChecks{states: []string{CheckNone}})
	m.Tick(mt0)
	m.Tick(mt0.Add(30 * time.Second))
	assert.Empty(t, s.merges(), "within the grace it waits")
	m.Tick(mt0.Add(2 * time.Minute))
	require.Len(t, s.merges(), 1)
	assert.Contains(t, s.merges()[0], "--batch 1 --note landed main at "+sha40('b')+"; no check run on "+sha40('b')+" within 1m0s: proved by none")

	s = &mergeSprint{state: "merging", queue: []mergeCardOf{{"s1-1", "queued", 1, sha40('a')}}}
	m, _ = mergerOn(s, &fakeMergeGit{}, &fakeChecks{states: []string{CheckPending}})
	m.Tick(mt0)
	m.Tick(mt0.Add(2 * time.Hour))
	require.Len(t, s.merges(), 1)
	assert.Contains(t, s.merges()[0], "--red --suspect s1-1 --note checks red on sprint/s1.e3 at "+sha40('b')+": the checks did not conclude within 1h0m0s")
}

// The merger's own problems are a NOTE, said once while they stand, and nothing is fed.
func TestTheMergersOwnProblemsAreSaidOnce(t *testing.T) {
	t.Parallel()
	s := &mergeSprint{state: "merging", queue: []mergeCardOf{{"s1-1", "queued", 1, sha40('a')}}}
	m, out := mergerOn(s, &fakeMergeGit{pushErr: errors.New("! refs/heads/sprint/s1.e3 [remote rejected] (permission denied)")}, &fakeChecks{states: []string{CheckGreen}})
	for i := 0; i < 3; i++ {
		m.Tick(mt0.Add(time.Duration(i) * time.Second))
	}
	assert.Equal(t, 1, strings.Count(out.String(), "NOTE merge s1: pushing sprint/s1.e3: ! refs/heads/sprint/s1.e3 [remote rejected] (permission denied)"), out.String())
	assert.Empty(t, s.merges())

	s = &mergeSprint{state: "merging", queue: []mergeCardOf{{"s1-1", "queued", 1, "short"}}}
	m, out = mergerOn(s, &fakeMergeGit{}, &fakeChecks{states: []string{CheckGreen}})
	m.Tick(mt0)
	assert.Contains(t, out.String(), "NOTE merge s1: card s1-1 has no full head to merge")
}
