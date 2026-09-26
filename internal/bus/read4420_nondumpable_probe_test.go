package bus

// Cold read of #4420 at 7deb7afb2 (rowan-opus, the clause Stella left open):
// a non-dumpable git of THIS account on linux has /proc/<pid> owned by root,
// so its cwd readlink answers EACCES. With no absolute -C on its command line
// the scan must answer unknown (the lock stays), never skip it as foreign.

import (
	"io/fs"
	"os"
	"syscall"
	"testing"
)

func TestRead4420NonDumpableSameAccountGitIsUnknown(t *testing.T) {
	t.Parallel() // added on copy: every test in this tree opens with it (internal/ci)
	self := uint32(501)
	v := procView{comm: "git\n", cmdline: []byte("git\x00commit\x00-m\x00x\x00"),
		cwdErr: &os.PathError{Op: "readlink", Path: "/proc/42/cwd", Err: fs.ErrPermission},
		owner:  0, ownerKnown: true} // root-owned /proc/42: the non-dumpable shape
	_ = syscall.EACCES
	procs, unknown := classifyViews([]procView{v}, self)
	if unknown == nil {
		t.Fatalf("a root-owned /proc entry of an unplaced git was skipped as foreign (procs=%v): a non-dumpable git of this account is out of sight and its stale lock can be unlinked", procs)
	}
}
