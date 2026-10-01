package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/member"
)

// The merger's git (mergergit.go) on real repositories: a bare origin holding the
// development branch main and each card's pushed branch, the merger's working
// repository under its root, a scripted sprint and a scripted checks source
// (docs/SPEC-SWARM.md, `member --merger`; tla/Merger.tla).

// mergeBench is an origin, the cards pushed to it, and the sprint the merger reads.
type mergeBench struct {
	t      *testing.T
	root   string
	origin string
	seed   string
	sprint *benchSprint
	env    []string
}

// benchSprint is the sprint as the merger reads it: stream s1's state and its
// queue; it records the merge facts, and a --batch fact takes the batch off.
type benchSprint struct {
	origin string
	state  string
	queue  []member.BatchCard
	stuck  map[string]bool
	facts  []string
}

func (s *benchSprint) Run(args ...string) (int, []byte) {
	var v any
	switch args[0] {
	case "where":
		v = map[string]any{"epoch": 0, "tables": map[string]any{"merge": map[string]any{"s1": map[string]string{"state": s.state, "queued": strconv.Itoa(len(s.queue))}}}}
	case "queue":
		var cards []map[string]any
		for i, c := range s.queue {
			col := "queued"
			if s.stuck[c.ID] {
				col = "stuck"
			}
			cards = append(cards, map[string]any{"id": c.ID, "col": col, "score": i + 1})
		}
		v = map[string]any{"cards": cards}
	case "card":
		for _, c := range s.queue {
			if c.ID == args[1] {
				v = map[string]any{
					"primary":    map[string]any{"Fields": map[string]string{"head": c.Head, "attempt": "1", "brief": c.ID + ": the work (s1)\nbase-repo: " + s.origin + "\nBASE: main\n\nThe task.\n"}},
					"work_cards": []any{map[string]any{"Fields": map[string]string{"attempt": "1", "head": c.Head, "branch": c.Branch}}},
				}
			}
		}
	case "merge":
		fact := strings.Join(args, " ")
		s.facts = append(s.facts, fact)
		switch {
		case strings.Contains(fact, "--conflict "):
			s.state = "stopped"
			s.stuck = map[string]bool{args[6]: true}
		case strings.Contains(fact, "--red") || strings.Contains(fact, "--rejected"):
			s.state = "stopped"
		default:
			n, _ := strconv.Atoi(args[4])
			s.queue = s.queue[n:]
		}
		return 0, []byte("MERGE OK\n")
	}
	b, _ := json.Marshal(v)
	return 0, b
}

// benchChecks answers every head with one state.
type benchChecks struct{ state string }

func (c *benchChecks) State(repo, head string) (string, string, error) { return c.state, "ci", nil }

func newMergeBench(t *testing.T) *mergeBench {
	t.Helper()
	root := t.TempDir()
	b := &mergeBench{t: t, root: root, origin: filepath.Join(root, "origin.git"), seed: filepath.Join(root, "seed")}
	cfg := filepath.Join(root, "gitconfig")
	require.NoError(t, os.WriteFile(cfg, []byte("[user]\n\tname = merger\n\temail = merger@example.com\n[init]\n\tdefaultBranch = main\n"), 0o644))
	b.env = []string{"GIT_CONFIG_GLOBAL=" + cfg, "GIT_CONFIG_NOSYSTEM=1"}
	runGit(t, "", "init", "-q", "-b", "main", "--", b.seed)
	require.NoError(t, os.WriteFile(filepath.Join(b.seed, "f"), []byte("base\n"), 0o644))
	gitAs(t, b.seed, "add", "f")
	gitAs(t, b.seed, "commit", "-q", "-m", "base")
	runGit(t, "", "clone", "-q", "--bare", "--", b.seed, b.origin)
	runGit(t, b.seed, "remote", "add", "origin", b.origin)
	b.sprint = &benchSprint{origin: b.origin, state: "merging"}
	return b
}

// card pushes a card's attempt to origin's sprint/<id>.w1, from main, writing
// file with body, and queues it.
func (b *mergeBench) card(id, file, body string) member.BatchCard {
	b.t.Helper()
	runGit(b.t, b.seed, "fetch", "-q", "origin")
	runGit(b.t, b.seed, "checkout", "-q", "-B", "work-"+id, "origin/main")
	require.NoError(b.t, os.WriteFile(filepath.Join(b.seed, file), []byte(body), 0o644))
	gitAs(b.t, b.seed, "add", file)
	gitAs(b.t, b.seed, "commit", "-q", "-m", "the work of "+id)
	head := gitAs(b.t, b.seed, "rev-parse", "HEAD")
	runGit(b.t, b.seed, "push", "-q", "origin", "HEAD:refs/heads/sprint/"+id+".w1")
	c := member.BatchCard{ID: id, Head: head, Branch: "sprint/" + id + ".w1"}
	b.sprint.queue = append(b.sprint.queue, c)
	return c
}

