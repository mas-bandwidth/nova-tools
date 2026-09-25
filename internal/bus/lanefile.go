package bus

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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
//
// EVERY COMPONENT FROM THE BUS ROOT DOWN, and not the last one only (Fable's cold read of
// #226). A lane is a DIRECTORY in the bus, and a commit can make `from-x` itself a symlink
// as easily as it can make `from-x/INDEX` one; a check of the final component only let the
// whole lane -- INDEX, RECEIPTS, CURSOR, OPEN and every note -- be written outside the bus
// with nothing raised. The walk starts AT THE ROOT and not above it, because the bus's own
// checkout may perfectly well sit under a symlinked directory (`/var` on a Mac is one), and
// where that link is is the person's business and not this tool's.
//
// A component that is not there yet ends the walk: nothing below an absent directory can be
// a link, and the write that follows creates it.
func refuseLaneLink(root, full string) error {
	rel, err := filepath.Rel(root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		return fmt.Errorf("%s is not inside the bus at %s", full, root)
	}
	at := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		if part == "." || part == "" {
			continue
		}
		at = filepath.Join(at, part)
		fi, err := os.Lstat(at)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		switch {
		case fi.Mode()&os.ModeSymlink != 0:
			return notRegular(at, fi.Mode())
		case at == full && !fi.Mode().IsRegular():
			return notRegular(at, fi.Mode())
		case at != full && !fi.IsDir():
			return notRegular(at, fi.Mode())
		}
	}
	return nil
}

func notRegular(full string, mode os.FileMode) error {
	return fmt.Errorf("%s is not a regular file (%s); a lane's state files are regular files and this tool follows no link and opens no pipe", full, kindOf(mode))
}

// kindOf names the kind a non-regular path is, in the one word the refusal line carries.
func kindOf(mode os.FileMode) string {
	switch {
	case mode&os.ModeSymlink != 0:
		return "symlink"
	case mode&os.ModeNamedPipe != 0:
		return "fifo"
	case mode&os.ModeDir != 0:
		return "directory"
	default:
		return mode.Type().String()
	}
}

// readLaneFile is io.ReadFile for a lane state file, on openLaneFile's terms: a path that
// is not a regular file is refused by name -- never followed and never blocked on -- and
// the open carries O_NOFOLLOW for what slipped past the check.
func readLaneFile(root, full string) ([]byte, error) {
	f, err := openLaneFile(root, full, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// openLaneFile is os.OpenFile for a lane state file: refused when the path is not a regular
// file, and never following a link if one is planted between the check and the open.
func openLaneFile(root, full string, flag int, perm os.FileMode) (*os.File, error) {
	if err := refuseLaneLink(root, full); err != nil {
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
func writeLaneFile(root, full string, content []byte, perm os.FileMode) error {
	f, err := openLaneFile(root, full, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
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
