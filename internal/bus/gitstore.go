package bus

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// gitStore is the Store over a git checkout: the bus's bytes live in a repository, and
// every verb is today's git code, unchanged. The design is
// LOGIC-TRANSPORT-SEPARATION-2026-10-02.md section 4a.
type gitStore struct {
	dir      string
	remote   string
	branch   string
	attempts int
	wait     time.Duration
	now      func() time.Time
}

// NewGitStore is a Store over the checkout at dir, pushing to remote/branch with the given
// retry budget. wait is how long a second run on one checkout waits for the first.
func NewGitStore(dir, remote, branch string, attempts int, wait time.Duration) Store {
	return &gitStore{
		dir:      dir,
		remote:   remote,
		branch:   branch,
		attempts: attempts,
		wait:     wait,
		now:      time.Now,
	}
}

// Roster is LoadConfig: the roster is a file at the bus root, read and validated once.
func (g *gitStore) Roster(ctx context.Context) (*Config, error) {
	return LoadConfig(g.dir)
}

// Refresh is LockCheckout then FetchAndFastForward, the same two calls a reader's first
// step makes. See tla/BusCursor.tla (the advance action reads after it moves).
func (g *gitStore) Refresh(ctx context.Context) (Position, bool, error) {
	release, err := LockCheckout(g.dir, g.wait)
	if err != nil {
		return "", false, err
	}
	defer release()
	moved, err := FetchAndFastForward(g.dir, g.remote, g.branch)
	if err != nil {
		return "", false, err
	}
	head, err := HeadCommit(g.dir)
	if err != nil {
		return "", false, err
	}
	return Position(head), moved, nil
}

// Head is HeadCommit.
func (g *gitStore) Head(ctx context.Context) (Position, error) {
	head, err := HeadCommit(g.dir)
	if err != nil {
		return "", err
	}
	return Position(head), nil
}

// Since is IsAncestor, then CommitsSinceBounded for the cap, then ChangedSince for the
// paths. from is the reader's cursor; to is the head, and a to that is not the head is
// refused because ChangedSince answers against HEAD. See tla/BusCursor.tla: a cursor that
// is not an ancestor of the head is a refusal, never a best effort.
func (g *gitStore) Since(ctx context.Context, from, to Position, limit int) ([]string, bool, error) {
	head, err := HeadCommit(g.dir)
	if err != nil {
		return nil, false, err
	}
	toS := string(to)
	if toS == "" {
		toS = head
	}
	if toS != head {
		return nil, false, ErrUnknownPosition
	}
	fromS := string(from)
	if fromS != "" && fromS != toS {
		ok, err := isAncestorOf(g.dir, fromS, toS)
		if err != nil {
			return nil, false, err
		}
		if !ok {
			return nil, false, ErrUnknownPosition
		}
	}
	if fromS == "" {
		paths, err := lanePathsAt(g.dir, head)
		if err != nil {
			return nil, false, err
		}
		return paths, false, nil
	}
	capped := false
	if limit > 0 {
		if _, capped, err = CommitsSinceBounded(g.dir, fromS, limit); err != nil {
			return nil, false, err
		}
	} else if _, err = CommitsBetween(g.dir, fromS, toS); err != nil {
		return nil, false, err
	}
	paths, err := ChangedSince(g.dir, fromS)
	if err != nil {
		return nil, false, err
	}
	return paths, capped, nil
}

// lanePathsAt is every lane file present at one commit, for Since from the beginning of
// history, where there is no earlier commit to diff against.
func lanePathsAt(dir, commit string) ([]string, error) {
	out, err := git(dir, "ls-tree", "-r", "--name-only", "-z", commit)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, p := range strings.Split(out, "\x00") {
		if p == "" || !strings.HasPrefix(p, "from-") || !strings.Contains(p, "/") {
			continue
		}
		paths = append(paths, p)
	}
	return paths, nil
}