// advanceMain puts another commit on origin's main, as a landing of another stream would.
func (b *mergeBench) advanceMain(file string) {
	b.t.Helper()
	runGit(b.t, b.seed, "fetch", "-q", "origin")
	runGit(b.t, b.seed, "checkout", "-q", "-B", "other", "origin/main")
	require.NoError(b.t, os.WriteFile(filepath.Join(b.seed, file), []byte("another stream\n"), 0o644))
	gitAs(b.t, b.seed, "add", file)
	gitAs(b.t, b.seed, "commit", "-q", "-m", "another stream landed")
	runGit(b.t, b.seed, "push", "-q", "origin", "HEAD:refs/heads/main")
}

// ref is origin's branch's sha, "" when it has none.
func (b *mergeBench) ref(branch string) string {
	b.t.Helper()
	return strings.TrimSpace(runGit(b.t, b.origin, "for-each-ref", "--format=%(objectname)", "refs/heads/"+branch))
}

// merger is the merger over the bench, batch n, its checks one state.
func (b *mergeBench) merger(n int, checks *benchChecks) (*member.Merger, *bytes.Buffer) {
	g := newGitMerger(filepath.Join(b.root, "merger"))
	g.env = b.env
	var out bytes.Buffer
	return member.NewMerger(member.MergerConfig{As: "merger", Batch: n, Deadline: time.Hour, Grace: time.Minute, RepoOf: briefRepo}, b.sprint, g, checks, &out), &out
}

// tick runs one pass of the merger, which must not stop on an error.
func (b *mergeBench) tick(m *member.Merger, at time.Time) {
	b.t.Helper()
	_, err := m.Tick(at)
	require.NoError(b.t, err)
}

// gitTry is one git in dir that may fail: its output and its error.
func gitTry(dir string, args ...string) (string, error) {
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	return string(out), err
}

var mergeT0 = time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)

// One card lands: the batch's branch sprint/s1.e0.b1 is main with the card's head
// merged, pushed to origin; green, main fast-forwards to it and --batch 1 is fed.
func TestTheMergerLandsACardOnOrigin(t *testing.T) {
	t.Parallel()
	b := newMergeBench(t)
	c := b.card("s1-1", "a", "card one\n")
	before := b.ref("main")
	m, out := b.merger(1, &benchChecks{state: member.CheckGreen})
	b.tick(m, mergeT0)
	branch := b.ref("sprint/s1.e0.b1")
	require.NotEmpty(t, branch, "the batch's branch is pushed: %s", out)
	assert.Equal(t, before, b.ref("main"), "nothing lands before the checks")
	parents := strings.Fields(runGit(t, b.origin, "rev-list", "--parents", "-n", "1", branch))
	assert.Equal(t, []string{branch, before, c.Head}, parents, "a merge commit of main and the card's head")
	b.tick(m, mergeT0.Add(time.Second))
	assert.Equal(t, branch, b.ref("main"), "main fast-forwards to the batch's branch")
	assert.Equal(t, []string{"merge --stream s1 --batch 1 --note landed main at " + branch + "; ci --epoch 0"}, b.sprint.facts)
}

// A batch of two whose second card conflicts with the first stops there:
// --conflict on it; the branch as built up to it (main and s1-1) is pushed as
// the batch's record, and main does not move.
func TestAConflictingSecondCardStopsTheBatch(t *testing.T) {
	t.Parallel()
	b := newMergeBench(t)
	one := b.card("s1-1", "f", "one\n")
	two := b.card("s1-2", "f", "two\n")
	before := b.ref("main")
	m, _ := b.merger(2, &benchChecks{state: member.CheckGreen})
	b.tick(m, mergeT0)
	assert.Equal(t, []string{"merge --stream s1 --batch 2 --conflict s1-2 --note the head of s1-2 did not merge into sprint/s1.e0.b1 --epoch 0"}, b.sprint.facts)
	record := b.ref("sprint/s1.e0.b1")
	require.NotEmpty(t, record, "the record is pushed")
	runGit(t, b.origin, "merge-base", "--is-ancestor", one.Head, record)
	assert.Equal(t, before, b.ref("main"))
	_, err := gitTry(b.origin, "merge-base", "--is-ancestor", two.Head, record)
	assert.Error(t, err, "the conflicting card is not in the record")
}

// Red checks feed --red with the batch; main does not move.
func TestRedChecksOnOriginFeedRed(t *testing.T) {
	t.Parallel()
	b := newMergeBench(t)
	b.card("s1-1", "a", "one\n")
	before := b.ref("main")
	m, _ := b.merger(1, &benchChecks{state: member.CheckRed})
	b.tick(m, mergeT0)
	b.tick(m, mergeT0.Add(time.Second))
	require.Len(t, b.sprint.facts, 1)
	assert.Contains(t, b.sprint.facts[0], "merge --stream s1 --batch 1 --red --suspect s1-1 --note checks red on sprint/s1.e0.b1")
	assert.Equal(t, before, b.ref("main"))
}

