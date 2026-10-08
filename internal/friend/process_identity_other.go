//go:build !linux && !darwin

package friend

const ProcessIdentitySupported = false

// ProcessIdentity has no reliable native birth witness on this platform.
func ProcessIdentity(int) string { return "" }
func ProcessGroupAlive(int) bool { return false }
