package cairn

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// The store's shape is decided from its contents, once, here, and every verb
// asks this file and nothing else. The model of the lifecycle that depends on
// it is tla/CairnStore.tla (checked by TLC; the records are in tla/RUNS.tsv); its
// "one shape per store" invariant is what MixedShapeError enforces and its
// Open action is what openBench implements.
//
//	bench  at least one top-level <id>.md session file and no sessions/,
//	       entries/ or log.jsonl; README.md, in any case, is documentation and
//	       never a session file, so it counts for no shape
//	own    anything with sessions/, entries/ or log.jsonl and no top-level
//	       <id>.md; an empty or absent store directory is own too, so a new
//	       store gets the tool's own shape
//	mixed  both; every verb refuses it
type shape int

const (
	shapeOwn shape = iota
	shapeBench
	shapeMixed
)

// MixedShapeError is the refusal for a store that holds the bench shape and
// the tool's own shape at once. It names the operation, the cause, the paths
// found of each shape and the next action, so the caller need not inspect the
// store to learn what to do.
type MixedShapeError struct {
	Op    string
	Store string
	Bench []string // top-level <id>.md files, sorted
	Own   []string // sessions/, entries/, log.jsonl as found
}

func (e *MixedShapeError) Error() string {
	shown := e.Bench
	more := ""
	if len(shown) > 3 {
		more = fmt.Sprintf(" and %d more", len(shown)-3)
		shown = shown[:3]
	}
	return fmt.Sprintf("cannot %s: store %q holds two shapes at once (bench files: %s%s; own shape: %s); "+
		"keep one shape by moving the other shape's paths out of the store (the top-level <id>.md files, or sessions/, entries/ and log.jsonl), "+
		"then run the same command again; the tool moves and deletes nothing",
		opPhrase(e.Op), e.Store, strings.Join(shown, ", "), more, strings.Join(e.Own, ", "))
}

// dirs is what one verb knows about the store's directories. It reads each
// directory at most once, so a verb that looks at many records (index) asks the
// disk about the store's directory once, not once per record. It refuses links:
// nova-cairn does not follow a symbolic link at the store directory's own name,
// at sessions/, entries/, an entry directory, log.jsonl or a record. Every
// directory looked at is remembered with its identity, and every file opened is
// proved to be the file at its name, in directories still the ones first seen,
// so a swap between the check and the use is refused. The read function is a
// field so a test can count the reads.
type dirs struct {
	op       string // the verb, for refusals
	read     func(string) ([]os.DirEntry, error)
	listed   map[string]dirListing
	verified map[string]os.FileInfo
}

type dirListing struct {
	entries []os.DirEntry
	err     error
}

func newDirs() *dirs {
	d := &dirs{op: "read", listed: map[string]dirListing{}, verified: map[string]os.FileInfo{}}
	d.read = d.readChecked
	return d
}

func (d *dirs) list(path string) ([]os.DirEntry, error) {
	if l, ok := d.listed[path]; ok {
		return l.entries, l.err
	}
	entries, err := d.read(path)
	d.listed[path] = dirListing{entries, err}
	return entries, err
}

func isLink(fi os.FileInfo) bool { return fi.Mode()&os.ModeSymlink != 0 }

// checkDir refuses a directory that is a link and remembers the identity of one
// that is not. A directory that is not there is the caller's to judge.
func (d *dirs) checkDir(path string) error {
	path = filepath.Clean(path)
	li, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if isLink(li) {
		return linkRefusal(d.op, path)
	}
	if was, ok := d.verified[path]; ok && !os.SameFile(was, li) {
		return changedRefusal(d.op, path)
	}
	d.verified[path] = li
	return nil
}

// recheck proves every directory looked at is still the same one, and no link.
func (d *dirs) recheck() error {
	for path, was := range d.verified {
		if li, err := os.Lstat(path); err != nil || isLink(li) || !os.SameFile(was, li) {
			return changedRefusal(d.op, path)
		}
	}
	return nil
}

// readChecked lists a directory through the open handle, after proving the
// handle is the directory at the name.
func (d *dirs) readChecked(path string) ([]os.DirEntry, error) {
	path = filepath.Clean(path)
	if err := d.checkDir(path); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if held, err := f.Stat(); err != nil || !os.SameFile(held, d.verified[path]) {
		return nil, changedRefusal(d.op, path)
	}
	entries, err := f.ReadDir(-1)
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	return entries, err
}

