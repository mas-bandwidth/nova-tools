//go:build windows

package swarm

import "testing"

// Windows has no FIFO in the POSIX sense, so the three FIFO tests skip here by name
// rather than failing the package build (certification 34771523657 at 9a95e33e).
func plantFIFO(t *testing.T, path string) {
	t.Helper()
	t.Skip("no FIFOs on Windows: the FIFO refusal is exercised on the unix legs")
}
