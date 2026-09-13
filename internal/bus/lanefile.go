package bus

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

// A LANE'S STATE FILES ARE REGULAR FILES, AND THIS TOOL NEVER WRITES THROUGH A LINK
// (security#30, finding 1).
//
// insideRoot answers a different question and answers it well: it refuses a path that
// LEXICALLY leaves the bus. It resolves nothing, because it is asked of a path this tool
// built out of a validated lane and a validated slug, and at that moment there is nothing
// to resolve. What it cannot see is a path that is inside the bus and points out of it:
// a commit that replaces a lane's INDEX, RECEIPTS or .gitattributes with a symlink makes
// the next append or overwrite land wherever the link says, and the link survives to catch
// the next run too. Everything a bus carries arrives by `pull`, from people the roster
// names and no further, which is why this is a discipline and not an alarm.
//
// The posture is nova-check's, which the repo already has in two places (kernel.go's
// measureKernel, floors.go's readRecord): Lstat, and a path that is not a regular file is
// refused by name rather than followed. The open carries O_NOFOLLOW as well, so the answer
// does not depend on nothing having changed between the two calls, and fstat asks the open
// file the same question a third time. On a platform with no O_NOFOLLOW the Lstat and the
// fstat still stand.
//
// Save (ops.go) and replaceLaneFile's rename were already immune and are left alone; this
// is the same rule they keep, written down once for the writes that did not.
func refuseLaneLink(full string) error {
	fi, err := os.Lstat(full)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if !fi.Mode().IsRegular() {
		return notRegular(full, fi.Mode())
	}
	return nil
}

func notRegular(full string, mode os.FileMode) error {
	return fmt.Errorf("%s is not a regular file (%s); a lane's state files are regular files and this tool does not write through a link", full, mode.Type())
}

// openLaneFile is os.OpenFile for a lane state file: refused when the path is not a regular
// file, and never following a link if one is planted between the check and the open.
func openLaneFile(full string, flag int, perm os.FileMode) (*os.File, error) {
	if err := refuseLaneLink(full); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(full, flag|oNoFollow, perm)
	if err != nil {
		return nil, err
	}
	fi, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		f.Close()
		return nil, notRegular(full, fi.Mode())
	}
	return f, nil
}

// writeLaneFile is os.WriteFile for a lane state file, on the same terms.
func writeLaneFile(full string, content []byte, perm os.FileMode) error {
	f, err := openLaneFile(full, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(content); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// laneTempPath is the name replaceLaneFile writes through before it renames.
//
// It was `<file>.tmp`, fixed, so that a run killed between the write and the rename left a
// single predictable name the lane walk steps over and a person can delete knowing what it
// was. That is still true of the shape -- the name still ends in TempSuffix, and
// isLaneStateTemp still recognises it -- but the middle is now twelve hex characters from
// the OS random source, because a FIXED temp name is a path a hostile commit can plant a
// symlink at, and the old write followed it. A name nobody can predict cannot be lain in
// wait for, and the O_EXCL on the open refuses even a lucky one rather than truncating it.
func laneTempPath(full string) (string, error) {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("the OS random source would not supply a temporary name for %s: %w", filepath.Base(full), err)
	}
	return full + "." + hex.EncodeToString(b[:]) + TempSuffix, nil
}