// openFile opens the file at path and proves the handle is the regular file at
// that name, that the name is no link, and that the directories above are still
// the ones checked. What is read or written afterwards goes through the handle.
func (d *dirs) openFile(path string, flag int, perm os.FileMode) (*os.File, error) {
	if err := d.recheck(); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, flag|noFollow, perm)
	li, lerr := os.Lstat(path)
	if lerr == nil && isLink(li) {
		if f != nil {
			f.Close()
		}
		return nil, linkRefusal(d.op, path)
	}
	if err != nil {
		return nil, err
	}
	if held, err := f.Stat(); err != nil || lerr != nil || !held.Mode().IsRegular() || !os.SameFile(held, li) || d.recheck() != nil {
		f.Close()
		return nil, changedRefusal(d.op, path)
	}
	return f, nil
}

// readFile is the whole content of the file at path, read through openFile.
func (d *dirs) readFile(path string) ([]byte, error) {
	f, err := d.openFile(path, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(f)
}

// linkRefusal is the one refusal for a symbolic link: it names the path, says
// what it is and that the tool does not follow links, and gives a command that
// shows where it leads and writes nothing.
func linkRefusal(op, path string) error {
	return &RecordPathError{Msg: fmt.Sprintf("cannot %s: %q is a symbolic link and nova-cairn does not follow links; "+
		"see where it leads with: %s; then put the real file or directory there, or use another store or session id",
		opPhrase(op), path, shellLine("ls -ld -- %s", shellWord{"path", path}))}
}

// changedRefusal is the refusal for a path that stopped being what the verb
// checked while the verb ran.
func changedRefusal(op, path string) error {
	return &RecordPathError{Msg: fmt.Sprintf("cannot %s: %q changed while the command was running; nothing was written through it, run the same command again",
		opPhrase(op), path)}
}

// storeShape reads the store's contents and names its shape. A directory that
// does not exist has no contents and is the tool's own shape. Any other read
// failure is returned, never read as "empty".
func storeShape(op, store string) (shape, error) { return storeShapeIn(newDirs(), op, store) }

func storeShapeIn(d *dirs, op, store string) (shape, error) {
	d.op = op
	files, err := d.list(store)
	if os.IsNotExist(err) {
		return shapeOwn, nil
	}
	var refused *RecordPathError
	if errors.As(err, &refused) {
		return shapeOwn, err
	}
	if err != nil {
		return shapeOwn, fmt.Errorf("cannot %s: cannot read store %q: %v", opPhrase(op), store, err)
	}
	var bench, own []string
	for _, f := range files {
		name := f.Name()
		switch {
		case (name == "sessions" || name == "entries") && (f.IsDir() || f.Type()&os.ModeSymlink != 0):
			// A symlink named like an own-shape directory is a marker too,
			// whatever it points at: the store's contents decide the shape,
			// not what a link resolves to.
			own = append(own, name+"/")
		case name == "log.jsonl" && !f.IsDir():
			own = append(own, name)
		case !f.IsDir() && strings.HasSuffix(name, ".md") && sessionFileID(strings.TrimSuffix(name, ".md")):
			bench = append(bench, name)
		}
	}
	switch {
	case len(bench) > 0 && len(own) > 0:
		return shapeMixed, &MixedShapeError{Op: op, Store: store, Bench: bench, Own: own}
	case len(bench) > 0:
		return shapeBench, nil
	}
	return shapeOwn, nil
}

// benchHeader is the short header a bench open writes: a title, one line
// naming the full session id and the open stamp, and the source pointer when
// one was given. The source is escaped to one line so it can never form a
// section heading.
func benchHeader(session, source, stamp string) string {
	short := session
	if r := []rune(session); len(r) > 8 {
		short = string(r[:8])
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# Cairn %s\n\nSession %s opened %s\n", short, session, stamp)
	if source != "" {
		fmt.Fprintf(&b, "Source: %s\n", oneline.Escape(source))
	}
	return b.String()
}

// benchHeaderSource reads the source pointer back out of a header this tool
// wrote: line one `# Cairn ...`, a blank line, `Session <id> opened <stamp>`,
// then `Source: <ptr>`. A file with any other opening, a hand-kept record
// included, has no session source. The pointer is the escaped single line open
// wrote.
func benchHeaderSource(raw []byte) string {
	lines := strings.SplitN(string(raw), "\n", 5)
	if len(lines) < 4 || !strings.HasPrefix(lines[0], "# Cairn ") || lines[1] != "" ||
		!strings.HasPrefix(lines[2], "Session ") || !strings.Contains(lines[2], " opened ") {
		return ""
	}
	return sourceLine(lines[3])
}

func sourceLine(line string) string {
	if !strings.HasPrefix(line, "Source: ") {
		return ""
	}
	return strings.TrimPrefix(line, "Source: ")
}

// openBench creates the bench record for a session that has none. An
// existing regular file is already open and stays as it is. Anything else at
// the record path (a directory, a link, a device) is refused, naming the path
// and what is there: open reporting success over it would leave every later
// verb unable to find the record.
func openBench(d *dirs, store, session, source, stamp string) error {
	name := benchFile(store, session)
	exists, err := recordState(d, "open", name)
	if err != nil || exists {
		return err
	}
	// NoReplace: a record that appears between the check and the write is kept.
	err = atomicfile.WriteFile(name, []byte(benchHeader(session, source, stamp)), 0o644, atomicfile.NoReplace())
	if err != nil && errors.Is(err, os.ErrExist) {
		// Whatever is there now, it is judged by what it is, not by the fact
		// that something exists.
		exists, err = recordState(d, "open", name)
		if err == nil && !exists {
			return fmt.Errorf("cannot open a session: %q changed while it was being created; run the same command again", name)
		}
		return err
	}
	return err
}

// recordState judges the path a session record lives at. It reports whether a
// record stands there: true for a regular file, false when nothing is there,
// and an error naming the path, what was found and the next action for anything
// else, a symbolic link included. The error is about this one session's path
// only.
func recordState(d *dirs, op, path string) (bool, error) {
	if err := d.checkDir(filepath.Dir(path)); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	li, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("cannot %s: cannot read %q: %v", opPhrase(op), path, err)
	}
	found := ""
	switch {
	case isLink(li):
		return false, linkRefusal(op, path)
	case li.IsDir():
		found = "a directory"
	case !li.Mode().IsRegular():
		found = fmt.Sprintf("not a regular file (%s)", li.Mode().Type())
	default:
		// On a disk that folds case, the path above may have reached a file
		// whose name differs from the one asked for. Only the exact name is a
		// session file, so the other is neither read nor written through.
		if listed, ok := d.foldedName(path); ok {
			return false, &RecordPathError{Msg: fmt.Sprintf("cannot %s: the session record %q matches the existing %q only by letter case; "+
				"only the exact name %q is a session file: rename that file or choose another session id",
				opPhrase(op), path, listed, filepath.Base(path))}
		}
		return true, nil
	}
	return false, &RecordPathError{Msg: fmt.Sprintf("cannot %s: the session record %q is %s; move or remove it, or choose another session id",
		opPhrase(op), path, found)}
}

// foldedName reports the directory entry that path reached by letter case
// alone: the name it holds when that is not the name asked for. The directory
// is read once per verb, through dirs.
func (d *dirs) foldedName(path string) (string, bool) {
	entries, err := d.list(filepath.Dir(path))
	if err != nil {
		return "", false
	}
	want, folded := filepath.Base(path), ""
	for _, e := range entries {
		if e.Name() == want {
			return "", false
		}
		if strings.EqualFold(e.Name(), want) {
			folded = e.Name()
		}
	}
	return folded, folded != ""
}

// RecordPathError is the refusal for a session record path holding something
// that is not a record. Its message carries its own next action.
type RecordPathError struct{ Msg string }

func (e *RecordPathError) Error() string { return e.Msg }

// opPhrase is the verb as it reads in a refusal sentence.
func opPhrase(op string) string {
	switch op {
	case "open":
		return "open a session"
	case "append":
		return "append an entry"
	case "index":
		return "index the store"
	case "receipt":
		return "read a receipt"
	}
	return op
}