// main moved after the build: the landing is not a fast-forward and is fed
// --rejected; after resume the same batch is built fresh from the moved main on
// the next branch, b2, and lands; b1 stays as it was.
func TestANonFastForwardLandingIsRejectedAndTheBatchLandsAfterResume(t *testing.T) {
	t.Parallel()
	b := newMergeBench(t)
	c := b.card("s1-1", "a", "one\n")
	m, _ := b.merger(1, &benchChecks{state: member.CheckGreen})
	b.tick(m, mergeT0)
	first := b.ref("sprint/s1.e0.b1")
	b.advanceMain("other")
	moved := b.ref("main")
	b.tick(m, mergeT0.Add(time.Second))
	require.Len(t, b.sprint.facts, 1)
	assert.Contains(t, b.sprint.facts[0], "merge --stream s1 --batch 1 --rejected --note the push of "+first+" to main is not a fast-forward")
	assert.Equal(t, moved, b.ref("main"))

	b.sprint.state = "merging" // the coordinator resumed
	b.tick(m, mergeT0.Add(2*time.Second))
	again := b.ref("sprint/s1.e0.b2")
	require.NotEmpty(t, again)
	assert.Equal(t, []string{again, moved, c.Head}, strings.Fields(runGit(t, b.origin, "rev-list", "--parents", "-n", "1", again)), "fresh from the moved main")
	assert.Equal(t, first, b.ref("sprint/s1.e0.b1"), "the rejected batch's branch stays as its record")
	b.tick(m, mergeT0.Add(3*time.Second))
	assert.Equal(t, again, b.ref("main"))
	require.Len(t, b.sprint.facts, 2)
	assert.Contains(t, b.sprint.facts[1], "merge --stream s1 --batch 1 --note landed main at "+again)
}

// A red batch then resume: the coordinator returns the red card and resumes, and
// the next batch is built fresh from main on the next branch and lands; the red
// batch's branch stays on origin as its record, and nothing is deleted.
func TestAfterARedBatchResumeIsEnoughForTheNextBatchToLand(t *testing.T) {
	t.Parallel()
	b := newMergeBench(t)
	b.card("s1-1", "a", "one\n")
	checks := &benchChecks{state: member.CheckRed}
	m, _ := b.merger(1, checks)
	b.tick(m, mergeT0)
	b.tick(m, mergeT0.Add(time.Second))
	red := b.ref("sprint/s1.e0.b1")
	require.NotEmpty(t, red)
	require.Len(t, b.sprint.facts, 1)
	b.sprint.queue = nil // the coordinator returned s1-1 and resumed: nothing else
	b.sprint.state = "merging"
	two := b.card("s1-2", "b", "two\n")
	before := b.ref("main")
	checks.state = member.CheckGreen
	b.tick(m, mergeT0.Add(2*time.Second))
	next := b.ref("sprint/s1.e0.b2")
	require.NotEmpty(t, next)
	assert.Equal(t, []string{next, before, two.Head}, strings.Fields(runGit(t, b.origin, "rev-list", "--parents", "-n", "1", next)), "fresh from main, without the red card")
	b.tick(m, mergeT0.Add(3*time.Second))
	assert.Equal(t, next, b.ref("main"))
	assert.Equal(t, red, b.ref("sprint/s1.e0.b1"), "the red batch's branch is its record, untouched")
	require.Len(t, b.sprint.facts, 2)
	assert.Contains(t, b.sprint.facts[1], "merge --stream s1 --batch 1 --note landed main at "+next)
}

// A stopped stream is untouched: no working repository, no branch, no fact.
func TestTheMergerLeavesAStoppedStreamAlone(t *testing.T) {
	t.Parallel()
	b := newMergeBench(t)
	b.card("s1-1", "a", "one\n")
	b.sprint.state = "stopped"
	m, _ := b.merger(1, &benchChecks{state: member.CheckGreen})
	b.tick(m, mergeT0)
	assert.Empty(t, b.sprint.facts)
	assert.Empty(t, b.ref("sprint/s1.e0.b1"))
	_, err := os.Stat(filepath.Join(b.root, "merger", "merge"))
	assert.True(t, os.IsNotExist(err), "no working repository is made for a stream left alone")
}

// member --merger needs no harness; it is refused with --reader, a batch under 1
// or no --as, and runs one pass with --once, printing its start line.
func TestMemberMergerFlags(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ args, refusal string }{
		{"--merger --reader --as m --root R", "--merger and --reader are two roles"},
		{"--merger --batch 0 --as m --root R", "--batch is the most cards of a stream's batch, at least 1"},
		{"--merger --root R", "--as"},
	} {
		var out, errb bytes.Buffer
		args := strings.Fields(strings.ReplaceAll(tc.args, "R", t.TempDir()))
		assert.Equal(t, 2, cmdMember(args, &out, &errb), tc.args)
		assert.Contains(t, errb.String(), tc.refusal, tc.args)
	}
	var out, errb bytes.Buffer
	code := cmdMember([]string{"--merger", "--as", "merger", "--root", t.TempDir(), "--once", "--sprint", "/usr/bin/false"}, &out, &errb)
	assert.Equal(t, 0, code, errb.String())
	assert.Contains(t, out.String(), "MEMBER merger as=merger batch=1 deadline=30m0s every=3s sprint=/usr/bin/false gh=gh")
	assert.Contains(t, errb.String(), "nova-swarm member: tick 1: where: exit 1", "a store that does not answer is said, not silent")
}
