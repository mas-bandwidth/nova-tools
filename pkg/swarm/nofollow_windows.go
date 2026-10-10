//go:build windows

package swarm

// oNoFollow and oNonBlock are zero on Windows: the platform has neither flag, and the
// Lstat before the open and the fstat after it carry the rule there.
const (
	oNoFollow = 0
	oNonBlock = 0
)

// ONoFollow is oNoFollow for callers outside this package; zero here, as above.
const ONoFollow = oNoFollow
