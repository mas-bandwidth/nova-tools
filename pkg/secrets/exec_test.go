package secrets

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRunExecRefusalNamesEveryMissingRequiredFlagTogether pins 6dbfe26e (#1477):
// RunExec names every missing required flag in one refusal, plus a pasteable
// example, rather than returning on the first empty string. Reverting exec.go
// left ./pkg/secrets green because nothing in the package called RunExec.
func TestRunExecRefusalNamesEveryMissingRequiredFlagTogether(t *testing.T) {
	t.Parallel()

	code, err := RunExec("", "", "", "", "", nil, []string{os.Args[0]})
	require.Equal(t, 125, code, "expected exit 125, got %d err=%v", code, err)
	require.Error(t, err, "expected a refusal, got nil")
	msg := err.Error()
	for _, flag := range []string{"--store", "--as", "--key", "--sops", "--only"} {
		assert.Contains(t, msg, flag, "refusal must name %s together, got: %s", flag, msg)
	}
	assert.Contains(t, msg, "example:", "refusal must carry a pasteable example invocation, got: %s", msg)
	assert.Contains(t, msg, "nova-secrets exec", "refusal must carry a pasteable example invocation, got: %s", msg)
}

// TestAFailedExecveSaysTheCommandNeverStarted pins security#64 finding 4: the OK
// line is on stderr before the exec call, and a successful exec never returns, so
// when the exec call does return -- LookPath already passed, so ENOEXEC and its
// friends -- the command never started, and the FAIL line the caller prints from
// this error must say exactly that.
func TestAFailedExecveSaysTheCommandNeverStarted(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("windows has no execve: replaceProcess spawns and waits, so this finding does not arise there")
	}

	s := newCheckStore(t, map[string]string{
		"recovery.pub": pubRecovery + "\n",
		".sops.yaml":   "creation_rules:\n  - path_regex: ^rowan\\.yaml$\n    age: " + pubRowan + "," + pubRecovery + "\n",
		"rowan.yaml":   fakeCipher([]string{pubRowan, pubRecovery}, "GH_TOKEN: ghp_execve\n"),
	})
	// A file LookPath accepts (it exists and is executable) whose bytes are no
	// program: no shebang, no ELF, no format the kernel loads. The exec call then
	// returns after the OK line is already out.
	neverStarted := filepath.Join(t.TempDir(), "no-exec-format")
	mustWrite(t, neverStarted, "no interpreter, no ELF, no program format\n", 0o755)

	code, err := RunExec(s.dir, "rowan", s.key, s.sops, "GH_TOKEN", nil, []string{neverStarted})
	require.Equal(t, 125, code, "expected exit 125, got %d err=%v", code, err)
	require.Error(t, err, "a failed execve must be a failure, got nil")
	assert.Contains(t, err.Error(), "the command never started",
		"the error the FAIL line is printed from must say the command never started, got: %s", err)
}
