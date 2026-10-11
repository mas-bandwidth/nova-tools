//go:build windows

package cardcontract

import "testing"

// Windows has no FIFO in the POSIX sense, so the FIFO test skips here by name rather than
// failing the package build, the way pkg/swarm's helper does.
func plantFIFO(t *testing.T, path string) {
	t.Helper()
	t.Skip("no FIFOs on Windows: the FIFO refusal is exercised on the unix legs")
}
