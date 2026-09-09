//go:build !windows

package bus

import (
	"os"
	"testing"
)

// openHeld opens a file for reading and reports whether a rename can REPLACE it while
// this descriptor is open. On unix every rename can: the descriptor goes on reading the
// bytes it was opened on, which is the property the lane state files are written by
// rename to have, and the direct test of it.
func openHeld(t *testing.T, path string) (*os.File, bool) {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return f, true
}
