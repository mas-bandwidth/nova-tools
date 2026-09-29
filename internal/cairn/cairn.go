// Package cairn is the mechanical half of nova-cairn, the optional
// checkpoint tool from nova-tools #248.
//
// It opens session records, appends the friend's exact words with a real
// clock stamp, stable identifiers and source pointers, and builds a bounded
// index and coverage ledger over them — and nothing else. There is no seal,
// no consume, no delete, no grading, no consolidation and no liveness
// inference here: those are separate explicit choices the caller never gets
// by accident. The store is plain files under a caller-named directory, so a
// note is durable (fsync) before success is reported, independently of Redis
// and of any remote: local persistence is reported separately from remote
// publication, and neither implies replicated durability.
//
// Layout under the store directory:
//
//	sessions/<session>.md        the readable record; header by convention only
//	entries/<session>/<id>.json  one file per entry, the source of truth
//	log.jsonl                    append-only event log feeding the ledger
//
// A store that keeps ONE MARKDOWN FILE PER SESSION directly under it --
// <session>.md, the shape a friend appending by hand has -- is a bench store.
// The shape is decided from the store's contents by storeShape (shape.go):
// a top-level <id>.md and no sessions/, entries/ or log.jsonl is a bench
// store; anything else, including an empty or absent directory, is the tool's
// own shape; a store holding both is refused by every verb. On a bench store
// `open` creates <session>.md with a short header when none exists and is a
// no-op when one does, and `append` lands a dated `## <stamp> — <entry>`
// section at the end of the file, with no sessions/, entries/ or log.jsonl
// and no index appearing beside it. The tool adapts to the store; the store
// is never converted to suit the tool. The lifecycle is modelled in
// tla/CairnStore.tla (checked by TLC; the records are in tla/RUNS.tsv).
//
// Each entry file is written atomically via internal/atomicfile (exclusive
// temporary file beside target, explicit mode, fsync to media, atomic rename),
// so a retry after an interrupted append finishes the pointer without
// duplicating the entry and without touching other writers' files. Stale
// temporary files are never indexed.
package cairn

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
)

// Publish policies name who publishes a checkpoint and when. This slice
// implements no transport: every append reports Published=false and the
// policy travels with the entry, so a later explicit act can carry it.
const (
	PublishNever     = "never"
	PublishManual    = "manual"
	PublishDeferred  = "deferred"
	PublishImmediate = "immediate"
)

// validPublish holds the caller-chosen publication policies.
func validPublish(p string) bool {
	switch p {
	case PublishNever, PublishManual, PublishDeferred, PublishImmediate:
		return true
	}
	return false
}

// ConflictError is a retry that must not silently win: the same entry id
// carrying different prose. It is the caller's failure (exit 1), not a crash.
type ConflictError struct{ Msg string }

func (e *ConflictError) Error() string { return e.Msg }

// NotFoundError is a use-before-open or a receipt for nothing stored.
type NotFoundError struct{ Msg string }

func (e *NotFoundError) Error() string { return e.Msg }

// AppendResult separates what this slice guarantees from what it does not:
// Persisted is true once the note is fsync-durable on this machine;
// Published names the remote, which this slice never touches.
type AppendResult struct {
	Stamp     time.Time // the timestamp actually stored, including on a duplicate retry
	Persisted bool
	Published bool
	Policy    string
	// Source is the pointer the entry carries: its own --source, or the
	// session's when the append named none.
	Source    string
	Duplicate bool
}

// ReceiptInfo is what receipt and index read back: the stamp, the pointer,
// the size, and the same persisted/published split the append reported.
type ReceiptInfo struct {
	Session   string
	ID        string
	Stamp     time.Time
	Source    string
	Bytes     int
	Policy    string
	Persisted bool
	Published bool
}

// IndexRow is one mechanical row of the section/entry index.
type IndexRow struct {
	Session string
	ID      string
	Stamp   time.Time
	Source  string
	Bytes   int
}

// Ledger is the coverage count, derived from the store rather than
// remembered: how many session records exist and how many entries they hold.
type Ledger struct {
	Sessions int
	Entries  int
}

