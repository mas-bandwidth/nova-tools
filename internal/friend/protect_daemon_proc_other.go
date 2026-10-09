//go:build !linux

package friend

// ProtectDaemonProcEnvironment is Linux-only. Other platforms keep their
// existing native sandbox policy and do not expose Linux procfs.
func ProtectDaemonProcEnvironment() error { return nil }
