package wake

import "os"

// kindOfMode names the thing found at the lock path in the words a person
// reading the line would use. It has no build tag because BOTH probes refuse a
// path that is not a regular file and the refusal a person reads must be the
// same sentence on every platform.
func kindOfMode(mode os.FileMode) string {
	switch {
	case mode.IsDir():
		return "directory"
	case mode&os.ModeNamedPipe != 0:
		return "fifo"
	case mode&os.ModeSocket != 0:
		return "socket"
	case mode&os.ModeDevice != 0:
		return "device"
	case mode&os.ModeSymlink != 0:
		return "symlink"
	}
	return "something that is not a regular file"
}