// entryFile is the on-disk form of one entry: the friend's exact words plus
// the stamp, identifiers and pointers that make the receipt checkable.
type entryFile struct {
	Session string `json:"session"`
	ID      string `json:"id"`
	Stamp   string `json:"stamp"`
	Source  string `json:"source"`
	Publish string `json:"publish"`
	Text    string `json:"text"`
}

// validID keeps identifiers stable and file-safe: nonempty, bounded, and
// free of separators, escapes and whitespace, so an id is one token on every
// output line and one file under entries/.
func validID(s string) bool {
	if s == "" || len(s) > 128 {
		return false
	}
	if s == "." || s == ".." || strings.Contains(s, "..") {
		return false
	}
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
		if r == '/' || r == '\\' {
			return false
		}
	}
	return true
}

// reservedSession reports the one name a session may never take: README, in
// any case. A top-level README.md is documentation for whoever reads the
// store, in either shape, never a session record. The rule is outside
// tla/CairnStore.tla, whose sessions are an abstract set of ids.
func reservedSession(id string) bool {
	return strings.EqualFold(id, "readme")
}

// sessionFileID reports whether a top-level <id>.md names a session record.
func sessionFileID(id string) bool {
	return validID(id) && !reservedSession(id)
}

// checkSession refuses a session id that cannot name a record, naming the
// cause. Read verbs and write verbs share it.
func checkSession(session string) error {
	if !validID(session) {
		return fmt.Errorf("bad session id %q: nonempty, no slashes, no whitespace", session)
	}
	if reservedSession(session) {
		return fmt.Errorf("session id %q is reserved: README.md in a store is documentation, never a session record; choose another session id", session)
	}
	return nil
}

func sessionFile(store, session string) string {
	return filepath.Join(store, "sessions", session+".md")
}

// benchFile is the bench store's record: one markdown file per session
// directly under the store, kept and appended by hand or created by open.
// Nothing is migrated, nothing is renamed, and a bench file gets no sidecar:
// the file IS the record.
func benchFile(store, session string) string {
	return filepath.Join(store, session+".md")
}

// locateRecord returns the session record's path for a store of the given
// shape and whether it is a bench file. Only the shape's own location is
// consulted: a store never answers from two places.
func locateRecord(store, session string, sh shape) (path string, bench, ok bool) {
	name, bench := sessionFile(store, session), false
	if sh == shapeBench {
		name, bench = benchFile(store, session), true
	}
	if fileExists(name) {
		return name, bench, true
	}
	return "", bench, false
}

func fileExists(name string) bool {
	info, err := os.Stat(name)
	return err == nil && !info.IsDir()
}

// noRecord is the refusal for a verb addressing a session nothing holds. It
// names the remedy VERB whole, flags and all: the refusal that cost an hour
// said `open first` and left the friend to rebuild the invocation from the
// usage text -- on a store where running it would have been wrong.
func noRecord(store, session, publish string) error {
	if !validPublish(publish) {
		publish = PublishManual
	}
	return &NotFoundError{Msg: fmt.Sprintf(
		"no such session %q under store %q; open first: %s",
		session, store, openRemedy(store, session, publish))}
}

// benchHeadingRe reads the one heading this tool writes into a bench file:
// `## <rfc3339> — <entry>`. It is the section boundary too, which is why the
// form is machine-tight -- a hand-written `## 21:55Z beat: …` heading in the
// same file is NOT a boundary, so a friend's prose may carry its own `##`
// headings without an append cutting the record in two.
var benchHeadingRe = regexp.MustCompile(`^## ([0-9]{4}-[0-9]{2}-[0-9]{2}T[^ ]+) — (\S+)$`)

// benchHeading is the dated section heading for one entry.
func benchHeading(id string, stamp time.Time) string {
	return fmt.Sprintf("## %s — %s", stamp.UTC().Format(time.RFC3339), id)
}

