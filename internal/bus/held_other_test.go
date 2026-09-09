//go:build !windows

package bus

import (
	"os"
	"testing"
)

// openHeld opens a file for reading in a way that does not stand in the way of the write
// that REPLACES it. On unix every open is that way: a rename over an open file leaves the
// descriptor reading the bytes it was opened on, which is the whole property the lane
// state files are written by rename to have.
func openHeld(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
