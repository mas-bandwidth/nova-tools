//go:build !unix

package swarm

// ignoreHangup is a no-op where there is no SIGHUP and no orphaned process group: windows
// has neither, and a supervisor there dies only when somebody ends it. See hangup_unix.go
// for what this defends on the platforms that have both.
func ignoreHangup() {}