// benchSection returns the prose already filed under this entry id in a bench
// file, its stored timestamp, and whether it is there at all. The body runs
// from the heading to the next heading of the same machine form, or EOF.
func benchSection(raw []byte, id string) (body, stamp string, found bool) {
	lines := strings.Split(string(raw), "\n")
	for i, line := range lines {
		m := benchHeadingRe.FindStringSubmatch(line)
		if m == nil || m[2] != id {
			continue
		}
		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			if benchHeadingRe.MatchString(lines[j]) {
				end = j
				break
			}
		}
		return strings.TrimSpace(strings.Join(lines[i+1:end], "\n")), m[1], true
	}
	return "", "", false
}

// appendBench files one entry into a bench record: a dated section at the end
// of the file, in the file's own shape (one blank line between sections), the
// friend's words under it. A retry with the same id and the same words adds
// nothing; the same id with different words is a conflict, as it is in the
// nested store. No index is written and no directory appears beside the file:
// this store is read, not converted.
func appendBench(path, id, text string, now time.Time, publish string) (AppendResult, error) {
	var res AppendResult
	raw, err := os.ReadFile(path)
	if err != nil {
		return res, err
	}
	if prev, storedStamp, found := benchSection(raw, id); found {
		if prev != strings.TrimSpace(text) {
			return res, &ConflictError{Msg: fmt.Sprintf("entry %q already holds different prose; pick a new id", id)}
		}
		stamp, err := time.Parse(time.RFC3339Nano, storedStamp)
		if err != nil {
			return res, fmt.Errorf("stored entry %q has an invalid stamp: %w", id, err)
		}
		return AppendResult{Stamp: stamp, Persisted: true, Published: false, Policy: publish, Duplicate: true}, nil
	}
	var b strings.Builder
	if len(raw) > 0 && !strings.HasSuffix(string(raw), "\n") {
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(benchHeading(id, now))
	b.WriteString("\n\n")
	b.WriteString(strings.TrimRight(text, "\n"))
	b.WriteString("\n")
	if err := appendBytes(path, b.String()); err != nil {
		return res, err
	}
	return AppendResult{Stamp: now.UTC().Truncate(time.Second), Persisted: true, Published: false, Policy: publish}, nil
}

// appendBytes adds content to an existing file and fsyncs before return, so a
// bench append is as durable as a nested one before success is reported.
func appendBytes(name, content string) error {
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func entryPath(store, session, id string) string {
	return filepath.Join(store, "entries", session, id+".json")
}

// appendLine adds one line to a file, creating it, and fsyncs before return.
func appendLine(name, line string) error {
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(line + "\n"); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return fsyncDir(filepath.Dir(name))
}

func fsyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return nil
	}
	defer d.Close()
	_ = d.Sync()
	return nil
}

// appendLog records one event on the store's append-only log.
func appendLog(store, event, session, id, stamp, policy, source string) error {
	rec, _ := json.Marshal(map[string]string{
		"event":   event,
		"session": session,
		"entry":   id,
		"stamp":   stamp,
		"publish": policy,
		"source":  source,
	})
	return appendLine(filepath.Join(store, "log.jsonl"), string(rec))
}

// SessionSource reads back the --source the session was opened with, from
// the open record in log.jsonl. The session file's header is convention only
// and is never parsed, so the log is where the pointer is read from.
//
// CORRUPT PROVENANCE NEVER READS AS NONE. An absent log is a store with no
// open records in it (a bench store, or one opened before the log carried a
// source) and answers "" with no error, as does an open record with no source
// key. A log that exists and cannot be read is an error, and so is a line
// that may be this session's open record and does not decode as one: a line
// naming the session and the open event that is not valid JSON, or whose
// fields are not strings. Answering "" for either would let an append file
// source=- over a pointer that open recorded.
func SessionSource(store, session string) (string, error) {
	name := filepath.Join(store, "log.jsonl")
	raw, err := os.ReadFile(name)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		var pe *os.PathError
		if errors.As(err, &pe) {
			err = pe.Err
		}
		return "", fmt.Errorf("cannot read the session's source from %s: %v", name, err)
	}
	quotedSession, _ := json.Marshal(session)
	for i, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var rec map[string]string
		if json.Unmarshal([]byte(line), &rec) != nil {
			// Not a record this reader can decode. It is only this
			// session's business if it could be this session's open.
			if strings.Contains(line, string(quotedSession)) && strings.Contains(line, `"open"`) {
				return "", fmt.Errorf("%s:%d may be the open record of session %q and does not decode as one; refusing to read its source as none", name, i+1, session)
			}
			continue
		}
		if rec["event"] == "open" && rec["session"] == session {
			return rec["source"], nil
		}
	}
	return "", nil
}