// Read is the tree at one position. The head is the checkout's own files through an
// os.Root; an earlier position is the git objects at that commit. See tla/BusCursor.tla:
// a reader reads the bus as it was at its cursor, not as it is now.
func (g *gitStore) Read(ctx context.Context, at Position) (fs.FS, error) {
	head, err := HeadCommit(g.dir)
	if err != nil {
		return nil, err
	}
	pos := string(at)
	if pos == "" || pos == head {
		root, err := os.OpenRoot(g.dir)
		if err != nil {
			return nil, err
		}
		return root.FS(), nil
	}
	if err := ValidCommitHex(pos); err != nil {
		return nil, ErrUnknownPosition
	}
	ok, err := isAncestorOf(g.dir, pos, "HEAD")
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrUnknownPosition
	}
	return gitFS{dir: g.dir, commit: pos}, nil
}

// Publish is today's send path, unchanged: the lock, the checkout and remote guards, the
// union-merge attribute, the lane file writes, StagePaths, then CommitAndPush or CommitOnly
// with the transport's own conflict settlement. See tla/BusCursor.tla: one publish is one
// advance, and the notes are create-only.
func (g *gitStore) Publish(ctx context.Context, change Change) (Published, error) {
	if change.Author.Lane == "" {
		return Published{}, fmt.Errorf("%q has no lane on this bus, so has nowhere to publish", change.Author.Name)
	}
	// Everything is checked before anything is written: a refused publish changes nothing.
	for rel := range change.Notes {
		if !strings.HasPrefix(rel, change.Author.Lane+"/") {
			return Published{}, fmt.Errorf("the note %s is outside lane %s", rel, change.Author.Lane)
		}
		full := filepath.Join(g.dir, filepath.FromSlash(rel))
		if err := insideRoot(g.dir, full); err != nil {
			return Published{}, err
		}
		have, err := os.ReadFile(full)
		switch {
		case err == nil:
			if !bytes.Equal(have, change.Notes[rel]) {
				return Published{}, ErrConflict
			}
		case os.IsNotExist(err):
		default:
			return Published{}, err
		}
	}
	already, err := g.alreadyPublished(change)
	if err != nil {
		return Published{}, err
	}
	head, err := HeadCommit(g.dir)
	if err != nil {
		return Published{}, err
	}
	if already {
		return Published{At: Position(head), Already: true}, nil
	}

	release, err := LockCheckout(g.dir, g.wait)
	if err != nil {
		return Published{}, err
	}
	defer release()

	on, err := CurrentBranch(g.dir)
	if err != nil {
		return Published{}, err
	}
	if on != g.branch {
		return Published{}, fmt.Errorf("the bus's checkout is on branch %q, not %q", on, g.branch)
	}
	if err := EnsureClean(g.dir, nil); err != nil {
		return Published{}, err
	}
	if !change.Local {
		if err := EnsureLevelWith(g.dir, g.remote, g.branch); err != nil {
			return Published{}, err
		}
	}

	for rel, data := range change.Notes {
		if err := writeCreateOnly(g.dir, rel, data); err != nil {
			return Published{}, err
		}
	}
	for _, e := range change.Index {
		if e.Lane == "" {
			e.Lane = change.Author.Lane
		}
		if e.Lane != change.Author.Lane {
			return Published{}, fmt.Errorf("the index line for %s belongs to lane %s", e.ID, e.Lane)
		}
		if err := AppendIndexLine(g.dir, e); err != nil {
			return Published{}, err
		}
	}
	if len(change.Receipts) > 0 {
		if err := appendLines(g.dir, change.Author.Lane+"/"+ReceiptsName, change.Receipts); err != nil {
			return Published{}, err
		}
	}
	if change.Cursor != nil {
		if err := WriteCursor(g.dir, change.Author.Lane, change.Cursor.Commit, change.Cursor.Open, change.Cursor.Legacy, g.now()); err != nil {
			return Published{}, err
		}
	}
	if change.Open != nil {
		if err := WriteOpen(g.dir, change.Author.Lane, *change.Open); err != nil {
			return Published{}, err
		}
	}

	paths := make([]string, 0, len(change.Notes)+3)
	for rel := range change.Notes {
		paths = append(paths, rel)
	}
	if len(change.Index) > 0 {
		paths = append(paths, IndexPath(change.Author.Lane))
	}
	if len(change.Receipts) > 0 {
		paths = append(paths, change.Author.Lane+"/"+ReceiptsName)
	}
	if change.Cursor != nil {
		paths = append(paths, CursorPath(change.Author.Lane))
	}
	if change.Open != nil {
		paths = append(paths, OpenPath(change.Author.Lane))
	}
	wroteAttrs, err := EnsureMergeAttributes(g.dir)
	if err != nil {
		return Published{}, err
	}
	if wroteAttrs {
		paths = append(paths, AttributesName)
	}
	paths, err = StagePaths(g.dir, paths)
	if err != nil {
		return Published{}, err
	}
	id := Identity{Name: change.Author.GitName, Email: change.Author.GitEmail}
	var res PushResult
	if change.Local {
		res, err = CommitOnly(g.dir, id, paths, change.Message)
	} else {
		res, err = CommitAndPush(g.dir, id, paths, change.Message, g.remote, g.branch, g.attempts)
	}
	if err != nil {
		return Published{}, err
	}
	return Published{At: Position(res.Commit), Attempts: res.Attempts}, nil
}

