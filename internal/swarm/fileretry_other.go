//go:build !windows

package swarm

// transientIO: on a platform whose rename is atomic against every reader there is no
// collision to wait out, so every retry loop in fileretry.go runs exactly once. An error
// here is the answer it looks like.
func transientIO(error) bool { return false }