// Open starts (or re-starts, idempotently) one session record. Concurrent
// records coexist: opening a second session never touches the first.
func Open(store, session, source string, now time.Time, publish string) error {
	if store == "" {
		return errors.New("no store given; refusing to guess")
	}
	if err := checkSession(session); err != nil {
		return err
	}
	if !validPublish(publish) {
		return fmt.Errorf("bad publish policy %q: never|manual|deferred|immediate", publish)
	}
	sh, err := storeShape("open", store)
	if err != nil {
		return err
	}
	if sh == shapeBench {
		return openBench(store, session, source, now.UTC().Format(time.RFC3339))
	}
	// The source a session was opened with must be readable before anything
	// is written or reported: open prints it, and append inherits it.
	if _, err := SessionSource(store, session); err != nil {
		return err
	}
	// Re-open is a no-op: the record already stands.
	if _, _, ok := locateRecord(store, session, sh); ok {
		return nil
	}
	name := sessionFile(store, session)
	stamp := now.UTC().Format(time.RFC3339Nano)
	header := fmt.Sprintf("# cairn %s\n\nOpened: %s\nSource: %s\nPublish: %s\n",
		session, stamp, source, publish)
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return err
	}
	// Atomic write per internal/atomicfile model: temporary file created
	// exclusively in parent directory, explicit 0o644 mode, fsync to media,
	// atomic rename over target path.
	if err := atomicfile.WriteFile(name, []byte(header), 0o644); err != nil {
		return err
	}
	return appendLog(store, "open", session, "", stamp, publish, source)
}

// pointerLine is the one machine-scannable line an append adds to the
// readable record. The header above it is convention only and is never
// parsed, so friends with different headings lose nothing.
func pointerLine(id string, stamp time.Time) string {
	return fmt.Sprintf("ENTRY %s %s", id, stamp.UTC().Format(time.RFC3339Nano))
}

// ensurePointer heals an interrupted append: the entry file is already
// durable but its pointer line never landed, so land it now without
// duplicating a line that is already there.
func ensurePointer(store, session, id string, stamp time.Time) error {
	name := sessionFile(store, session)
	raw, err := os.ReadFile(name)
	if err != nil {
		return &NotFoundError{Msg: fmt.Sprintf("no such session %q; open first", session)}
	}
	want := pointerLine(id, stamp)
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "ENTRY "+id+" ") || line == want {
			return nil
		}
	}
	return appendLine(name, want)
}

