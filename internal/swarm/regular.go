package swarm

import (
	"fmt"
	"io"
	"io/fs"
	"os"
)

// A JOB DIRECTORY IS THE WORKER'S, AND THE DISPATCHER IS OUTSIDE THE WALL
// (security#30, findings 2 and 4).
//
// sandbox.go makes <job> the worker's first `--write` and its `--cwd`, so RESULT.md,
// harness.log, exit.json and note are names the worker chooses the kind of. Two kinds are
// not records:
//
//   - a SYMLINK pointing out of the wall. The dispatcher runs unrestricted, so following
//     one copies a file the wall exists to keep from the worker into the retained report
//     and into the `log=` tail of a line a person reads. The wall is not a wall if the
//     thing outside it will fetch.
//
//   - a FIFO. `os.ReadFile` of one blocks in open(2) until somebody writes, and nothing
//     ever will; the dispatcher is single-threaded, so one mkfifo parks the whole pass,
//     and the NEXT run's recovery blocks at the same path. Nothing watches the watcher.
//
// So a path that is not a REGULAR FILE is read as no record at all -- the class this
// package already has for a job that published nothing (ClassNoResult, EndUnknown) and the
// lines it already prints. Nothing new is printed and no caller learns a new outcome.
//
// The posture and its wording are nova-check's, which already refuses a non-regular file by
// name (kernel.go's measureKernel, floors.go's readRecord). The open carries O_NOFOLLOW so
// a link planted between the Lstat and the open is refused too, and O_NONBLOCK so a FIFO
// that slipped through both cannot park the open itself; the fstat then asks the open file
// the same question. On a platform with neither flag the Lstat and the fstat stand alone.
var errNotRegular = fs.ErrInvalid

// MaxRegularRecord is the maximum size of a worker-written record read whole into memory
// by the supervisor. Normal records (RESULT.md, harness.log, sidecars, task files, retry state)
// range from tens of bytes to a few MiB for verbose test runs; 16 MiB provides generous
// margin while bounding supervisor memory consumption against unbounded reads or rogue
// worker files (security#30, finding 4 residue, issue #234).
const MaxRegularRecord = 16 * 1024 * 1024

var errRecordTooLarge = fs.ErrInvalid

func notRegular(path string, mode os.FileMode) error {
	return &fs.PathError{Op: "read", Path: path, Err: fmt.Errorf("not a regular file (%s); a job's records are regular files and this tool follows no link and opens no pipe: %w", kindOf(mode), errNotRegular)}
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

func recordTooLarge(path string, size, limit int64) error {
	if size > 0 {
		return &fs.PathError{
			Op:   "read",
			Path: path,
			Err:  fmt.Errorf("record size %d passes ceiling %d; a job's records have a bounded size: %w", size, limit, errRecordTooLarge),
		}
	}
	return &fs.PathError{
		Op:   "read",
		Path: path,
		Err:  fmt.Errorf("record size passes ceiling %d; a job's records have a bounded size: %w", limit, errRecordTooLarge),
	}
}

// statRegular refuses a path that is not a regular file. A path that is NOT THERE passes
// its own error through unchanged, so `missing` keeps the meaning it has everywhere.
func statRegular(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return notRegular(path, fi.Mode())
	}
	return nil
}

// readBounded reads from r up to maxBytes. It refuses if the stream passes
// maxBytes without allocating past maxBytes + 1, and returns no partial result.
// maxBytes must be positive; all worker record reads require a bound.
func readBounded(r io.Reader, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, fmt.Errorf("readBounded: maxBytes must be positive, got %d", maxBytes)
	}
	lr := io.LimitReader(r, maxBytes+1)
	raw, err := io.ReadAll(lr)
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > maxBytes {
		return nil, fmt.Errorf("record size passes ceiling %d; a job's records have a bounded size: %w", maxBytes, errRecordTooLarge)
	}
	return raw, nil
}

// readRegular is os.ReadFile for a worker-writable path: the whole file when it is a
// regular file within MaxRegularRecord, and a refusal when it is anything else or oversized.
func readRegular(path string) ([]byte, error) {
	return readRegularBounded(path, MaxRegularRecord)
}

// readRegularBounded reads a worker-writable regular file up to maxBytes.
// If the file is not a regular file, or if its size passes maxBytes (either at stat time
// or during read), it is refused without unbounded allocation.
func readRegularBounded(path string, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 {
		return nil, fmt.Errorf("readRegularBounded: maxBytes must be positive, got %d", maxBytes)
	}
	if err := statRegular(path); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDONLY|oNoFollow|oNonBlock, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, notRegular(path, fi.Mode())
	}
	if fi.Size() > maxBytes {
		return nil, recordTooLarge(path, fi.Size(), maxBytes)
	}
	raw, err := readBounded(f, maxBytes)
	if err != nil {
		return nil, &fs.PathError{
			Op:   "read",
			Path: path,
			Err:  err,
		}
	}
	return raw, nil
}

// openRegularRead opens a worker-writable path for STREAMING, on readRegular's terms: the
// file is read a line at a time by the caller and never held whole, and a path that is not
// a regular file is refused before a byte of it is asked for.
func openRegularRead(path string) (*os.File, error) {
	if err := statRegular(path); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_RDONLY|oNoFollow|oNonBlock, 0)
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
		return nil, notRegular(path, fi.Mode())
	}
	return f, nil
}

// openRegularWrite is os.OpenFile for a record this tool writes into a worker-writable
// directory, refused on the same terms.
func openRegularWrite(path string, flag int, perm os.FileMode) (*os.File, error) {
	fi, err := os.Lstat(path)
	switch {
	case err == nil && !fi.Mode().IsRegular():
		return nil, notRegular(path, fi.Mode())
	case err != nil && !missing(err):
		return nil, err
	}
	f, err := os.OpenFile(path, flag|oNoFollow, perm)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !st.Mode().IsRegular() {
		f.Close()
		return nil, notRegular(path, st.Mode())
	}
	return f, nil
}
