package friend

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/testkit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// carryEnv is stageEnv with a fixed git identity, so the carry's merge can commit.
func carryEnv(t *testing.T) []string {
	t.Helper()
	return append(stageEnv(t),
		"GIT_AUTHOR_NAME=testkit", "GIT_AUTHOR_EMAIL=testkit@example.com",
		"GIT_COMMITTER_NAME=testkit", "GIT_COMMITTER_EMAIL=testkit@example.com")
}

// carriedCard is a held work card whose brief carries a CARRY: line naming head, as the member
// writes it for a rework (member.CarryLine).
func carriedCard(card, repo, base, head string) HeldCard {
	h := stagedCard(card, "working", repo, base)
	h.Brief = strings.Replace(h.Brief, "BASE: "+base+"\n", "BASE: "+base+"\nCARRY: "+card+" attempt 1 head="+head+"\n", 1)
	return h
}

// A card that carries a CARRY: line is staged at the base tip with the carried head merged onto
// it: a clean merge is one commit named `carry <head> onto <base tip>`, the checkout starts from
// it, and JOB.md names it.
func TestACarryMergesThePriorHeadOntoTheBaseTipAsOneCommit(t *testing.T) {
	t.Parallel()
	g := testkit.Git(t, 1)
	clone := g.Clones[0]
	env := carryEnv(t)

	g.Commit(clone, map[string]string{"a.txt": "base\n"})
	gitIn(t, env, clone, "push", "-q", g.Remote, "HEAD:refs/heads/sprint/mechanical")
	base0 := gitIn(t, env, g.Remote, "rev-parse", "sprint/mechanical")

	// the prior attempt's head, on the old base
	g.Commit(clone, map[string]string{"b.txt": "work\n"})
	carried := gitIn(t, env, clone, "rev-parse", "HEAD")
	gitIn(t, env, clone, "push", "-q", g.Remote, "HEAD:refs/heads/sprint/card.g1.e15")

	// the base moves on, from the old base
	gitIn(t, env, clone, "reset", "--hard", base0)
	g.Commit(clone, map[string]string{"c.txt": "moved\n"})
	baseTip := gitIn(t, env, clone, "rev-parse", "HEAD")
	gitIn(t, env, clone, "push", "-q", g.Remote, "HEAD:refs/heads/sprint/mechanical")

	p, ok := PacketOf(carriedCard("card.w1", "mas-bandwidth/nova-tools", "sprint/mechanical", carried))
	require.True(t, ok)
	require.Equal(t, carried, p.Carry)

	dir := t.TempDir()
	stager := &Stager{Dir: dir, Env: env, URL: func(string) string { return g.Remote }}
	sha, err := stager.Stage(context.Background(), p)
	require.NoError(t, err)
	assert.NotEqual(t, baseTip, sha, "a clean merge is a new commit")
	assert.NotEqual(t, carried, sha)

	checkout := filepath.Join(dir, "jobs", p.Job, "repo")
	assert.Equal(t, sha, gitIn(t, env, checkout, "rev-parse", "HEAD"))
	assert.Equal(t, p.Branch, gitIn(t, env, checkout, "rev-parse", "--abbrev-ref", "HEAD"))
	assert.Equal(t, "1", gitIn(t, env, checkout, "rev-list", "--first-parent", "--count", baseTip+"..HEAD"), "one commit on the base tip")
	assert.Equal(t, carryCommitMsg(carried, baseTip), gitIn(t, env, checkout, "log", "-1", "--format=%s", "HEAD"))

	raw, err := os.ReadFile(filepath.Join(dir, "jobs", p.Job, "JOB.md"))
	require.NoError(t, err)
	assert.Contains(t, string(raw), sha, "JOB.md names the carry commit")
}

