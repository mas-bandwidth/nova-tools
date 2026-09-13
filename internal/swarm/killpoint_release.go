//go:build !swarmtest

package swarm

// THE RELEASE BUILD. The kill and pause injection points are test-only machinery: the
// NOVA_SWARM_* environment variables let a test fix the order of two deaths it would
// otherwise have to hope for, and no product path ever sets them. They are read only by the
// swarmtest build (killpoint_unix.go and killpoint_other.go); in the release build every
// injection function is a no-op that never reads the environment, so a caller who sets any
// of the five variables gets a normal run, whatever the value. The product logic — the
// supervisor, the recovery, the attestation — is identical in both builds; only these two
// hooks differ.

// CheckKillPoint is a no-op in the release build: it never reads the environment and never
// kills. The kill injection point exists only in the swarmtest build.
func CheckKillPoint(point string) {}

// CheckPausePoint is a no-op in the release build: it never reads the environment and never
// stops. The pause injection point exists only in the swarmtest build.
func CheckPausePoint(point string) {}
