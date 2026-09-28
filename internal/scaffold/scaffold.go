// Package scaffold provides the shared write-confinement and template engine
// for scaffolding verbs across this repository (nova-tools#2498 S5).
//
// Every output goes through os.OpenRoot(root), so no path resolves outside
// --root; a preflight walks each output path with Lstat and refuses a symlink
// anywhere on it, a file where a directory goes, and an existing file. Only
// fs.ErrNotExist counts as free; any other lookup error is reported and nothing
// is written. Each file is created O_CREATE|O_EXCL, so a file or dangling
// symlink raced in after the preflight is refused. A failure part-way cleans up
// whatever this run created.
package scaffold

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// Planned is one rendered file and where it lands, slash-separated and
// relative to the checkout root.
type Planned struct {
	Rel  string
	Data []byte
}

// BeforeCreate runs between the preflight and each file's creation. It is nil
// outside tests, which use it to race a file in where one is about to land.
var BeforeCreate func(rel string)

// Write lays the planned files into root, confined strictly to it.
func Write(root string, outs []Planned) (written []string, err error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = r.Close() }()

	for _, o := range outs {
		if err := Preflight(r, o.Rel); err != nil {
			return nil, err
		}
	}

	var made []string // directories created this run, outermost first
	defer func() {
		if err == nil {
			return
		}
		for _, w := range slices.Backward(written) {
			_ = r.Remove(filepath.FromSlash(w))
		}
		for _, d := range slices.Backward(made) {
			_ = r.Remove(filepath.FromSlash(d)) // fails harmlessly if non-empty
		}
		written = nil
	}()

	for _, o := range outs {
		if err := Mkdirs(r, path.Dir(o.Rel), &made); err != nil {
			return written, err
		}
		if BeforeCreate != nil {
			BeforeCreate(o.Rel)
		}
		f, err := r.OpenFile(filepath.FromSlash(o.Rel), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if errors.Is(err, fs.ErrExist) {
			return written, fmt.Errorf("%s appeared while scaffold was writing — it never overwrites a file", o.Rel)
		}
		if err != nil {
			return written, fmt.Errorf("%s: %w", o.Rel, err)
		}
		written = append(written, o.Rel)
		_, werr := f.Write(o.Data)
		if cerr := f.Close(); werr == nil {
			werr = cerr
		}
		if werr != nil {
			return written, fmt.Errorf("%s: %w", o.Rel, werr)
		}
	}
	return written, nil
}

// Preflight walks rel one element at a time with Lstat so no symlink is
// followed: a symlink anywhere on the path is refused, as is a file where a
// directory goes and a final file that already exists. The first element that
// does not exist ends the walk.
func Preflight(r *os.Root, rel string) error {
	elems := strings.Split(rel, "/")
	for i := range elems {
		p := strings.Join(elems[:i+1], "/")
		info, err := r.Lstat(filepath.FromSlash(p))
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("%s: cannot tell whether it exists: %w", rel, err)
		}
		switch {
		case info.Mode()&fs.ModeSymlink != 0:
			return fmt.Errorf("%s is a symlink — scaffold writes %s only as a real file under --root and never through a link", p, rel)
		case i == len(elems)-1:
			return fmt.Errorf("%s already exists — scaffold lays down a new skeleton and never overwrites a file", rel)
		case !info.IsDir():
			return fmt.Errorf("%s is not a directory, and %s goes under it", p, rel)
		}
	}
	return nil
}

// Mkdirs creates dir and its missing parents inside r, one element at a time,
// recording each created directory. An element already present must be a real
// directory.
func Mkdirs(r *os.Root, dir string, made *[]string) error {
	if dir == "." || dir == "" {
		return nil
	}
	elems := strings.Split(dir, "/")
	for i := range elems {
		p := strings.Join(elems[:i+1], "/")
		err := r.Mkdir(filepath.FromSlash(p), 0o755)
		if err == nil {
			*made = append(*made, p)
			continue
		}
		if !errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%s: %w", p, err)
		}
		info, err := r.Lstat(filepath.FromSlash(p))
		if err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
		if !info.IsDir() || info.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s is not a real directory — scaffold never writes through a link", p)
		}
	}
	return nil
}