// A carry whose merge conflicts is not resolved by the stage: the conflicted file stays in the
// checkout, no commit is made (the checkout is still the base tip), JOB.md lists the file and
// the two sides' shas under CONFLICTS, and the lane's prompt opens with that list.
func TestAConflictingCarryListsTheFilesAndMakesNoCommit(t *testing.T) {
	t.Parallel()
	g := testkit.Git(t, 1)
	clone := g.Clones[0]
	env := carryEnv(t)

	g.Commit(clone, map[string]string{"a.txt": "base\n"})
	gitIn(t, env, clone, "push", "-q", g.Remote, "HEAD:refs/heads/sprint/mechanical")
	base0 := gitIn(t, env, g.Remote, "rev-parse", "sprint/mechanical")

	g.Commit(clone, map[string]string{"a.txt": "work\n"})
	carried := gitIn(t, env, clone, "rev-parse", "HEAD")
	gitIn(t, env, clone, "push", "-q", g.Remote, "HEAD:refs/heads/sprint/card.g1.e15")

	gitIn(t, env, clone, "reset", "--hard", base0)
	g.Commit(clone, map[string]string{"a.txt": "moved\n"})
	baseTip := gitIn(t, env, clone, "rev-parse", "HEAD")
	gitIn(t, env, clone, "push", "-q", g.Remote, "HEAD:refs/heads/sprint/mechanical")

	p, ok := PacketOf(carriedCard("card.w1", "mas-bandwidth/nova-tools", "sprint/mechanical", carried))
	require.True(t, ok)

	dir := t.TempDir()
	stager := &Stager{Dir: dir, Env: env, URL: func(string) string { return g.Remote }}
	sha, err := stager.Stage(context.Background(), p)
	require.NoError(t, err)
	assert.Equal(t, baseTip, sha, "no commit: the checkout stays at the base tip")

	checkout := filepath.Join(dir, "jobs", p.Job, "repo")
	assert.Equal(t, baseTip, gitIn(t, env, checkout, "rev-parse", "HEAD"))
	assert.Equal(t, "a.txt", gitIn(t, env, checkout, "diff", "--name-only", "--diff-filter=U"), "the conflicted file is left in the checkout")

	job := filepath.Join(dir, "jobs", p.Job)
	raw, err := os.ReadFile(filepath.Join(job, JobFile))
	require.NoError(t, err)
	assert.Contains(t, string(raw), "CONFLICTS: ours="+baseTip+" theirs="+carried)
	assert.Contains(t, string(raw), "\na.txt\n")

	prompt := CarryPrompt(baseTip, carried, []string{"a.txt"})
	assert.True(t, strings.HasPrefix(prompt, "nova-friend: the carry merge of "+carried+" onto "+baseTip), "the prompt opens with the list: %q", prompt)
	assert.Equal(t, prompt, CarryPromptOf(job), "the lane's prompt is read back from JOB.md")
}

// A CARRY head that no longer exists on the remote (its branch pruned) is a stage fault: the
// card is finished FAIL with the exact line, never started (no JOB.md, no checkout), and the
// judgment names the head.
func TestACarriedHeadThatIsGoneFailsTheCard(t *testing.T) {
	t.Parallel()
	g := testkit.Git(t, 1)
	clone := g.Clones[0]
	env := carryEnv(t)

	g.Commit(clone, map[string]string{"a.txt": "base\n"})
	gitIn(t, env, clone, "push", "-q", g.Remote, "HEAD:refs/heads/sprint/mechanical")

	g.Commit(clone, map[string]string{"b.txt": "work\n"})
	carried := gitIn(t, env, clone, "rev-parse", "HEAD")
	gitIn(t, env, clone, "push", "-q", g.Remote, "HEAD:refs/heads/sprint/card.g1.e15")

	p, ok := PacketOf(carriedCard("card.w1", "mas-bandwidth/nova-tools", "sprint/mechanical", carried))
	require.True(t, ok)

	dir := t.TempDir()
	// First fetch while the carried branch exists. A local mirror retains its object even
	// after the remote branch is later pruned; that must not count as remote existence.
	warm := stagedCard("warm.w1", "working", "mas-bandwidth/nova-tools", "sprint/mechanical")
	warmPacket, ok := PacketOf(warm)
	require.True(t, ok)
	_, err := (&Stager{Dir: dir, Env: env, URL: func(string) string { return g.Remote }}).Stage(context.Background(), warmPacket)
	require.NoError(t, err)
	gitIn(t, env, g.Remote, "update-ref", "-d", "refs/heads/sprint/card.g1.e15")
	stager := &Stager{Dir: dir, Env: env, URL: func(string) string { return g.Remote }}
	_, err = stager.Stage(context.Background(), p)
	var ns *NotStageable
	require.ErrorAs(t, err, &ns)
	assert.Contains(t, ns.Why, carried)
	assert.Contains(t, ns.Why, "is gone")
	assert.NoFileExists(t, filepath.Join(dir, "jobs", p.Job, JobFile), "never started")
	assert.NoDirExists(t, filepath.Join(dir, "jobs", p.Job, "repo"))

	raw, err := os.ReadFile(filepath.Join(dir, "outbox", p.Job, "REPORT.md"))
	require.NoError(t, err)
	assert.Equal(t, "Verdict: FAIL\n\ncarry head "+carried+" is gone\n", string(raw))
}

