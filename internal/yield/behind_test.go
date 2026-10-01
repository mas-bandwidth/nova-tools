//go:build darwin || linux

package yield

import (
	"errors"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAlreadyBehindIsOnlyARefusalToRaise: a failed setpriority is let pass only
// when the kernel refused to raise priority (EACCES, EPERM, wrapped or not) and
// the process or thread judged is already at Nice or more; a refusal to raise one
// still at 0, or any other error, is a failure (the #5026 reader's finding: one
// thread's reading must not vouch for threads left at 0).
func TestAlreadyBehindIsOnlyARefusalToRaise(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		err  error
		now  int
		want bool
	}{
		{"EACCES at 19", syscall.EACCES, 19, true},
		{"EPERM at 15", syscall.EPERM, 15, true},
		{"wrapped EACCES at 19", errors.Join(errors.New("thread 7"), syscall.EACCES), 19, true},
		{"EACCES at 0", syscall.EACCES, 0, false},
		{"EACCES at 14", syscall.EACCES, 14, false},
		{"ESRCH at 19", syscall.ESRCH, 19, false},
		{"EINVAL at 19", syscall.EINVAL, 19, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			assert.Equal(t, c.want, alreadyBehind(c.err, c.now, Nice))
		})
	}
}

// TestToCILeavesAProcessAlreadyBehind: a process already above Nice (this test
// under `nice -n 19`) is refused the raise by the kernel, and ToCI succeeds and
// leaves it where it is. A run below Nice cannot exercise this path.
func TestToCILeavesAProcessAlreadyBehind(t *testing.T) {
	t.Parallel()
	before, err := currentNice()
	require.NoError(t, err)
	if before <= Nice {
		t.Skipf("at nice %d, not above %d: nothing to refuse (TestToCIStepsThisProcessDownToNice tests the step)", before, Nice)
	}
	require.NoError(t, ToCI(), "a process already behind CI is left there")
	n, err := currentNice()
	require.NoError(t, err)
	assert.Equal(t, before, n)
}
