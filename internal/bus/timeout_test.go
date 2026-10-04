package bus

import (
	"errors"
	"testing"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/subproc"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A git that never returns is a tool that has stopped saying anything. What a call killed on
// its budget SAYS is decided apart from the subprocess (gitFailure), so it is pinned here
// with no git and no clock; killing a real git on a real budget is the functional test
// TestAGitThatHangsIsKilledAndNamed (timeout_functional_test.go).
func TestAGitFailureIsNamed(t *testing.T) {
	t.Parallel()
	args := []string{"fetch", "origin", "main"}
	for _, tc := range []struct {
		name  string
		err   error
		wants []string // nil: no error
		typed bool     // a gitError carrying git's own words
	}{
		{name: "killed on its budget", err: &subproc.TimeoutError{What: "git fetch origin main", Budget: 300 * time.Millisecond, Err: errors.New("signal: killed")},
			wants: []string{"fetch origin main", "did not finish within", "300ms", "--git-timeout"}},
		{name: "git said no", err: errors.New("exit status 128"), wants: []string{"fetch origin main", "exit status 128", "fatal: no remote"}, typed: true},
		{name: "a call inside its budget", err: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := gitFailure("/bus", args, 300*time.Millisecond, "fatal: no remote", tc.err)
			if tc.wants == nil {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			for _, want := range tc.wants {
				assert.Contains(t, err.Error(), want)
			}
			var ge *gitError
			assert.Equal(t, tc.typed, errors.As(err, &ge), "a gitError: %v", err)
		})
	}
}

// A budget of nothing is a bad invocation rather than a call with no budget at all.
func TestAGitBudgetOfNothingIsRefused(t *testing.T) {
	t.Parallel()
	for _, d := range []time.Duration{0, -time.Second} {
		assert.Error(t, SetGitTimeout(d), "a budget of %v was accepted; every call would be killed before it started", d)
	}
}