// An interrupted stage can have moved the base checkout into place before it merged the
// carry or wrote JOB.md. Retrying must merge the carry, not write a base-only JOB.md.
func TestAnInterruptedStageResumesTheCarryBeforeWritingJob(t *testing.T) {
	t.Parallel()
	g := testkit.Git(t, 1)
	clone := g.Clones[0]
	env := carryEnv(t)
	g.Commit(clone, map[string]string{"base.txt": "base\n"})
	gitIn(t, env, clone, "push", "-q", g.Remote, "HEAD:refs/heads/sprint/mechanical")
	g.Commit(clone, map[string]string{"carry.txt": "carry\n"})
	carried := gitIn(t, env, clone, "rev-parse", "HEAD")
	gitIn(t, env, clone, "push", "-q", g.Remote, "HEAD:refs/heads/sprint/card.g1.e15")

	p, ok := PacketOf(carriedCard("card.w1", "mas-bandwidth/nova-tools", "sprint/mechanical", carried))
	require.True(t, ok)
	withoutCarry := p
	withoutCarry.Carry = ""
	dir := t.TempDir()
	stager := &Stager{Dir: dir, Env: env, URL: func(string) string { return g.Remote }}
	base, err := stager.Stage(context.Background(), withoutCarry)
	require.NoError(t, err)
	job := filepath.Join(dir, "jobs", p.Job)
	require.NoError(t, os.Remove(filepath.Join(job, JobFile)))
	sha, err := stager.Stage(context.Background(), p)
	require.NoError(t, err)
	assert.NotEqual(t, base, sha)
	assert.Equal(t, carryCommitMsg(carried, base), gitIn(t, env, filepath.Join(job, "repo"), "log", "-1", "--format=%s", "HEAD"))
	raw, err := os.ReadFile(filepath.Join(job, JobFile))
	require.NoError(t, err)
	assert.Contains(t, string(raw), sha)
}

func TestOneShotCarryCardOpensWithConflicts(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	job := filepath.Join(dir, "job")
	require.NoError(t, os.Mkdir(job, 0o755))
	base, carried := strings.Repeat("a", 40), strings.Repeat("b", 40)
	require.NoError(t, os.WriteFile(filepath.Join(job, JobFile), []byte("# JOB\n"+conflictsSection(base, carried, []string{"conflict.txt"})), 0o644))
	brief := filepath.Join(dir, "BRIEF.md")
	require.NoError(t, os.WriteFile(brief, []byte("STATUS: card\n"), 0o644))
	card := Card{ID: "card.w1", Brief: brief}
	prepared, err := carryOneShotCard(job, card)
	require.NoError(t, err)
	assert.NotEqual(t, card.Brief, prepared.Brief)
	raw, err := os.ReadFile(prepared.Brief)
	require.NoError(t, err)
	assert.Equal(t, CarryPrompt(base, carried, []string{"conflict.txt"})+"STATUS: card\n", string(raw))
	original, err := os.ReadFile(brief)
	require.NoError(t, err)
	assert.Equal(t, "STATUS: card\n", string(original))
}

// A card with no CARRY line is staged as before: no carry head, the checkout at the base, no
// carry commit; and a CARRY line whose head is no full sha is no carry either.
func TestACardWithNoCarryLineStagesAtTheBase(t *testing.T) {
	t.Parallel()
	g := testkit.Git(t, 1)
	clone := g.Clones[0]
	env := carryEnv(t)

	g.Commit(clone, map[string]string{"a.txt": "base\n"})
	gitIn(t, env, clone, "push", "-q", g.Remote, "HEAD:refs/heads/sprint/mechanical")
	baseTip := gitIn(t, env, g.Remote, "rev-parse", "sprint/mechanical")

	p, ok := PacketOf(stagedCard("card.w1", "working", "mas-bandwidth/nova-tools", "sprint/mechanical"))
	require.True(t, ok)
	assert.Equal(t, "", p.Carry)

	dir := t.TempDir()
	stager := &Stager{Dir: dir, Env: env, URL: func(string) string { return g.Remote }}
	sha, err := stager.Stage(context.Background(), p)
	require.NoError(t, err)
	assert.Equal(t, baseTip, sha, "no carry: the checkout is the base tip")
	checkout := filepath.Join(dir, "jobs", p.Job, "repo")
	assert.Equal(t, baseTip, gitIn(t, env, checkout, "rev-parse", "HEAD"))
	assert.Equal(t, "0", gitIn(t, env, checkout, "rev-list", "--count", baseTip+"..HEAD"), "no carry commit")

	// a CARRY line whose head is no full sha is no carry, as member.Carried reads it
	assert.Equal(t, "", carryHead("card.w1 attempt 1 head=abc123"))
	assert.Equal(t, "", carryHead(""))
	assert.Equal(t, strings.Repeat("a", 40), carryHead("card.w1 attempt 1 head="+strings.Repeat("a", 40)))
}
