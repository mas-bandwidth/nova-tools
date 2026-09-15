//go:build !windows

package bus

import "errors"

// noReplaceRenameCall is the call the refusal names when the hard link is refused and this
// build has no no-replace rename to fall back to.
const noReplaceRenameCall = "the OS no-replace rename"

// errNoNoReplaceRename is what this build says about itself, which is what the refusal
// quotes. It is a sentence about THIS BUILD and never a borrowed errno: reporting
// os.ErrInvalid here would tell a reader that the call was made and answered "invalid
// argument", and no call was made.
var errNoNoReplaceRename = errors.New("this build has no OS no-replace rename: renameat2 with RENAME_NOREPLACE and renamex_np with RENAME_EXCL are not reachable from Go's standard library, and this module has no dependencies")

// noReplaceRename is the second create-exclusive publish, and this build does not have one.
//
// WHAT IS MISSING AND WHY. The kernels have it -- `renameat2` with `RENAME_NOREPLACE` on
// Linux, `renamex_np` with `RENAME_EXCL` on macOS -- and neither is reachable from Go's
// standard library: one is a raw syscall whose number differs per architecture and the
// other is a libc entry point that needs cgo. This module has no dependencies and builds
// for three platforms without cgo, so rather than guess a syscall number the publish
// REFUSES here, naming this absence in its own words.
//
// It is only reachable on a filesystem that refuses hard links, which is neither APFS,
// ext4, XFS, btrfs nor NTFS.
func noReplaceRename(from, to string) error {
	return errNoNoReplaceRename
}