// Append stores the friend's exact prose under a stable entry id with a real
// clock stamp and source pointers. A retry of the same request succeeds with
// Duplicate=true and no second entry; the same id with different prose is a
// conflict, never an overwrite. Offline use succeeds: the result carries
// persisted=true with published=false, because local durability never waited
// for the remote.
func Append(store, session, id, text, source string, now time.Time, publish string) (AppendResult, error) {
	var res AppendResult
	if store == "" {
		return res, errors.New("no store given; refusing to guess")
	}
	if err := checkSession(session); err != nil {
		return res, err
	}
	if !validID(id) {
		return res, fmt.Errorf("bad entry id %q: nonempty, no slashes, no whitespace", id)
	}
	if !validPublish(publish) {
		return res, fmt.Errorf("bad publish policy %q: never|manual|deferred|immediate", publish)
	}
	if text == "" {
		return res, errors.New("empty note stores nothing; refusing to file it")
	}
	sh, err := storeShape("append", store)
	if err != nil {
		return res, err
	}
	path, bench, ok := locateRecord(store, session, sh)
	if !ok {
		return res, noRecord(store, session, publish)
	}
	stamp := now.UTC()
	if bench {
		return appendBench(path, id, text, stamp, publish)
	}
	// AN ENTRY WITH NO --source CARRIES THE SESSION'S. open --source names
	// where the record points back to; an append that names nothing else came
	// from the same place, so the entry records that pointer and index and
	// receipt read it back. The dogfood finding (2026-09-18): open carried
	// --source session:x, and every entry line then printed source= empty.
	// The read comes BEFORE any entry or pointer write, so a log that cannot
	// be read refuses with nothing written.
	if source == "" {
		inherited, err := SessionSource(store, session)
		if err != nil {
			return res, err
		}
		source = inherited
	}
	final := entryPath(store, session, id)
	if raw, err := os.ReadFile(final); err == nil {
		var prev entryFile
		if err := json.Unmarshal(raw, &prev); err != nil {
			return res, fmt.Errorf("stored entry %q is corrupt: %v", id, err)
		}
		if prev.Text != text {
			return res, &ConflictError{Msg: fmt.Sprintf("entry %q already holds different prose; pick a new id", id)}
		}
		prevStamp, err := time.Parse(time.RFC3339Nano, prev.Stamp)
		if err != nil {
			return res, fmt.Errorf("stored entry %q has an invalid stamp: %w", id, err)
		}
		if err := ensurePointer(store, session, id, prevStamp); err != nil {
			return res, err
		}
		return AppendResult{Stamp: prevStamp, Persisted: true, Published: false, Policy: prev.Publish, Source: prev.Source, Duplicate: true}, nil
	}
	rec, _ := json.Marshal(entryFile{
		Session: session,
		ID:      id,
		Stamp:   stamp.Format(time.RFC3339Nano),
		Source:  source,
		Publish: publish,
		Text:    text,
	})
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return res, err
	}
	// Atomic write per internal/atomicfile model: temporary file created
	// exclusively in parent directory, explicit 0o644 mode, fsync to media,
	// atomic rename over target path.
	if err := atomicfile.WriteFile(final, rec, 0o644); err != nil {
		return res, err
	}
	if err := ensurePointer(store, session, id, stamp); err != nil {
		return res, err
	}
	if err := appendLog(store, "append", session, id, stamp.Format(time.RFC3339Nano), publish, source); err != nil {
		return res, err
	}
	return AppendResult{Stamp: stamp, Persisted: true, Published: false, Policy: publish, Source: source}, nil
}

// readEntry loads one stored entry or explains its absence.
func readEntry(store, session, id string) (entryFile, error) {
	var ef entryFile
	if store == "" {
		return ef, errors.New("no store given; refusing to guess")
	}
	raw, err := os.ReadFile(entryPath(store, session, id))
	if err != nil {
		return ef, &NotFoundError{Msg: fmt.Sprintf("no such entry %q in session %q", id, session)}
	}
	if err := json.Unmarshal(raw, &ef); err != nil {
		return ef, fmt.Errorf("stored entry %q is corrupt: %v", id, err)
	}
	return ef, nil
}

// EntryText returns the friend's words byte-for-byte: the store never
// rewrites, grades or consolidates what was appended.
func EntryText(store, session, id string) (string, error) {
	ef, err := readEntry(store, session, id)
	if err != nil {
		return "", err
	}
	return ef.Text, nil
}

// Receipt names what was preserved for one entry: its stamp, pointers and
// size, with the persisted/published split repeated so a reader never has to
// infer the remote from the local.
func Receipt(store, session, id string) (ReceiptInfo, error) {
	var rc ReceiptInfo
	if err := existingStore(store); err != nil {
		return rc, err
	}
	if !validID(id) {
		return rc, fmt.Errorf("bad entry id %q: nonempty, no slashes, no whitespace", id)
	}
	sh, err := storeShape("receipt", store)
	if err != nil {
		return rc, err
	}
	path, bench, err := recordForRead(store, session, sh)
	if err != nil {
		return rc, err
	}
	if bench {
		rows, err := benchReceipts(path, session)
		if err != nil {
			return rc, err
		}
		for _, row := range rows {
			if row.ID == id {
				return row, nil
			}
		}
		return rc, &NotFoundError{Msg: fmt.Sprintf("no such entry %q in session %q", id, session)}
	}
	ef, err := readEntry(store, session, id)
	if err != nil {
		return rc, err
	}
	stamp, _ := time.Parse(time.RFC3339Nano, ef.Stamp)
	return ReceiptInfo{
		Session:   ef.Session,
		ID:        ef.ID,
		Stamp:     stamp,
		Source:    ef.Source,
		Bytes:     len(ef.Text),
		Policy:    ef.Publish,
		Persisted: true,
		Published: false,
	}, nil
}

