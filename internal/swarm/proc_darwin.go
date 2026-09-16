//go:build darwin

package swarm

// The kernel's start stamp, on a system whose standard library will not hand it over.
//
// StartStamp is what tells a live pid from a REUSED one: a slot file whose pid is alive
// under a different stamp is a different process wearing an old number, and rule 17
// quarantines it rather than adopting it. On Linux it is field 22 of /proc/<pid>/stat. On
// macOS the value lives in struct kinfo_proc behind sysctl `kern.proc.pid`, and the
// standard library's syscall package exports Sysctl (a C string) and SysctlUint32 and
// nothing that returns the raw record -- the raw form is in golang.org/x/sys, which this
// repo does not import. Running `ps` for it is the one thing this tool must never do.
//
// So the stamp here is a DASH, an absence stated rather than a value invented, and the
// comparison that uses it compares dash with dash: on this platform a slot file's identity
// rests on the pid alone, which is the weaker claim, and it is weaker HERE rather than
// everywhere.
func StartStamp(pid int) string { return "-" }

// GroupMembers cannot enumerate a process group here for the same reason. The callers fall
// back to GroupAlive over the job's own process group, which the supervisor is not a
// member of, so "is anything left in it" is answerable without enumeration.
func GroupMembers(pgid, self int) (int, bool) { return 0, false }
