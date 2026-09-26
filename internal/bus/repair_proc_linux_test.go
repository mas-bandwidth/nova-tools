//go:build linux

package bus

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

// The real /proc: this process's own entry is owned by its effective uid, and
// readProcView records that owner beside the comm it reads.
func TestLinuxProcSelfOwnerIsEffectiveUID(t *testing.T) {
	t.Parallel()
	pid := strconv.Itoa(os.Getpid())
	self := uint32(os.Geteuid())
	uid, ok, err := linuxProcOwner(pid)
	if err != nil || !ok || uid != self {
		t.Fatalf("/proc/%s owner: uid=%d ok=%v err=%v, want %d", pid, uid, ok, err, self)
	}
	v := readProcView(pid, linuxProcReader)
	if !v.ownerKnown || v.owner != self || strings.TrimSpace(v.comm) == "" {
		t.Fatalf("our own entry: %+v, want owner %d and comm read", v, self)
	}
	// The real status of this process: its Uid line states the effective uid, which is the
	// account the scan compares (readProcView reads it only for a git, so here directly).
	status, err := os.ReadFile("/proc/" + pid + "/status")
	if err != nil {
		t.Fatal(err)
	}
	if uid, ok := statusEffectiveUID(status); !ok || uid != self {
		t.Fatalf("/proc/%s/status Uid: uid=%d ok=%v, want %d", pid, uid, ok, self)
	}
	git := readProcView(pid, procReader{owner: linuxProcOwner, readlink: os.Readlink, readFile: func(name string) ([]byte, error) {
		if strings.HasSuffix(name, "/comm") {
			return []byte("git\n"), nil
		}
		return os.ReadFile(name)
	}})
	if !git.accountKnown || git.account != self || git.accountErr != nil || git.cwd == "" {
		t.Fatalf("our own entry read as a git: %+v, want account %d from the real status and the cwd read", git, self)
	}
}
