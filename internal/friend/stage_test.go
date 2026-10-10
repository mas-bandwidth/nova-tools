package friend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/gitrun"
	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stagedCard is a held work card whose brief names its repository and base, as the sprint
// writes a heavy card's header.
func stagedCard(card, col, repo, base string) HeldCard {
	h := workCard(card, col)
	h.Brief = strings.Replace(h.Brief, "\n\n", fmt.Sprintf("\n\nRESULT: %s sha= tier: heavy\nREPO: %s\nBASE: %s\n", card, repo, base), 1)
	return h
}

// stageEnv is git's environment for the stager under test: the caller's without its GIT_
// variables, the system config off and the global config an empty file, so the machine's git
// settings cannot reach the test.
func stageEnv(t *testing.T) []string {
	global := filepath.Join(t.TempDir(), "gitconfig")
	require.NoError(t, os.WriteFile(global, nil, 0o644))
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			env = append(env, kv)
		}
	}
	return append(env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+global, "GIT_TERMINAL_PROMPT=0")
}

func gitIn(t *testing.T, env []string, dir string, args ...string) string {
	t.Helper()
	out, err := gitrun.Output(context.Background(), gitrun.Options{Env: env, C: dir, OwnRepo: true}, args...)
	require.NoError(t, err, "git %v", args)
	return out
}

