// Package readregular provides bounds-checked and type-checked file reads
// for regular files, rejecting non-regular files (such as FIFOs, devices, and
// sockets) and files that exceed an explicit size cap.
//
// Governed by security#77 finding 3 (re-file of security#56 finding 3).
package readregular

import (
	"fmt"
	"io"
	"os"
)

// DefaultMax is the default file size cap: 16 MiB.
const DefaultMax int64 = 16 << 20

// Read reads path if and only if it is a regular file whose size does not
// exceed max. It follows symlinks when stating and opening the path, verifies
// that the target is a regular file, checks that its stated size is within max,
// opens the file, confirms os.SameFile between the handle and the initial stat,
// and reads through io.LimitReader(file, max+1).
//
// If the target is not regular, it returns an error naming the path and mode.
// If the file exceeds max (either at stat or during read), it returns an error
// naming the cap.
func Read(path string, max int64) ([]byte, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: not a regular file (mode %s)", path, fi.Mode())
	}
	if max > 0 && fi.Size() > max {
		return nil, fmt.Errorf("%s: file size %d exceeds limit %d", path, fi.Size(), max)
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }() // ignored: the file is opened only to be read

	fi2, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !os.SameFile(fi, fi2) {
		return nil, fmt.Errorf("%s: file changed while opening", path)
	}
	if !fi2.Mode().IsRegular() {
		return nil, fmt.Errorf("%s: not a regular file (mode %s)", path, fi2.Mode())
	}

	var r io.Reader = f
	if max > 0 {
		r = io.LimitReader(f, max+1)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if max > 0 && int64(len(data)) > max {
		return nil, fmt.Errorf("%s: file size exceeds limit %d", path, max)
	}
	return data, nil
}
