//go:build functional

package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/member"
)

// A member restart after a push attempt can recover the same working packet,
// hence the same launch name. This seeds refs retained after an interrupted
// cleanup; it does not start or kill a member process. The recovered checkout
// no longer has the old child branch, so its old commit is not this checkout's
// head even though the member's persistent push repository still holds it.
func TestRetryRefusesAHeadOnlyOnARetainedLaunchRef(t *testing.T) {
	t.Parallel()
	b := newPushBench(t)
	oldHead := b.commit(t, "first child's work\n")
	g := b.pusher()
	repo, err := g.repo(b.origin)
	require.NoError(t, err)
	runGit(t, repo, "config", "fetch.prune", "false") // the production fetch does not request pruning
	ns := "refs/member/" + launchName(b.p)

	// Model a prior Push stopped after fetch and before its deferred drop.
	// The same namespace is reused by a recovered packet at this gen and epoch.
	runGit(t, repo, "fetch", "-q", "--no-tags", "--no-write-fetch-head", "--", b.checkout,
		"+HEAD:"+ns+"/HEAD", "+refs/heads/*:"+ns+"/heads/*")
	require.Equal(t, oldHead, strings.TrimSpace(runGit(t, repo, "rev-parse", ns+"/heads/rowan/c1")))

	// Model the recovered child's fresh checkout: it is still staged at the
	// original base, but the old branch is gone. The retained push ref is the
	// only ref left in this launch's namespace that contains oldHead.
	gitAs(t, b.checkout, "switch", "-q", "main")
	gitAs(t, b.checkout, "branch", "-D", "rowan/c1")
	require.Equal(t, b.base, gitAs(t, b.checkout, "rev-parse", "HEAD"))
	require.Empty(t, strings.TrimSpace(runGit(t, b.checkout, "for-each-ref", "--format=%(refname)", "--contains", oldHead, "refs/heads/")))

	got := g.Push(b.p, member.Result{Head: oldHead})
	assert.Empty(t, got.Sha, "a retained ref must not authorize this checkout's push")
	assert.NotEmpty(t, got.Refused, "the reported head is absent from the current checkout")
	assert.Empty(t, b.originHas(t, b.p.Branch), "no target branch may be written")
}