// For every card the daemon writes into the inbox it also stages the job: a clone of REPO at
// BASE on the card's branch under jobs/<job>/repo, from one cached mirror per repository, and
// jobs/<job>/JOB.md naming the checkout, the branch, the outbox report and the finish. A
// repository her account cannot reach is one judgment to the coordinator with the remedy, and
// no lane is handed its card.
func TestEveryWrittenJobIsStagedWithItsCheckoutAndJobFile(t *testing.T) {
	t.Parallel()
	g := testkit.Git(t, 1)
	g.Commit(g.Clones[0], map[string]string{"README.md": "the tools\n"})
	env := stageEnv(t)
	gitIn(t, env, g.Clones[0], "push", "-q", g.Remote, "HEAD:refs/heads/sprint/mechanical")
	base := gitIn(t, env, g.Remote, "rev-parse", "sprint/mechanical")
	g.Commit(g.Clones[0], map[string]string{"later.md": "landed on another branch\n"})
	gitIn(t, env, g.Clones[0], "push", "-q", g.Remote, "HEAD:refs/heads/other")

	r := newRig(t)
	r.d.Coordinator = "ada"
	dir := r.d.Dir
	urls := map[string]string{"mas-bandwidth/nova-tools": g.Remote, "mas-bandwidth/private": filepath.Join(t.TempDir(), "no-such-repo")}
	stager := &Stager{Dir: dir, Env: env, URL: func(repo string) string { return urls[repo] }}
	stages, ended := 0, make(chan struct{}, 16)
	r.d.Stage = func(ctx context.Context, p Packet) (string, error) {
		r.mu.Lock()
		stages++
		r.mu.Unlock()
		defer func() { ended <- struct{}{} }()
		return stager.Stage(ctx, p)
	}
	// the rig stops its Run after the steps it is asked for, and a stop ends a stage: the
	// first step's beat waits for the four stages it started (the loop beats on meanwhile in
	// a daemon that runs)
	r.at[1] = func() {
		for range 4 {
			<-ended
		}
	}
	row := &twinRow{}
	r.d.Held = row.held
	// written by the daemon this loop: two cards on the tools, one dealt with its packet in
	// the server's fields; one on a repository her account cannot reach; a card that names no
	// repository and a read, neither of which is staged here
	taken := stagedCard("taken.w1", "working", "mas-bandwidth/nova-tools", "sprint/mechanical")
	dealt := stagedCard("dealt.w2", "ready", "-", "-")
	dealt.Brief = strings.Replace(dealt.Brief, "REPO: -\nBASE: -\n", "", 1)
	dealt.Repo, dealt.Base, dealt.Branch, dealt.Attempt = "mas-bandwidth/nova-tools", base, "sprint/dealt.w2.g3.e15", 2
	private := stagedCard("private.w1", "working", "mas-bandwidth/private", "main")
	private2 := stagedCard("private2.w1", "working", "mas-bandwidth/private", "main")
	plain := workCard("plain.w1", "working")
	read := HeldCard{Card: "frontier.r1", Job: "frontier.r1.bob", Col: "working", Kind: "read", Brief: "WHO: friend bob\n\nread this\n"}
	row.set(taken, dealt, private, private2, plain, read)

	r.run(t, 1) // writes the briefs and starts the stages; Run waits for them before it returns
	for _, h := range []HeldCard{taken, dealt} {
		require.FileExists(t, filepath.Join(dir, "inbox", h.Job, "BRIEF.md"))
	}
	r.run(t, 1) // says what they did

	for _, c := range []struct {
		h               HeldCard
		branch, attempt string
	}{{taken, "sprint/taken.w1.g1.e15", "attempt 1"}, {dealt, "sprint/dealt.w2.g3.e15", "attempt 2"}} {
		checkout := filepath.Join(dir, "jobs", c.h.Job, "repo")
		assert.Equal(t, base, gitIn(t, env, checkout, "rev-parse", "HEAD"), "%s is staged at its base", c.h.Card)
		assert.Equal(t, c.branch, gitIn(t, env, checkout, "rev-parse", "--abbrev-ref", "HEAD"), "on the card's branch")
		assert.Equal(t, g.Remote, gitIn(t, env, checkout, "remote", "get-url", "origin"), "origin is the repository, never the mirror")
		assert.Equal(t, base, gitIn(t, env, checkout, "rev-parse", "origin/sprint/mechanical"), "origin's base is in the checkout")
		assert.FileExists(t, filepath.Join(checkout, "README.md"), "checked out")
		raw, err := os.ReadFile(filepath.Join(dir, "jobs", c.h.Job, "JOB.md"))
		require.NoError(t, err, "%s has its JOB.md", c.h.Card)
		job := string(raw)
		assert.True(t, strings.HasPrefix(job, "# JOB: work "+c.h.Card+", "+c.attempt+"\n"), "the card-contract title: %q", job)
		assert.Contains(t, job, "The staged checkout: "+checkout+" (a git worktree of mas-bandwidth/nova-tools at ")
		assert.Contains(t, job, base+", on branch "+c.branch+")")
		assert.Contains(t, job, "git push -u origin "+c.branch)
		assert.Contains(t, job, "Finish: write "+filepath.Join(dir, "outbox", c.h.Job, "REPORT.md")+" with 'Verdict: LAND|HOLD|FAIL' and 'Head: <sha>'")
		assert.Contains(t, job, "RESULT.md beside it")
	}
	mirrors, err := filepath.Glob(filepath.Join(dir, "mirrors", "*", "*"))
	require.NoError(t, err)
	assert.Equal(t, []string{filepath.Join(dir, "mirrors", "mas-bandwidth", "nova-tools.git")}, mirrors, "one mirror per repository reached, shared by its cards")

	for _, h := range []HeldCard{private, private2, plain, read} {
		assert.NoFileExists(t, filepath.Join(dir, "jobs", h.Job, "JOB.md"), "%s is not staged", h.Card)
		assert.NoDirExists(t, filepath.Join(dir, "jobs", h.Job, "repo"))
	}
	var judgments []string
	for _, m := range r.adaGot(t) {
		if strings.HasPrefix(m, "judgment: ") {
			judgments = append(judgments, m)
		}
	}
	require.Len(t, judgments, 1, "one judgment for the repository, not one per card: %v", judgments)
	assert.Contains(t, judgments[0], "judgment: bob cannot reach mas-bandwidth/private")
	assert.Contains(t, judgments[0], "The remedy: give her account read and push access to mas-bandwidth/private")

	// no lane is handed a card whose job is not staged; a card the daemon stages nothing for is
	// handed on its brief
	var handed []string
	skip := func(c Card) bool { return false }
	for {
		c, ok, err := r.d.nextCard(skip)
		require.NoError(t, err)
		if !ok {
			break
		}
		handed = append(handed, c.ID)
		prev := skip
		skip = func(x Card) bool { return x.ID == c.ID || prev(x) }
	}
	assert.Equal(t, []string{"taken.w1", "dealt.w2", "plain.w1", "frontier.r1"}, handed)

	// the batch session hears of the staged cards' briefs once they are staged, never of a card
	// with no checkout
	delivered := func() string { r.mu.Lock(); defer r.mu.Unlock(); return strings.Join(r.delivered, "\n") }
	require.Eventually(t, func() bool { return strings.Contains(delivered(), "inbox/taken.w1~15/BRIEF.md") }, 10*time.Second, time.Millisecond)
	assert.Contains(t, delivered(), "inbox/dealt.w2~15/BRIEF.md")
	assert.Contains(t, delivered(), "inbox/plain.w1~15/BRIEF.md")
	assert.NotContains(t, delivered(), "inbox/private.w1~15/BRIEF.md")

	// later loops stage nothing again and say the judgment no more: the unreachable jobs wait
	// StageRetryEvery before they are tried again
	r.mu.Lock()
	before := stages
	r.mu.Unlock()
	r.run(t, 5)
	r.mu.Lock()
	assert.Equal(t, before, stages, "a staged job is not staged again, and a failed one waits")
	r.mu.Unlock()
	n := 0
	for _, m := range r.adaGot(t) {
		if strings.HasPrefix(m, "judgment: ") {
			n++
		}
	}
	assert.Equal(t, 1, n, "said once while it stands")
}

