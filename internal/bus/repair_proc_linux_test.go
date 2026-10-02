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
	require.False(t, err != nil || !ok || uid != self, "/proc/%s owner: uid=%d ok=%v err=%v, want %d", pid, uid, ok, err, self)
	v := readProcView(pid, linuxProcReader)
	require.False(t, !v.ownerKnown || v.owner != self || strings.TrimSpace(v.comm) == "", "our own entry: %+v, want owner %d and comm read", v, self)
	// The real status of this process: its Uid line states the effective uid, which is the
	// account the scan compares (readProcView reads it only for a git, so here directly).
	status, err := os.ReadFile("/proc/" + pid + "/status")
	require.NoError(t, err)
	{
		uid, ok := statusEffectiveUID(status)
		require.False(t, !ok || uid != self, "/proc/%s/status Uid: uid=%d ok=%v, want %d", pid, uid, ok, self)
	}
	git := readProcView(pid, procReader{owner: linuxProcOwner, readlink: os.Readlink, readFile: func(name string) ([]byte, error) {
		if strings.HasSuffix(name, "/comm") {
			return []byte("git\n"), nil
		}
		return os.ReadFile(name)
	}})
	require.False(t, !git.accountKnown || git.account != self || git.accountErr != nil || git.cwd == "", "our own entry read as a git: %+v, want account %d from the real status and the cwd read", git, self)
}
