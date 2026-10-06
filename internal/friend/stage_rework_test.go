package friend

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reworkCard is a friend's later attempt as friend sync writes its brief (nova-sprint
// friendBrief): the STATUS line of attempt 2, the start line naming the head attempt 1
// pushed (when it pushed), the fix, then the card's own brief with its PATHS, REPO and BASE.
func reworkCard(primary, repo, base, carry, fix string) HeldCard {
	card, job := primary+".w2", primary+".w2~15"
	var b strings.Builder
	fmt.Fprintf(&b, "STATUS: nova-sprint card %s, epoch 15, attempt 2; push your work to the branch sprint/%s.g1.e15; when done, write outbox/%s/REPORT.md with Verdict: LAND|HOLD|FAIL and Head: <sha>\n", card, card, job)
	if carry != "" {
		fmt.Fprintf(&b, "This attempt starts from the current tip of %s on origin, never from an older base: fetch it and start your branch there. Carry the work of attempt 1 onto it yourself: its head, %s, is the last pushed by any attempt before this one.\n", base, carry)
	}
	fmt.Fprintf(&b, "The coordinator asks: %s\n\n%s: the brief\nPATHS: internal/friend/**,docs/SPEC-FRIEND.md\nREPO: %s\nBASE: %s\n", fix, primary, repo, base)
	return HeldCard{Card: card, Job: job, Col: "working", Brief: b.String()}
}

