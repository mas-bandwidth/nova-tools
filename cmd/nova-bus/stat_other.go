//go:build !unix

package main

import "os"

const oNoFollow = 0

// statOf has no owner or device number off unix: a check that needs them refuses.
func statOf(os.FileInfo) (uid uint32, rdev uint64, ok bool) { return 0, 0, false }
