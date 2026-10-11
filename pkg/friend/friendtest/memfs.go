// Package friendtest holds the test doubles of pkg/friend that tests in
// other packages share.
package friendtest

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"sync"

	"github.com/mas-bandwidth/nova-tools/pkg/friend"
)

// MemFS is the in-memory twin of friend.OSFS, a friend.SettingsFS: directories, files and symlinks by
// clean absolute path. A write wants its parent a directory; MkdirAll
// refuses a component that is a file or a symlink.
type MemFS struct {
	mu    sync.Mutex
	nodes map[string]memNode
}

type memNode struct {
	kind   string
	data   []byte
	target string
}

// NewMemFS is an empty twin holding only the root.
func NewMemFS() *MemFS { return &MemFS{nodes: map[string]memNode{"/": {kind: friend.KindDir}}} }

// Symlink makes p a symlink to target, its parent created.
func (m *MemFS) Symlink(target, p string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	// ignored: a test fixture; mkdirAll fails only on a non-directory parent the test itself planted
	_ = m.mkdirAll(path.Dir(path.Clean(p)))
	m.nodes[path.Clean(p)] = memNode{kind: friend.KindSymlink, target: target}
}

func (m *MemFS) Lstat(p string) (friend.Entry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.nodes[path.Clean(p)]
	if !ok {
		return friend.Entry{Kind: friend.KindMissing}, nil
	}
	return friend.Entry{Kind: n.kind, Target: n.target}, nil
}

func (m *MemFS) ReadFile(p string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.nodes[path.Clean(p)]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: p, Err: fs.ErrNotExist}
	}
	if n.kind != friend.KindFile {
		return nil, &fs.PathError{Op: "read", Path: p, Err: fmt.Errorf("is a %s", n.kind)}
	}
	return slices.Clone(n.data), nil
}

func (m *MemFS) WriteFile(p string, data []byte, _ fs.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p = path.Clean(p)
	if parent := m.nodes[path.Dir(p)]; parent.kind != friend.KindDir {
		return &fs.PathError{Op: "write", Path: p, Err: fs.ErrNotExist}
	}
	if n, ok := m.nodes[p]; ok && n.kind == friend.KindDir {
		return &fs.PathError{Op: "write", Path: p, Err: errors.New("is a directory")}
	}
	m.nodes[p] = memNode{kind: friend.KindFile, data: slices.Clone(data)}
	return nil
}

func (m *MemFS) MkdirAll(p string, _ fs.FileMode) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mkdirAll(path.Clean(p))
}

func (m *MemFS) mkdirAll(p string) error {
	if n, ok := m.nodes[p]; ok {
		if n.kind != friend.KindDir {
			return &fs.PathError{Op: "mkdir", Path: p, Err: fmt.Errorf("is a %s", n.kind)}
		}
		return nil
	}
	if err := m.mkdirAll(path.Dir(p)); err != nil {
		return err
	}
	m.nodes[p] = memNode{kind: friend.KindDir}
	return nil
}
