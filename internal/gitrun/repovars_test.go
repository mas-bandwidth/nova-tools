package gitrun

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestWithoutRepoVarsDropsOnlyTheLocationVariables pins which entries survive: every
// variable that says where a repository is goes, every other one (a prompt switch, a
// name that only starts like one, a value that mentions one) stays, in order.
func TestWithoutRepoVarsDropsOnlyTheLocationVariables(t *testing.T) {
	t.Parallel()
	in := []string{
		"PATH=/bin", "GIT_DIR=/x/.git", "GIT_WORK_TREE=/x", "GIT_OBJECT_DIRECTORY=/x/o",
		"GIT_ALTERNATE_OBJECT_DIRECTORIES=/y", "GIT_INDEX_FILE=/x/i", "GIT_COMMON_DIR=/x",
		"GIT_NAMESPACE=n", "GIT_PREFIX=p/", "GIT_CEILING_DIRECTORIES=/", "GIT_TERMINAL_PROMPT=0",
		"GIT_DIRECTORY=keep", "NOTE=GIT_DIR=/x",
	}
	want := []string{"PATH=/bin", "GIT_TERMINAL_PROMPT=0", "GIT_DIRECTORY=keep", "NOTE=GIT_DIR=/x"}
	assert.Equal(t, want, WithoutRepoVars(in))
	assert.Len(t, in, 13, "the argument is not changed")
}
