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
}
