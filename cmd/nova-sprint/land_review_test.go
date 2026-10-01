package main

import (
	"github.com/stretchr/testify/assert"
	"strings"
	"testing"
)

func TestLandReviewRefusesWrongEpochBeforePush(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 1")
	r.queued(map[string]string{"s1-1": r.head("s1-1", "main", "a.txt", "a\n")}, "s1-1")
	before := r.git(r.remote, "rev-parse", "main")
	code, out, errs := r.do("land --repo-dir " + r.clone + " --base main --epoch 999")
	assert.NotEqual(t, 0, code, out+errs)
	assert.Equal(t, before, r.git(r.remote, "rev-parse", "main"), "an already-invalid epoch must refuse before external delivery: %s", out+errs)
}

func TestLandReviewGitIdentityFailureDoesNotBlameCard(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 1")
	r.queued(map[string]string{"s1-1": r.head("s1-1", "main", "a.txt", "a\n")}, "s1-1")
	before := r.streamState("s1")
	env := []string{}
	for _, e := range r.env {
		if !strings.HasPrefix(e, "GIT_AUTHOR_NAME=") && !strings.HasPrefix(e, "GIT_COMMITTER_NAME=") {
			env = append(env, e)
		}
	}
	r.a.gitEnv = append(env, "GIT_AUTHOR_NAME=", "GIT_COMMITTER_NAME=")
	code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
	assert.NotEqual(t, 0, code, out+errs)
	assert.Equal(t, before, r.streamState("s1"), "environment failure must leave queue unchanged: %s", out+errs)
	assert.Equal(t, map[string]string{"s1-1": "merging/queued"}, r.places("s1-1"))
}

func TestLandReviewDifferentRepositoriesHaveDifferentCloneNames(t *testing.T) {
	t.Parallel()
	assert.NotEqual(t, repoDirName("https://example.invalid/a-b/c.git"), repoDirName("https://example.invalid/a/b-c.git"))
}
