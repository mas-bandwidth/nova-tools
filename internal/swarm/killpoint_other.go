//go:build !unix && swarmtest

package swarm

// CheckPausePoint is a no-op on non-unix systems.
func CheckPausePoint(point string) {}
