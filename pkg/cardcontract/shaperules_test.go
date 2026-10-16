package cardcontract

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// Every job that asks for a RESULT.md says, right after the shape, what makes the file
// refused (measured: a worker wrote a RESULT line and the word DONE, another left out the
// report line), and a work job says which commit the head is.
func TestAJobStatesTheResultShapeRulesAfterTheShape(t *testing.T) {
	t.Parallel()
	s := Staged{Job: "/j", Repo: "/j/repo", Head: "0123456789abcdef0123456789abcdef01234567"}
	for _, family := range []string{"plain", "claude"} {
		work := For(family).JobText(Frame{Kind: "work", Card: "c1", Attempt: 1, BaseRef: "main", Branch: "sprint/c1"}, s)
		assert.Contains(t, work, ShapeRules, family+" work")
		assert.Contains(t, work, ShapeWorkRule, family+" work")
		assert.Contains(t, work, "line 1 is `head:`", family+" work")
		assert.Contains(t, work, "refused as `no RESULT.md shape`", family+" work")
	}
	read := For("plain").JobText(Frame{Kind: "read", Card: "c1", Attempt: 1, Branch: "sprint/c1", ReviewBase: "main"}, s)
	assert.Contains(t, read, ShapeRules, "plain read")
	assert.NotContains(t, read, ShapeWorkRule, "a read makes no commit")
}