// alreadyPublished reports whether every note in the change is already on the checkout
// with the same bytes, which is a retry after a lost reply and not a second publish.
func (g *gitStore) alreadyPublished(change Change) (bool, error) {
	if len(change.Notes) == 0 {
		return false, nil
	}
	for rel, want := range change.Notes {
		have, err := os.ReadFile(filepath.Join(g.dir, filepath.FromSlash(rel)))
		if err != nil {
			return false, nil
		}
		if !bytes.Equal(have, want) {
			return false, nil
		}
	}
	return true, nil
}

// Find answers whether an id is on the bus, from the lane INDEX at the head, the same way
// queryRemote answers it for a prepared note: the index is the catalogue and the note is
// its file. It reports found=false and no error for an id the bus does not hold.
func (g *gitStore) Find(ctx context.Context, id string) ([]byte, IndexEntry, bool, error) {
	c, err := LoadConfig(g.dir)
	if err != nil {
		return nil, IndexEntry{}, false, err
	}
	idx, err := ReadIndex(g.dir, c)
	if err != nil {
		return nil, IndexEntry{}, false, err
	}
	e, ok := idx.ByID(id)
	if !ok {
		return nil, IndexEntry{}, false, nil
	}
	raw, err := os.ReadFile(filepath.Join(g.dir, filepath.FromSlash(e.Path)))
	if err != nil {
		return nil, IndexEntry{}, false, err
	}
	return raw, *e, true, nil
}

// writeCreateOnly writes one note and refuses to overwrite it: a note once written is not
// rewritten, which is the bus's own rule and the reason a publish is safe to retry. A note
// whose bytes are already there with the same content is a retry after a write that was cut
// short, not a second write, so it is left as it is and the rest of the change lands.
func writeCreateOnly(root, rel string, data []byte) error {
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := insideRoot(root, full); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			if have, rerr := os.ReadFile(full); rerr == nil && bytes.Equal(have, data) {
				return nil
			}
		}
		return err
	}
	defer func() { _ = f.Close() }() // ignored: the write error is returned, and the explicit close on success is the one reported
	if _, err := f.Write(data); err != nil {
		return err
	}
	return f.Close()
}

// appendLines appends lines to a lane state file, the same append-only write the receipt
// plan makes: one file per lane, only ever added to.
func appendLines(root, rel string, lines []string) error {
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := insideRoot(root, full); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	f, err := openLaneFile(root, full, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }() // ignored: a write error is returned, and the explicit close on success is the one reported
	for _, line := range lines {
		if _, err := io.WriteString(f, line+"\n"); err != nil {
			return err
		}
	}
	return f.Close()
}

// gitFS is a read-only fs.FS over one commit's tree, so a reader can open the bus as it
// was at its cursor. It reads the tree with ls-tree and the blobs with cat-file/show.
type gitFS struct {
	dir    string
	commit string
}

