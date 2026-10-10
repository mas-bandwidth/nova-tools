package release

import (
	"fmt"
	"os"
	"path/filepath"
)

// installFile is atomicInstall with the rename as a seam, because the one
// filesystem that behaves differently here is the one no test on this fleet
// can reach.
//
// WINDOWS WILL NOT REPLACE A FILE THAT IS OPEN FOR EXECUTION. The rename is the
// whole install on unix, where replacing a running binary is ordinary and the
// running process keeps its inode; on windows the same call fails with a
// sharing violation, and the file it fails on is very often nova-update.exe
// replacing ITSELF -- `adopt` runs the release's own nova-update.exe on the
// bench, and that process is holding its own image open while it installs. A
// perfectly good release would report INSTALL FAILED on the one tool that matters
// most, on the one platform nobody here can reproduce it on.
//
// Windows DOES allow a running file to be renamed ASIDE: the open handle
// follows the file rather than the name. So the fallback is that platform's own
// self-replacement -- move the existing binary aside, then rename the new one
// into place -- and it is a FALLBACK, taken only after the ordinary rename has
// failed, so nothing about the unix path changes. The name it moves aside to is
// dot-prefixed, which is what keeps it out of `nova-version snapshot` and out of
// `--retire`, both of which take nova-* only: windows will not let a running image
// be deleted while it is still running, so that file may survive until the
// process ends and has to be inert while it does.
//
// A rename that fails for a REAL reason -- a full disk, a read-only directory --
// still fails: the existing file is put back, the temporary is removed, and the error
// the caller is given is the one the filesystem gave.
func installFile(src, dst string, rename func(oldpath, newpath string) error) error {
	body, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	tmp := filepath.Join(filepath.Dir(dst), "."+filepath.Base(dst)+".new")
	if err := writeNoFollow("install", tmp, body, 0o755); err != nil {
		return err
	}
	if err := os.Chmod(tmp, 0o755); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	renameErr := rename(tmp, dst)
	if renameErr == nil {
		return nil
	}
	aside := filepath.Join(filepath.Dir(dst), "."+filepath.Base(dst)+".old")
	_ = os.Remove(aside) // an earlier install's, if that one could not clear it
	if err := rename(dst, aside); err != nil {
		// Nothing was moved, so there is nothing to put back. The error
		// reported is the ORIGINAL one: "the file could not be replaced" is
		// what happened, and "it could not be moved aside either" is only how
		// the remedy failed.
		_ = os.Remove(tmp)
		return renameErr
	}
	if err := rename(tmp, dst); err != nil {
		// Put the replaced binary back under its own name. A bench left with no
		// nova-bus at all is worse than one left with the previous nova-bus.
		if back := rename(aside, dst); back != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("%w; and %s could not be put back from %s: %v (move it back by hand)", err, filepath.Base(dst), filepath.Base(aside), back)
		}
		_ = os.Remove(tmp)
		return err
	}
	// Best effort: on windows a still-running image cannot be removed, and that
	// is the expected case rather than a failure. It is inert where it is, and
	// the next install clears it.
	_ = os.Remove(aside)
	return nil
}
