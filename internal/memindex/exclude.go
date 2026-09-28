package memindex

import (
	"io/fs"
	"path"
)

// Excluding presents a filesystem with .git and caller-excluded paths hidden.
// Globs, reads and link resolution all see the same tree. Excluding an ancestor
// hides its descendants, including when a caller names one directly.
func Excluding(fsys fs.FS, exclude func(string) bool) fs.FS {
	return excludingFS{FS: fsys, exclude: exclude}
}

// excludingFS presents the same excluded tree to glob-based verification that
// Build presents to retrieval. Ancestor checks also cover literal selectors:
// excluding a directory hides its contents even when no glob walk visits it.
type excludingFS struct {
	fs.FS
	exclude func(string) bool
}

func (v excludingFS) hidden(name string) bool {
	for p := name; p != "." && p != "/"; p = path.Dir(p) {
		if path.Base(p) == ".git" || (v.exclude != nil && v.exclude(p)) {
			return true
		}
	}
	return false
}

func (v excludingFS) Open(name string) (fs.File, error) {
	if v.hidden(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return v.FS.Open(name)
}

func (v excludingFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if v.hidden(name) {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrNotExist}
	}
	entries, err := fs.ReadDir(v.FS, name)
	if err != nil {
		return nil, err
	}
	visible := make([]fs.DirEntry, 0, len(entries))
	for _, entry := range entries {
		if !v.hidden(path.Join(name, entry.Name())) {
			visible = append(visible, entry)
		}
	}
	return visible, nil
}
