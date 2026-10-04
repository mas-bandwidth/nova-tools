//go:build linux

package bus

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The real /proc: this process's own entry is owned by its effective uid, and
// readProcView records that owner beside the comm it reads.
func TestLinuxProcSelfOwnerIsEffectiveUID(t *testing.T) {
	t.Parallel()
	pid := strconv.Itoa(os.Getpid())
	self := uint32(os.Geteuid())
	uid, ok, err := linuxProcOwner(pid)
	require.NoError(t, err, "/proc/%s owner: uid=%d ok=%v err=%v, want %d", pid, uid, ok, err, self)
	require.True(t, ok, "/proc/%s owner: uid=%d ok=%v err=%v, want %d", pid, uid, ok, err, self)
	require.Equal(t, self, uid, "/proc/%s owner: uid=%d ok=%v err=%v, want %d", pid, uid, ok, err, self)
	v := readProcView(pid, linuxProcReader)
	require.True(t, v.ownerKnown, "our own entry: %+v, want owner %d and comm read", v, self)
	require.Equal(t, self, v.owner, "our own entry: %+v, want owner %d and comm read", v, self)
	require.NotEqual(t, "", strings.TrimSpace(v.comm), "our own entry: %+v, want owner %d and comm read", v, self)
	// The real status of this process: its Uid line states the effective uid, which is the
	// account the scan compares (readProcView reads it only for a git, so here directly).
	status, err := os.ReadFile("/proc/" + pid + "/status")
	require.NoError(t, err)
	{
		uid, ok := statusEffectiveUID(status)
		require.True(t, ok, "/proc/%s/status Uid: uid=%d ok=%v, want %d", pid, uid, ok, self)
		require.Equal(t, self, uid, "/proc/%s/status Uid: uid=%d ok=%v, want %d", pid, uid, ok, self)
	}
	git := readProcView(pid, procReader{owner: linuxProcOwner, readlink: os.Readlink, readFile: func(name string) ([]byte, error) {
		if strings.HasSuffix(name, "/comm") {
			return []byte("git\n"), nil
		}
		return os.ReadFile(name)
	}})
	require.True(t, git.accountKnown, "our own entry read as a git: %+v, want account %d from the real status and the cwd read", git, self)
	require.Equal(t, self, git.account, "our own entry read as a git: %+v, want account %d from the real status and the cwd read", git, self)
	require.NoError(t, git.accountErr, "our own entry read as a git: %+v, want account %d from the real status and the cwd read", git, self)
	require.NotEqual(t, "", git.cwd, "our own entry read as a git: %+v, want account %d from the real status and the cwd read", git, self)
}
