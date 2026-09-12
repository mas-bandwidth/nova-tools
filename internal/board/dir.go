/*
dir.go is the directory backend: one file per card, `<dir>/<id>.board`, created by add and
appended to by take and close. The board is a FOLD over every file in the directory.

ONE FILE PER CARD, ONE WRITER PER CARD, AND NO LOCK. There is no shared file and no index,
so there is no serial step for two lines to queue behind: an add creates a file whose name
is the tool-owned id, so two adds of one thing are two files; a take or a close appends one
line to one file, and a second line appending to the same file appends a DIFFERENT line,
which a union merge keeps in either order and the fold order sorts. The tool holds no lock
because it needs none.

CREATION IS EXCLUSIVE AGAINST HAND-MADE FILES. The card file is created with O_EXCL and is
never truncated. An id that already exists is a refusal at exit 1 and no file is ever
replaced.

THE DIRECTORY BACKEND APPENDS AND NEVER RUNS GIT. Committing and pushing it is the
caller's, and saying so is more honest than a push hidden inside add. That is the one
asymmetry between the backends: an add --dir is durable when the caller lands it, and an
add --issue is durable when the command returns.
*/
package board

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Dir is the directory backend.
type Dir struct {
	path string
}

// NewDir returns the backend for a directory of card files. The directory must exist: a
// tool that made one would be guessing at where a caller meant to keep a board -- the
// board lives in somebody's repository, and which directory is tracked, and by which
// clone, is the caller's decision and not this tool's. SPEC-BOARD grants this backend one
// creation and names it: "One file per card, <dir>/<id>.board, created by add."
//
// So the refusal CARRIES THE REMEDY. Emma, dogfooding v0.12.0 (nova-tools #104): a
// quickstart against a directory that did not exist exited 2 with a stat error and no way
// forward, while nova-swarm's quickstart makes its own pool. The asymmetry is deliberate
// -- a swarm pool is a layout this family owns, a board directory is one directory in
// somebody's repository -- but a refusal that does not say `mkdir -p` makes a reader
// guess at a tool that refuses to.
func NewDir(path string) (*Dir, error) {
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("--dir wants a directory of .board card files and does not create one, because which directory holds a board is yours: %s does not exist; make it first: mkdir -p %s", path, path)
	}
	if err != nil {
		return nil, fmt.Errorf("--dir wants a directory of .board card files: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("--dir wants a directory of .board card files, and %s is a file", path)
	}
	return &Dir{path: path}, nil
}

// Source is what the listing's source= field carries, so a listing cannot be mistaken for
// a different board's.
func (d *Dir) Source() string { return d.path }

// Events reads every card file whole and returns their lines. A file that is not
// `<thirty-two hex>.board` is COUNTED and never read as a card; a card file without its
// version line is an error, because a later format read as this one would be entries
// nobody wrote.
func (d *Dir) Events() (Log, error) {
	entries, err := os.ReadDir(d.path)
	if err != nil {
		return Log{}, fmt.Errorf("reading the board directory: %w", err)
	}
	log := Log{}
	for _, entry := range entries {
		name := entry.Name()
		id, ok := strings.CutSuffix(name, ".board")
		if entry.IsDir() || !ok || !Hex(id, IDHex) {
			log.UnparsedFiles++
			continue
		}
		raw, err := os.ReadFile(filepath.Join(d.path, name))
		if err != nil {
			return Log{}, fmt.Errorf("reading %s: %w", name, err)
		}
		lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
		if len(lines) == 0 || strings.TrimSpace(lines[0]) != Version {
			return Log{}, fmt.Errorf("%s does not begin with its version line %q; a later format read as this one would be entries nobody wrote", name, Version)
		}
		log.Lines = append(log.Lines, lines[1:]...)
	}
	return log, nil
}

// Append writes one event line. A card event creates its file with O_EXCL and never
// truncates; every other event appends to the file its id names.
func (d *Dir) Append(line string) error {
	e, ok := Parse(line)
	if !ok {
		return fmt.Errorf("refusing to append a line this tool cannot parse as an event")
	}
	path := filepath.Join(d.path, e.ID+".board")
	if e.Verb == "card" {
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			if os.IsExist(err) {
				return ErrExists
			}
			return err
		}
		defer f.Close()
		if _, err := fmt.Fprintf(f, "%s\n%s\n", Version, line); err != nil {
			return err
		}
		return f.Sync()
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		if os.IsNotExist(err) {
			return ErrNoCard
		}
		return err
	}
	defer f.Close()
	if _, err := fmt.Fprintf(f, "%s\n", line); err != nil {
		return err
	}
	return f.Sync()
}