// A rework whose fix names no new files starts in the previous attempt's worktree on the same
// friend: the daemon moves the kept checkout whole into the new job on the card's new branch,
// and the lane is handed the fix as the first line of its card, so it edits and pushes without
// re-learning the tree. When the worktree is gone, the friend differs, the fix names files
// outside PATHS, or the tree has uncommitted changes, the job is staged afresh from the head
// the last attempt pushed (internal/friend/tla/ReworkWorktree.tla).
func TestAReworkReusesTheLastWorktree(t *testing.T) {
	t.Parallel()
	g := testkit.Git(t, 1)
	g.Commit(g.Clones[0], map[string]string{"README.md": "the tools\n"})
	env := stageEnv(t)
	gitIn(t, env, g.Clones[0], "push", "-q", g.Remote, "HEAD:refs/heads/sprint/mechanical")
	base := gitIn(t, env, g.Remote, "rev-parse", "sprint/mechanical")
	const repo = "mas-bandwidth/nova-tools"

	r := newRig(t)
	r.d.Coordinator = "ada"
	dir := r.d.Dir
	stager := &Stager{Dir: dir, Env: env, URL: func(string) string { return g.Remote }}
	ended := make(chan struct{}, 16)
	r.d.Stage = func(ctx context.Context, p Packet) (string, error) {
		defer func() { ended <- struct{}{} }()
		return stager.Stage(ctx, p)
	}

	// attempt 1 of each card on this friend: staged, worked (a commit pushed, a build output
	// left untracked), and ended with its report
	attempt1 := func(primary string) (checkout, head string) {
		p, ok := PacketOf(stagedCard(primary+".w1", "working", repo, "sprint/mechanical"))
		require.True(t, ok)
		_, err := stager.Stage(context.Background(), p)
		require.NoError(t, err)
		checkout = filepath.Join(dir, "jobs", p.Job, "repo")
		g.Commit(checkout, map[string]string{"stage.go": "package friend // attempt 1\n"})
		gitIn(t, env, checkout, "push", "-q", "origin", p.Branch)
		require.NoError(t, os.WriteFile(filepath.Join(checkout, "build.out"), []byte("what attempt 1 built\n"), 0o644))
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "outbox", p.Job), 0o755))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "outbox", p.Job, "REPORT.md"), []byte("Verdict: LAND\n"), 0o644))
		return checkout, gitIn(t, env, checkout, "rev-parse", "HEAD")
	}
	kept, head := attempt1("kept")
	_, wideHead := attempt1("wide")
	dirty, dirtyHead := attempt1("dirty")
	require.NoError(t, os.WriteFile(filepath.Join(dirty, "README.md"), []byte("an edit never committed\n"), 0o644))

	rework := reworkCard("kept", repo, "sprint/mechanical", head, "the help line of stage.go says jobs/, not job/")
	wide := reworkCard("wide", repo, "sprint/mechanical", wideHead, "cite it in cmd/nova-sprint/friendcards.go too")
	dirtied := reworkCard("dirty", repo, "sprint/mechanical", dirtyHead, "one more doc sentence")
	row := &twinRow{}
	r.d.Held = row.held
	row.set(rework, wide, dirtied)
	r.at[1] = func() {
		for range 3 {
			<-ended
		}
	}
	r.run(t, 1) // writes the briefs and stages them
	r.run(t, 1) // says what they did

	// the kept worktree is the rework's checkout: the same tree, moved, on the new branch
	checkout := filepath.Join(dir, "jobs", rework.Job, "repo")
	assert.NoDirExists(t, kept, "moved, never copied: one worktree is one job's")
	assert.FileExists(t, filepath.Join(checkout, "build.out"), "the tree as attempt 1 left it, its build output too")
	assert.Equal(t, head, gitIn(t, env, checkout, "rev-parse", "HEAD"), "at attempt 1's head")
	assert.Equal(t, "sprint/kept.w2.g1.e15", gitIn(t, env, checkout, "rev-parse", "--abbrev-ref", "HEAD"), "on the card's new branch")
	assert.Equal(t, g.Remote, gitIn(t, env, checkout, "remote", "get-url", "origin"))
	raw, err := os.ReadFile(filepath.Join(dir, "jobs", rework.Job, JobFile))
	require.NoError(t, err)
	job := string(raw)
	assert.True(t, strings.HasPrefix(job, "# JOB: work kept.w2, attempt 2\n\nThe fix: the help line of stage.go says jobs/, not job/\n"), "the fix first: %q", job)
	assert.Contains(t, job, "The kept checkout: "+checkout+" (the worktree of the attempt before, job kept.w1~15")
	assert.Contains(t, job, "git push -u origin sprint/kept.w2.g1.e15")
	assert.Equal(t, "the help line of stage.go says jobs/, not job/", KeptFix(dir, rework.Job))
	r.mu.Lock()
	said := strings.Join(r.records, "\n")
	r.mu.Unlock()
	assert.Contains(t, said, "stage: kept the last worktree as jobs/kept.w2~15/repo", "the stage says it kept the tree")

	// the lane opens in it with the fix as the first line it is handed
	c, ok, err := r.d.nextCard(func(c Card) bool { return c.ID != rework.Card })
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "the help line of stage.go says jobs/, not job/", c.Fix)
	text := CardText(c, 1, 1, "nova-bus send ada done", "", "", nil)
	assert.True(t, strings.HasPrefix(text, "The fix, first: the help line of stage.go says jobs/, not job/ ("), "the turn: %q", text)
	brief, err := os.ReadFile(c.Brief)
	require.NoError(t, err)
	prompt := LanePrompt(c, string(brief))
	assert.True(t, strings.HasPrefix(prompt, "The fix, first: "), "a one-shot run: %q", prompt)
	assert.Contains(t, prompt, "\nSTATUS: nova-sprint card kept.w2, ", "then the brief whole")

	// a fix naming a file outside PATHS, and a tree with an uncommitted change: staged afresh
	// at the head attempt 1 pushed, the old tree left as it was
	for _, x := range []struct {
		h          HeldCard
		head, prev string
	}{{wide, wideHead, filepath.Join(dir, "jobs", "wide.w1~15", "repo")}, {dirtied, dirtyHead, dirty}} {
		fresh := filepath.Join(dir, "jobs", x.h.Job, "repo")
		assert.Equal(t, x.head, gitIn(t, env, fresh, "rev-parse", "HEAD"), "%s starts from the carried head", x.h.Card)
		assert.NoFileExists(t, filepath.Join(fresh, "build.out"), "%s is a fresh clone", x.h.Card)
		assert.DirExists(t, x.prev, "%s: the old tree is not taken", x.h.Card)
		assert.Empty(t, KeptFix(dir, x.h.Job))
		raw, err := os.ReadFile(filepath.Join(dir, "jobs", x.h.Job, JobFile))
		require.NoError(t, err)
		assert.Contains(t, string(raw), "(a clone of "+repo+" at the head attempt 1 pushed, "+x.head+", on branch ")
		c, ok, err := r.d.nextCard(func(c Card) bool { return c.ID != x.h.Card })
		require.NoError(t, err)
		require.True(t, ok)
		assert.Empty(t, c.Fix, "%s is handed its brief alone", x.h.Card)
	}
	assert.FileExists(t, filepath.Join(dirty, "README.md"))
	assert.Equal(t, "an edit never committed\n", string(must(os.ReadFile(filepath.Join(dirty, "README.md")))), "an uncommitted change is never moved or lost")

	// another friend (her own working directory: no kept tree there) stages from the carried
	// head; with nothing pushed, at the base
	other := &Stager{Dir: t.TempDir(), Env: env, URL: func(string) string { return g.Remote }}
	p, ok := PacketOf(reworkCard("kept", repo, "sprint/mechanical", head, "the same fix"))
	require.True(t, ok)
	sha, err := other.Stage(context.Background(), p)
	require.NoError(t, err)
	assert.Equal(t, head, sha, "the friend differs: from the carried head")
	p, ok = PacketOf(reworkCard("bare", repo, "sprint/mechanical", "", "the fix"))
	require.True(t, ok)
	sha, err = other.Stage(context.Background(), p)
	require.NoError(t, err)
	assert.Equal(t, base, sha, "no attempt pushed: at the base")
}

// The previous attempt's card is <primary>.w<n-1>; a card of another shape has none.
func TestPreviousCard(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "a.b.w1", PreviousCard("a.b.w2", 2))
	assert.Equal(t, "x.w9", PreviousCard("x.w10", 10))
	assert.Empty(t, PreviousCard("x.w1", 1))
	assert.Empty(t, PreviousCard("x.w3", 2), "the attempt is the card's")
	assert.Empty(t, PreviousCard("x", 2))
}
