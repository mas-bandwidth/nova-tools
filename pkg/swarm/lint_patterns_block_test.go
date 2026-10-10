package swarm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// patternsCard is the card with a PATTERNS TO REFUSE paragraph and a TEST line appended
// (docs/SPEC-CARD-CONTRACT.md, lint-allows-quoted-patterns-in-tests).
func patternsCard(t *testing.T, test, block string) string {
	t.Helper()
	return ourCard(t) + "TEST: " + test + "\n\n" + block
}

func patternsChecks(t *testing.T, card string) []string {
	t.Helper()
	return childChecks(LintCardChildWith([]byte(card), ourRules(t)))
}

const (
	classTestLine = "./internal/ci TestNoForcePushClass in internal/ci/nopush_class_test.go"
	plainTestLine = "./pkg/swarm TestSomething"
	quotedBlock   = "PATTERNS TO REFUSE. The class test refuses `git push --force` and `kill -9 1234`.\n"
)

// A class-test card carries the block and passes; the same block in a plain card is a
// finding, and the block's quoted literals are not scanned there either way.
func TestPatternsToRefuseBlockIsExemptForAClassTestCard(t *testing.T) {
	t.Parallel()
	t.Run("class test card passes", func(t *testing.T) {
		t.Parallel()
		assert.Empty(t, patternsChecks(t, patternsCard(t, classTestLine, quotedBlock)))
	})
	t.Run("plain card fails", func(t *testing.T) {
		t.Parallel()
		got := patternsChecks(t, patternsCard(t, plainTestLine, quotedBlock))
		assert.Contains(t, got, PatternsBlockCheck)
	})
	t.Run("a class test named in PATHS counts", func(t *testing.T) {
		t.Parallel()
		card := ourCard(t) + "PATHS: internal/ci/nopush_class_test.go\n\n" + quotedBlock
		assert.Empty(t, patternsChecks(t, card))
	})
	t.Run("unquoted command in the block fires", func(t *testing.T) {
		t.Parallel()
		block := "PATTERNS TO REFUSE. The test refuses `git stash` and also run git push --force here.\n"
		assert.Equal(t, []string{"step-force-push"}, patternsChecks(t, patternsCard(t, classTestLine, block)))
	})
	t.Run("scan outside the block still fires", func(t *testing.T) {
		t.Parallel()
		card := patternsCard(t, classTestLine, quotedBlock) + "\nSTEP 9. git push --force\n"
		assert.Equal(t, []string{"step-force-push"}, patternsChecks(t, card))
	})
	t.Run("the block ends at the blank line", func(t *testing.T) {
		t.Parallel()
		card := patternsCard(t, classTestLine, quotedBlock+"\n`git stash` then git stash\n")
		assert.Equal(t, []string{"step-stash"}, patternsChecks(t, card))
	})
	t.Run("the finding has a remedy", func(t *testing.T) {
		t.Parallel()
		assert.NotEmpty(t, ChildRemedy(nil, PatternsBlockCheck))
	})
}
