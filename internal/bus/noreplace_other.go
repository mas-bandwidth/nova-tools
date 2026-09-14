//go:build !windows

package bus

import "os"

// noReplaceRenameCall is the call the refusal names when the hard link is refused and this
// build has no no-replace rename to fall back to.
const noReplaceRenameCall = "link"

// noReplaceRename is the second create-exclusive publish, and this build does not have one.
//
// WHAT IS MISSING AND WHY. The kernels have it -- `renameat2` with `RENAME_NOREPLACE` on
// Linux, `renamex_np` with `RENAME_EXCL` on macOS -- and neither is reachable from Go's
// standard library: one is a raw syscall whose number differs per architecture and the
// other is a libc entry point that needs cgo. This module has no dependencies and builds
// for three platforms without cgo, so rather than guess a syscall number the publish
// REFUSES here, which is the third branch the contract already specifies: exit 2, naming
// the directory and the call it tried, with one remedy line.
//
// It is only reachable on a filesystem that refuses hard links, which is neither APFS,
// ext4, XFS, btrfs nor NTFS.
func noReplaceRename(from, to string) error {
	return os.ErrInvalid
}
