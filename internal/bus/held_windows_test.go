package bus

import (
	"os"
	"testing"
)

// openHeld opens a file for reading and reports whether a rename can REPLACE it while
// this descriptor is open. On Windows it cannot, so it opens nothing and says so.
//
// os.Rename there is MoveFileEx with MOVEFILE_REPLACE_EXISTING, whose replace deletes the
// destination through FileRenameInformation, and that fails with ERROR_ACCESS_DENIED while
// ANY handle is open on the destination -- FILE_SHARE_DELETE does not change it; only the
// POSIX-semantics rename, which Go does not use, would. So a test that held a descriptor
// across the write would not be testing this tool's write at all: its own handle would be
// what made the rename fail, and it did, with "Access is denied" on a CI runner.
//
// The half of the assertion that IS expressible here is the one that matters as much: the
// path names a DIFFERENT FILE after the write than before it, which is the whole of
// "replaced rather than rewritten in place" and is what the caller checks on every
// platform. The difference is declared rather than silently untested.
func openHeld(t *testing.T, path string) (*os.File, bool) {
	t.Helper()
	return nil, false
}