// A stage the daemon's stop ends is no judgment and no failure: nothing is said, and the next
// Run stages it again.
func TestAStageEndedByTheDaemonsStopIsStagedAgain(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.d.Coordinator = "ada"
	row := &twinRow{}
	r.d.Held = row.held
	row.set(stagedCard("a.w1", "working", "o/r", "dev"))
	calls := 0
	r.d.Stage = func(ctx context.Context, p Packet) (string, error) {
		r.mu.Lock()
		calls++
		first := calls == 1
		r.mu.Unlock()
		if first {
			<-ctx.Done()
			return "", &NotStageable{Repo: p.Repo, Why: "git clone: " + ctx.Err().Error()}
		}
		checkout := filepath.Join(JobDir(r.d.Dir, p.Job), "repo")
		writeCheckout(t, checkout)
		brief := filepath.Join(r.d.Dir, "inbox", p.Job, "BRIEF.md")
		require.NoError(t, os.MkdirAll(filepath.Dir(brief), 0o755))
		require.NoError(t, os.WriteFile(brief, []byte("STATUS: nova-sprint card a.w1\n"), 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(JobDir(r.d.Dir, p.Job), JobFile), []byte(stageRecordText(checkout, brief)), 0o644))
		return strings.Repeat("a", 40), nil
	}
	r.run(t, 1)
	r.run(t, 3)
	r.mu.Lock()
	assert.Equal(t, 2, calls, "staged again on the next Run, with no StageRetryEvery wait")
	r.mu.Unlock()
	assert.Empty(t, r.adaGot(t), "a stop is no judgment")
	for _, l := range r.records {
		assert.NotContains(t, l, "not staged", "a stop is not said")
	}
	assert.True(t, Staged(r.d.Dir, "a.w1~15"))
}