func (g gitFS) Open(name string) (fs.File, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrInvalid}
	}
	if name == "." {
		entries, err := g.readDir(".")
		if err != nil {
			return nil, err
		}
		return &gitDir{name: ".", entries: entries}, nil
	}
	kind, err := git(g.dir, "cat-file", "-t", g.commit+":"+name)
	if err != nil {
		entries, derr := g.readDir(name)
		if derr != nil {
			return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
		}
		return &gitDir{name: name, entries: entries}, nil
	}
	if strings.TrimSpace(kind) == "tree" {
		entries, err := g.readDir(name)
		if err != nil {
			return nil, err
		}
		return &gitDir{name: name, entries: entries}, nil
	}
	data, err := git(g.dir, "show", g.commit+":"+name)
	if err != nil {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return &gitFile{name: path.Base(name), data: []byte(data)}, nil
}

func (g gitFS) ReadDir(name string) ([]fs.DirEntry, error) { return g.readDir(name) }

func (g gitFS) Stat(name string) (fs.FileInfo, error) {
	if !fs.ValidPath(name) {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrInvalid}
	}
	if name == "." {
		return gitInfo{name: ".", dir: true, size: 0}, nil
	}
	kind, err := git(g.dir, "cat-file", "-t", g.commit+":"+name)
	if err != nil {
		return nil, &fs.PathError{Op: "stat", Path: name, Err: fs.ErrNotExist}
	}
	if strings.TrimSpace(kind) == "tree" {
		return gitInfo{name: path.Base(name), dir: true, size: 0}, nil
	}
	size, err := git(g.dir, "cat-file", "-s", g.commit+":"+name)
	if err != nil {
		return nil, fmt.Errorf("%s: size: %w", name, err)
	}
	var n int64
	if _, err := fmt.Sscanf(strings.TrimSpace(size), "%d", &n); err != nil {
		return nil, fmt.Errorf("%s: size: git cat-file -s did not answer with a number: %w", name, err)
	}
	return gitInfo{name: path.Base(name), size: n}, nil
}

func (g gitFS) readDir(name string) ([]fs.DirEntry, error) {
	spec := g.commit
	if name != "." && name != "" {
		spec = g.commit + ":" + name
	}
	out, err := git(g.dir, "ls-tree", "-z", spec)
	if err != nil {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrNotExist}
	}
	var entries []fs.DirEntry
	for _, line := range strings.Split(out, "\x00") {
		if line == "" {
			continue
		}
		meta, entry, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		fields := strings.Fields(meta)
		if len(fields) < 2 {
			continue
		}
		entries = append(entries, gitInfo{name: path.Base(entry), dir: fields[1] == "tree"})
	}
	return entries, nil
}

// gitFile is one blob read out of the object store.
type gitFile struct {
	name string
	data []byte
	off  int
}

func (f *gitFile) Stat() (fs.FileInfo, error) {
	return gitInfo{name: f.name, size: int64(len(f.data))}, nil
}

func (f *gitFile) Read(p []byte) (int, error) {
	if f.off >= len(f.data) {
		return 0, io.EOF
	}
	n := copy(p, f.data[f.off:])
	f.off += n
	return n, nil
}

func (f *gitFile) Close() error { return nil }

// gitDir is one tree, listed with ls-tree.
type gitDir struct {
	name    string
	entries []fs.DirEntry
}

func (d *gitDir) Stat() (fs.FileInfo, error) { return gitInfo{name: d.name, dir: true}, nil }
func (d *gitDir) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: d.name, Err: fs.ErrInvalid}
}
func (d *gitDir) Close() error { return nil }
func (d *gitDir) ReadDir(int) ([]fs.DirEntry, error) {
	return d.entries, nil
}

// gitInfo is fs.FileInfo for a path in a tree.
type gitInfo struct {
	name string
	dir  bool
	size int64
}

func (i gitInfo) Name() string { return i.name }
func (i gitInfo) Size() int64  { return i.size }
func (i gitInfo) Mode() fs.FileMode {
	if i.dir {
		return fs.ModeDir | 0o755
	}
	return 0o644
}
func (i gitInfo) ModTime() time.Time { return time.Time{} }
func (i gitInfo) IsDir() bool        { return i.dir }
func (i gitInfo) Sys() any           { return nil }

// Type is the fs.DirEntry type bits, and Info makes gitInfo usable as one entry.
func (i gitInfo) Type() fs.FileMode          { return i.Mode().Type() }
func (i gitInfo) Info() (fs.FileInfo, error) { return i, nil }
