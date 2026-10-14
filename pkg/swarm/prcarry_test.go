package swarm

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// A card that finishes an open pull request stages from the PR's own head, not from its
// base: the head rides a machine-read CARRY: line, and a brief that names a PR and
// carries none is refused at add with the fix named, so a flash worker cannot come back
// one commit off the base having built nothing on the PR's branch.
func TestLintBriefRefusesAFinishPRCardWithNoCarry(t *testing.T) {
	t.Parallel()
	const head = "0123456789abcdef0123456789abcdef01234567"
	base := "finish pr 1, tier: pro\nREPO: example/repo\nBASE: main\nPATHS: a.go\nPR: example/repo#1\n\nTHE TASK. Start from the PR's branch pr-1 at " + head + " and finish it, merging the base in.\n"

	fs, _ := LintBrief([]byte(base), BriefBase{})
	var got []string
	for _, f := range fs {
		if f.Check == "pr-carry" {
			got = append(got, f.Excerpt)
		}
	}
	assert.Len(t, got, 1, "an uncarried finish-PR brief is refused")
	assert.Contains(t, got[0], "CARRY:", "the finding names the fix")
	assert.Contains(t, BriefRemedy("pr-carry"), "CARRY:", "the remedy names the fix")

	// the same brief with the machine-read line passes
	carried := strings.Replace(base, "PR: example/repo#1\n", "PR: example/repo#1\nCARRY: head="+head+" branch=pr-1\n", 1)
	fs, _ = LintBrief([]byte(carried), BriefBase{})
	for _, f := range fs {
		assert.NotEqual(t, "pr-carry", f.Check, "a carried brief passes this check")
	}

	// a brief with no PR: line, whose THE TASK names the PR's branch, is refused too:
	// the prose the first finish-PR cards were staged with is what the lint catches
	prose := "fix a, tier: pro\nREPO: example/repo\nBASE: main\nPATHS: a.go\n\nTHE TASK. Start from the PR's branch pr-1 at " + head + " and then stop.\n"
	fs, _ = LintBrief([]byte(prose), BriefBase{})
	found := false
	for _, f := range fs {
		found = found || f.Check == "pr-carry"
	}
	assert.True(t, found, "a finish-PR brief named in prose alone is refused")

	// an ordinary card that names no PR is not held to the carry
	plain := "fix a, tier: pro\nREPO: example/repo\nBASE: main\nPATHS: a.go\n\nTHE TASK. Change a.go.\n"
	fs, _ = LintBrief([]byte(plain), BriefBase{})
	for _, f := range fs {
		assert.NotEqual(t, "pr-carry", f.Check, "a card that names no PR is not held")
	}
	assert.True(t, NamesAFinishPR([]byte(base)))
	assert.False(t, NamesAFinishPR([]byte(plain)))
}
