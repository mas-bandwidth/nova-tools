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

func TestLandReviewReportPinsTheHeadItPushed(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 --count 1")
	oldHead := r.head("s1-1", "main", "a.txt", "old\n")
	r.queued(map[string]string{"s1-1": oldHead}, "s1-1")
	newHead := r.head("s1-1-next", "main", "a.txt", "new\n")
	r.git(r.worker, "push", "-q", "origin", "refs/heads/sprint/*:refs/heads/sprint/*")
	r.a.beforePush = func(int) {
		r.ok("return s1-1 --reason 'needs a new head'")
		r.ok("rework s1-1 --fix 'replace the old head'")
		r.deal(1)
		r.ok("take --as m1 --limit 100")
		r.ok("finish --as m1 s1-1.w2@1 --head " + newHead)
		r.ok("ask")
		r.ok("read --as reader-a --ok --limit 100")
		r.ok("read --as reader-b --ok --limit 100")
		r.ok("accept --read-ok")
	}
	code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
	assert.Equal(t, "old", r.git(r.remote, "show", "main:a.txt"), "the first attempt was already built")
	assert.NotEqual(t, 0, code, "new attempt must not be reported landed by old push: %s%s", out, errs)
	assert.Equal(t, "merging/queued", r.places("s1-1")["s1-1"], "%s%s", out, errs)
}

func TestLandReviewReusedOperationCannotClaimAnotherBatchLanded(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	r.ok("add --stream s1 first")
	r.queued(map[string]string{"first": r.head("first", "main", "first.txt", "first\n")}, "first")
	r.ok("land --repo-dir " + r.clone + " --base main --op repeated-land")
	r.ok("add --stream s1 second")
	r.queued(map[string]string{"second": r.head("second", "main", "second.txt", "second\n")}, "second")
	code, out, errs := r.do("land --repo-dir " + r.clone + " --base main --op repeated-land")
	if code == 0 {
		assert.Equal(t, "landed/merged", r.places("second")["second"], "LAND OK must describe the batch reported: %s%s", out, errs)
	}
}
