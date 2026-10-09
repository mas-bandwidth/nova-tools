package main

import (
	"context"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The server checks a broken read's branch at its close (docs/SPEC-SPRINT.md section 6, a
// read on a branch origin does not hold): the read verb asks origin's tip of the branch the
// read's packet names, and when origin holds none, finds the work card's branch that holds
// its head; the step retires the read with no verdict and asks it again on that branch.
func TestTheReadVerbChecksABrokenReadsBranchOnOrigin(t *testing.T) {
	t.Parallel()
	const head = "0123456789abcdef0123456789abcdef01234567"
	r := newServerRig(t,
		"nova-sprint init --readers reader-m2 --members m1:1,m2:1",
		"nova-sprint add --one --stream s1 --count 1 --brief-file "+writeBrief(t, "tier: flash\nREPO: mas-bandwidth/nova-tools"),
		"nova-sprint start",
		"nova-sprint tick",
	)
	r.queue("reader-m2")
	done := 0
	for i := 0; i < 4 && done == 0; i++ {
		for _, m := range []string{"m1", "m2"} {
			for _, c := range taken(t, r.one("take", "--as", m, "--limit", "1", "--epoch", "0", "--json")) {
				res := r.one("finish", "--as", m, c, "--epoch", "0", "--report", "done", "--head", head)
				require.Equal(t, 0, res.Code, res.Stderr)
				done++
			}
		}
		r.boss("nova-sprint tick")
	}
	require.Equal(t, 1, done, "the fixture: the card's work finished")
	r.boss("nova-sprint tick")
	asked := r.queue("reader-m2")["asked"]
	require.Len(t, asked, 1, "the fixture: one read asked")
	var tipped []string
	r.a.readTip = func(_ context.Context, repo, branch string) (string, error) {
		tipped = append(tipped, repo+" "+branch)
		return "", nil // origin holds no such branch
	}
	holds := "sprint/s1-1.w1.g7.e0"
	r.a.readHeads = func(_ context.Context, _, pattern string) (map[string]string, error) {
		assert.Equal(t, "refs/heads/sprint/s1-1.w1.g*", pattern)
		return map[string]string{holds: head, "sprint/s1-1.w1.g8.e0": strings.Repeat("f", 40)}, nil
	}
	res := r.one("read", "--as", "reader-m2", "--broken", asked[0], "--epoch", "0", "--finding", "RULE: the branch this read names is not on origin; there is no commit to judge")
	require.Equal(t, 0, res.Code, res.Stderr)
	require.Len(t, tipped, 1, "one tip read for the one read")
	assert.True(t, strings.HasSuffix(tipped[0], "mas-bandwidth/nova-tools.git sprint/s1-1.w1.g1.e0"), "the tip of the branch the read named, in the card's repository: %s", tipped[0])
	assert.Contains(t, res.Stdout, sprint.RetiredByMissingBranch, "the read is retired, not judged")

	r.boss("nova-sprint tick")
	again := r.queue("reader-m2")["asked"]
	require.Len(t, again, 1, "the read is asked again")
	card := r.boss("nova-sprint card s1-1 --json")
	assert.Contains(t, card, holds, "the primary records the branch origin holds the head on")
	assert.NotContains(t, card, "a reader found it broken")
}
