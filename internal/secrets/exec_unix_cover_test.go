//go:build !windows

package secrets

// The unit cover for exec_unix.go's replaceProcess. Its success path calls
// syscall.Exec, which replaces the running image and never returns, so no unit
// test can reach it: only the two refusals and the resolved-path branch that
// reaches the exec call and gets its error back are reachable in process. The
// refusal "no command specified" and the LookPath refusal are covered here, as
// is a resolved executable whose exec fails (a file that is not a program), so
// the process is not replaced.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestExecUnixCoverReplaceProcessRefusals pins replaceProcess's refusals: an
// empty argv names the missing command, and an argv whose program LookPath
// cannot resolve carries the not-found error instead of an exec attempt.
func TestExecUnixCoverReplaceProcessRefusals(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		argv []string
		want string
	}{
		{"no command at all", nil, "no command specified"},
		{"command not on PATH", []string{"nova-secrets-no-such-command-cover"}, "executable file not found"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := replaceProcess(tc.argv, nil)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

// TestExecUnixCoverReplaceProcessReachesExecOnAResolvedPath pins the reachable
// part of the main path: a program LookPath resolves reaches the exec call, and
// a file that is not a program makes that call return its error rather than
// replace the test process. The successful exec -- the process becoming the
// command -- is out of the unit tier because it never returns to the test.
func TestExecUnixCoverReplaceProcessReachesExecOnAResolvedPath(t *testing.T) {
	t.Parallel()

	notAProgram := filepath.Join(t.TempDir(), "not-a-program")
	require.NoError(t, os.WriteFile(notAProgram, []byte("no interpreter, no ELF, no program format\n"), 0o755))

	err := replaceProcess([]string{notAProgram}, nil)
	require.Error(t, err, "a file that is not a program must fail the exec call, not replace the test process")
	assert.True(t, strings.Contains(strings.ToLower(err.Error()), "exec format error"),
		"the exec call's error must say the file is not a program, got: %s", err)
}