// Index builds the bounded section/entry index mechanically from the stored
// entries: no narrative is recopied, work events are linked by
// session/entry pointers, and a stale *.tmp from an interrupted append is
// never a row. session "" indexes every record; max <= 0 lifts the ceiling
// and returns everything with no MORE standing for the rest.
func Index(store, session string, max int) ([]IndexRow, int, error) {
	if err := existingStore(store); err != nil {
		return nil, 0, err
	}
	sh, err := storeShape("index", store)
	if err != nil {
		return nil, 0, err
	}
	if session != "" {
		if _, _, err := recordForRead(store, session, sh); err != nil {
			return nil, 0, err
		}
	}
	var rows []IndexRow
	flat := map[string]bool{}
	if sh == shapeBench {
		if rows, flat, err = flatIndexRows(store, session); err != nil {
			return nil, 0, err
		}
	}
	root := filepath.Join(store, "entries")
	entries, err := os.ReadDir(root)
	if err != nil && !os.IsNotExist(err) {
		return nil, 0, err
	}
	for _, sess := range entries {
		if !sess.IsDir() || flat[sess.Name()] || (session != "" && sess.Name() != session) {
			continue
		}
		files, err := os.ReadDir(filepath.Join(root, sess.Name()))
		if err != nil {
			return nil, 0, err
		}
		for _, f := range files {
			name := f.Name()
			if f.IsDir() || !strings.HasSuffix(name, ".json") {
				continue // partials (*.tmp) and anything else are not rows
			}
			ef, err := readEntry(store, sess.Name(), strings.TrimSuffix(name, ".json"))
			if err != nil {
				return nil, 0, err
			}
			stamp, _ := time.Parse(time.RFC3339Nano, ef.Stamp)
			rows = append(rows, IndexRow{
				Session: ef.Session,
				ID:      ef.ID,
				Stamp:   stamp,
				Source:  ef.Source,
				Bytes:   len(ef.Text),
			})
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if !rows[i].Stamp.Equal(rows[j].Stamp) {
			return rows[i].Stamp.Before(rows[j].Stamp)
		}
		if rows[i].Session != rows[j].Session {
			return rows[i].Session < rows[j].Session
		}
		return rows[i].ID < rows[j].ID
	})
	total := len(rows)
	if max > 0 && len(rows) > max {
		rows = rows[:max]
	}
	return rows, total, nil
}

// Coverage derives the ledger from the store: session records and stored
// entries counted, never remembered, so the number cannot drift from the
// tree it reports on.
func Coverage(store string) Ledger {
	var led Ledger
	names := map[string]bool{}
	// Only the shape's own location is counted. A store that reads as mixed
	// is refused by every verb, so its count is never one a caller acts on.
	sh, _ := storeShape("index", store)
	dirs := []string{filepath.Join(store, "sessions")}
	if sh == shapeBench {
		dirs = []string{store}
	} else if sh == shapeMixed {
		dirs = append(dirs, store)
	}
	for _, dir := range dirs {
		if files, err := os.ReadDir(dir); err == nil {
			for _, f := range files {
				if !f.IsDir() && strings.HasSuffix(f.Name(), ".md") {
					id := strings.TrimSuffix(f.Name(), ".md")
					if sessionFileID(id) {
						names[id] = true
					}
				}
			}
		}
	}
	led.Sessions = len(names)
	if _, total, err := Index(store, "", 0); err == nil {
		led.Entries = total
	}
	return led
}