// PacketOf reads the server's fields first and the brief's lines for the rest, and stages
// nothing for a read or a brief with no REPO; a packet git could misread is refused.
func TestPacketOfAndItsRefusals(t *testing.T) {
	t.Parallel()
	p, ok := PacketOf(stagedCard("a.w1", "working", "o/r", "dev"))
	require.True(t, ok)
	assert.Equal(t, Packet{Card: "a.w1", Job: "a.w1~15", Repo: "o/r", Base: "dev", Branch: "sprint/a.w1.g1.e15", Attempt: 1}, p)
	_, ok = PacketOf(workCard("b.w1", "working"))
	assert.False(t, ok, "no REPO: nothing to stage")
	_, ok = PacketOf(HeldCard{Card: "r", Job: "r", Kind: "read", Brief: "STATUS: nova-sprint card r\nREPO: o/r\n"})
	assert.False(t, ok, "a read is not staged here")
	for _, bad := range []Packet{
		{Card: "x", Job: "x~15", Repo: "-o/r", Base: "dev", Branch: "b"},
		{Card: "x", Job: "x~15", Repo: "o/r", Base: "--upload-pack=x", Branch: "b"},
		{Card: "x", Job: "x~15", Repo: "o/r", Base: "dev", Branch: "a/../b"},
		{Card: "x", Job: "../x", Repo: "o/r", Base: "dev", Branch: "b"},
	} {
		_, err := (&Stager{Dir: t.TempDir()}).Stage(context.Background(), bad)
		var ns *NotStageable
		require.ErrorAs(t, err, &ns, "%+v", bad)
		assert.Equal(t, "x", ns.Card)
	}
}

// A card tree pins its base as `BASE: <ref>@<sha40>`: PacketOf reads it through the tree's one
// header reader (cardhdr), the ref as the base and the sha as its pin, and the job is staged at
// the pinned commit even after the ref has moved on; a pin the repository does not hold is
// the card's judgment, never a checkout of the ref's tip.
func TestAPinnedBaseIsStagedAtItsSha(t *testing.T) {
	t.Parallel()
	g := testkit.Git(t, 1)
	g.Commit(g.Clones[0], map[string]string{"README.md": "the tools\n"})
	env := stageEnv(t)
	gitIn(t, env, g.Clones[0], "push", "-q", g.Remote, "HEAD:refs/heads/sprint/mechanical")
	pinned := gitIn(t, env, g.Remote, "rev-parse", "sprint/mechanical")
	g.Commit(g.Clones[0], map[string]string{"later.md": "landed after the pin\n"})
	gitIn(t, env, g.Clones[0], "push", "-q", g.Remote, "HEAD:refs/heads/sprint/mechanical")
	require.NotEqual(t, pinned, gitIn(t, env, g.Remote, "rev-parse", "sprint/mechanical"), "the ref moved on")

	p, ok := PacketOf(stagedCard("pin.w1", "working", "mas-bandwidth/nova-tools", "sprint/mechanical@"+pinned))
	require.True(t, ok)
	assert.Equal(t, Packet{Card: "pin.w1", Job: "pin.w1~15", Repo: "mas-bandwidth/nova-tools", Base: "sprint/mechanical", BaseSha: pinned,
		Branch: "sprint/pin.w1.g1.e15", Attempt: 1}, p)

	dir := t.TempDir()
	stager := &Stager{Dir: dir, Env: env, URL: func(string) string { return g.Remote }}
	sha, err := stager.Stage(context.Background(), p)
	require.NoError(t, err)
	assert.Equal(t, pinned, sha)
	checkout := filepath.Join(dir, "jobs", p.Job, "repo")
	assert.Equal(t, pinned, gitIn(t, env, checkout, "rev-parse", "HEAD"), "staged at the pin, not the ref's tip")
	assert.Equal(t, p.Branch, gitIn(t, env, checkout, "rev-parse", "--abbrev-ref", "HEAD"))
	assert.NoFileExists(t, filepath.Join(checkout, "later.md"))

	// a pin the repository does not hold, and a pin that is no full sha
	gone := p
	gone.Job, gone.BaseSha = "gone.w1~15", strings.Repeat("e", 40)
	_, err = stager.Stage(context.Background(), gone)
	var ns *NotStageable
	require.ErrorAs(t, err, &ns)
	assert.Contains(t, ns.Why, gone.BaseSha)
	assert.NoFileExists(t, filepath.Join(dir, "jobs", gone.Job, "JOB.md"))
	short, ok := PacketOf(stagedCard("short.w1", "working", "mas-bandwidth/nova-tools", "sprint/mechanical@abc123"))
	require.True(t, ok)
	_, err = stager.Stage(context.Background(), short)
	require.ErrorAs(t, err, &ns, "a short pin is refused, never read as the ref")
}
