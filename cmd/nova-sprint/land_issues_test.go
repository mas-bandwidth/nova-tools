package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/github"
)

// keptGitHub closes every issue asked and keeps each comment.
type keptGitHub struct{ comments map[string]string }

func (f *keptGitHub) Close(_ context.Context, i github.Issue, comment string) (bool, error) {
	if _, ok := f.comments[i.String()]; ok {
		return true, nil
	}
	f.comments[i.String()] = comment
	return false, nil
}

// land closes the issues of the cards it landed as its pass ends: the brief's, and the
// "Closes" of the commits the card's merge brought, each with one comment naming the card,
// its stream and the landing commit, the base's tip it pushed; its report says so on a
// LAND ISSUES line (docs/SPEC-SPRINT.md section 7, "A landing closes the card's issues").
func TestLandClosesTheIssuesOfTheBriefAndTheLandedCommits(t *testing.T) {
	t.Parallel()
	r := newLandRig(t)
	f := &keptGitHub{comments: map[string]string{}}
	r.a.issueGitHub = f
	path := filepath.Join(t.TempDir(), "a.md")
	require.NoError(t, os.WriteFile(path, []byte(passingBrief("REPO: "+r.remote+"\nBASE: main\nISSUES: "+github.Web+"mas-bandwidth/ideas/issues/7\n\nWrite a.txt.")), 0o600))
	r.ok("add --stream s1 a --one --brief-file " + path)
	r.git(r.worker, "switch", "-q", "--no-track", "-c", "sprint/a", "refs/remotes/origin/main")
	head := r.commit("a.txt", "a\n", "work of a\n\nCloses "+github.Web+"mas-bandwidth/ideas/issues/8")
	r.queued(map[string]string{"a": head}, "a")

	out := r.ok("land --repo-dir " + r.clone + " --base main")
	tip := r.git(r.remote, "rev-parse", "main")
	assert.Contains(t, out, "LAND OK stream=s1 cards=1 base=main tip="+tip)
	assert.Contains(t, out, "LAND ISSUES a issues: closed mas-bandwidth/ideas#7, mas-bandwidth/ideas#8")
	require.Len(t, f.comments, 2)
	for i, c := range f.comments {
		assert.Contains(t, c, "card: a\n- stream: s1\n- landing commit: "+tip+"\n", "%s's comment", i)
	}
	assert.Equal(t, map[string]string{"a": "landed/merged"}, r.places("a"))
	r.clean()
}
