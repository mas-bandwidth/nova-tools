package main

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/mas-bandwidth/nova-tools/internal/cardcontract"
	"github.com/mas-bandwidth/nova-tools/internal/member"
)

// The frame is the packet's (docs/SPEC-CARD-CONTRACT.md layer 1): the brief's
// header names the repository and base, the packet the branch and attempt, and
// the commit staged is a later attempt's previous pushed head or a read's head
// under read, never a branch name the attempt before may not have pushed.
func TestTheFrameIsThePackets(t *testing.T) {
	t.Parallel()
	brief := "c1: do it (tools) tier: pro\nbase-repo: https://example.com/example-owner/example-repo.git\nBASE: main@" + fullSha + "\n\nThe task."
	work := member.Packet{Card: "c1.w1", Kind: "work", Attempt: 1, Brief: brief, Branch: "sprint/c1.w1"}
	f := frameOf(work, "test/claude-x", "/root")
	assert.Equal(t, "work", f.Kind)
	assert.Equal(t, "https://example.com/example-owner/example-repo.git", f.Repo)
	assert.Equal(t, "main", f.BaseRef)
	assert.Equal(t, fullSha, f.StageSha)
	assert.Equal(t, "sprint/c1.w1", f.Branch)
	assert.Equal(t, "pro", f.Tier)
	assert.Equal(t, "test/claude-x", f.Model)
	assert.Empty(t, f.Stage)
	assert.Empty(t, f.Recipes, "no Stage: line, no recipes directory")
	staged := work
	staged.Brief = "c1: do it\nREPO: o/n\nStage: pr/4926.md\n\nThe task."
	f = frameOf(staged, "m", "/root")
	assert.Equal(t, []string{"pr/4926.md"}, f.Stage)
	assert.Equal(t, "/root/recipes", f.Recipes)

	again := work
	again.Card, again.Attempt, again.Branch, again.Base, again.Fix = "c1.w2", 2, "sprint/c1.w2", "sprint/c1.w1", "f.go:3 the bound"
	f = frameOf(again, "m", "/root")
	assert.Equal(t, fullSha, f.StageSha, "a branch name the attempt before never pushed is not staged")
	assert.Empty(t, f.PrevHead)
	again.BaseHead = pushedSha
	f = frameOf(again, "m", "/root")
	assert.Equal(t, pushedSha, f.StageSha, "the attempt starts from the previous pushed head")
	assert.Equal(t, pushedSha, f.PrevHead)
	assert.Equal(t, "f.go:3 the bound", f.Fix)
	again.Why, again.Finding = "attempt 1 finished and a reader found it broken", "the test is missing"
	f = frameOf(again, "m", "/root")
	assert.Equal(t, "attempt 1 finished and a reader found it broken", f.Why)
	assert.Equal(t, "the test is missing", f.Finding, "the readers' words are the frame's finding, the coordinator's the fix")
	job := cardcontract.For("claude").JobText(f, cardcontract.Staged{Job: "/j", Repo: "/j/repo", Head: pushedSha})
	assert.Contains(t, job, "This attempt exists because: attempt 1 finished and a reader found it broken\nA reader found: the test is missing\nThe coordinator asks: f.go:3 the bound\nDo that first; a finish with no new commit is refused.")
	assert.Equal(t, "sprint/c1.w2", f.Branch)

	read := member.Packet{Card: "c1.r1", Kind: "read", Attempt: 1, Brief: brief, Head: pushedSha, WorkBranch: "sprint/c1.w1"}
	f = frameOf(read, "m", "/root")
	assert.Equal(t, pushedSha, f.StageSha, "a read stages the head under read")
	assert.Equal(t, "sprint/c1.w1", f.Branch)
	assert.Equal(t, "main", f.ReviewBase)
	read.Head = "c1.w1" // a finish that named no head
	assert.Equal(t, fullSha, frameOf(read, "m", "/root").StageSha)
}

// The member names the repository to gh as gh names it: a GitHub URL's
// owner/name in any spelling, another host's host/owner/name, a path as it is.
func TestThePullRequestRepositoryIsGhsName(t *testing.T) {
	t.Parallel()
	for url, want := range map[string]string{
		"https://" + githubHost + "/o/n.git": "o/n",
		"git@" + githubHost + ":o/n.git":     "o/n",
		"ssh://git@" + githubHost + "/o/n":   "o/n",
		"https://example.com/o/n/":           "example.com/o/n",
		"/srv/origin.git":                    "/srv/origin.git",
	} {
		assert.Equal(t, want, prRepo(url), url)
	}
}
