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
// <session>.md, the shape a friend appending by hand already has -- is read as
// it stands. `open` on such a record is a no-op and `append` lands a dated
// `## <stamp> — <entry>` section at the end of the file, with no entries/
// directory, no log and no index appearing beside it. The tool adapts to the
// store; the store is never converted to suit the tool.
//
// Each entry file is written atomically (fixed temp name per entry, fsync,
// rename, dir fsync), so a retry after an interrupted append finishes the
// pointer without duplicating the entry and without touching other writers'
// files. A stale *.tmp is never indexed and is overwritten by the retry.
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
	Persisted bool
	Published bool
	Policy    string
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

func sessionFile(store, session string) string {
	return filepath.Join(store, "sessions", session+".md")
}

// benchFile is the other store shape this tool reads: one markdown file per
// session directly under the store, kept and appended by hand. Rowan's bench
// has kept its cairns that way since before the tool existed
// (`cairns/<session>.md`), and on 2026-09-18 an append into it refused with
// `no such session; open first` while the record sat right there. The refusal
// was false, and its remedy was worse than the defect: `open` would have
// written a second record under sessions/ and split one session in two.
//
// So the store's own shape is READ rather than imposed. Nothing is migrated,
// nothing is renamed, and a bench file gets no sidecar: the file IS the
// record, which is the same promise SPEC-CAIRN already makes about headers.
func benchFile(store, session string) string {
	return filepath.Join(store, session+".md")
}

// locateRecord returns the session record's path and whether it is a bench
// file. The nested record wins when both exist, so a store the tool opened
// keeps its own shape and no caller is switched between two records by a file
// appearing beside the store.
func locateRecord(store, session string) (path string, bench, ok bool) {
	if name := sessionFile(store, session); fileExists(name) {
		return name, false, true
	}
	if name := benchFile(store, session); fileExists(name) {
		return name, true, true
	}
	return "", false, false
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
		"no such session %q under store %q; open first: nova-cairn open --store %s --session %s --publish %s",
		session, store, store, session, publish)}
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
// file, and whether the entry is there at all. The body runs from the heading
// to the next heading of the same machine form, or to the end of the file.
func benchSection(raw []byte, id string) (string, bool) {
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
		return strings.TrimSpace(strings.Join(lines[i+1:end], "\n")), true
	}
	return "", false
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
	if prev, found := benchSection(raw, id); found {
		if prev != strings.TrimSpace(text) {
			return res, &ConflictError{Msg: fmt.Sprintf("entry %q already holds different prose; pick a new id", id)}
		}
		return AppendResult{Persisted: true, Published: false, Policy: publish, Duplicate: true}, nil
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
	return AppendResult{Persisted: true, Published: false, Policy: publish}, nil
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

// writeAtomic lands content at final via a fixed per-entry temp name, so a
// retry after a crash overwrites the partial instead of orphaning it, and
// other writers' files are never touched. The file and its directory are
// fsynced before success, which is what makes persisted=true honest.
func writeAtomic(final string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
		return err
	}
	tmp := final + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write(content); err != nil {
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
	if err := os.Rename(tmp, final); err != nil {
		return err
	}
	return fsyncDir(filepath.Dir(final))
}

// fsyncDir is best-effort: where the platform cannot sync a directory the
// file sync above still holds the content, and the retry stays idempotent.
func fsyncDir(dir string) error {
	d, err := os.Open(dir)
	if err != nil {
		return nil
	}
	defer d.Close()
	_ = d.Sync()
	return nil
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

// appendLog records one event on the store's append-only log.
func appendLog(store, event, session, id, stamp, policy string) error {
	rec, _ := json.Marshal(map[string]string{
		"event":   event,
		"session": session,
		"entry":   id,
		"stamp":   stamp,
		"publish": policy,
	})
	return appendLine(filepath.Join(store, "log.jsonl"), string(rec))
}

// Open starts (or re-starts, idempotently) one session record. Concurrent
// records coexist: opening a second session never touches the first.
func Open(store, session, source string, now time.Time, publish string) error {
	if store == "" {
		return errors.New("no store given; refusing to guess")
	}
	if !validID(session) {
		return fmt.Errorf("bad session id %q: nonempty, no slashes, no whitespace", session)
	}
	if !validPublish(publish) {
		return fmt.Errorf("bad publish policy %q: never|manual|deferred|immediate", publish)
	}
	// Re-open is a no-op: the record already stands, in whichever shape the
	// store keeps it. A bench file counts, or open would write a second
	// record beside one already being appended to.
	if _, _, ok := locateRecord(store, session); ok {
		return nil
	}
	name := sessionFile(store, session)
	stamp := now.UTC().Format(time.RFC3339Nano)
	header := fmt.Sprintf("# cairn %s\n\nOpened: %s\nSource: %s\nPublish: %s\n",
		session, stamp, source, publish)
	if err := writeAtomic(name, []byte(header)); err != nil {
		return err
	}
	return appendLog(store, "open", session, "", stamp, publish)
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
	if !validID(session) {
		return res, fmt.Errorf("bad session id %q: nonempty, no slashes, no whitespace", session)
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
	path, bench, ok := locateRecord(store, session)
	if !ok {
		return res, noRecord(store, session, publish)
	}
	stamp := now.UTC()
	if bench {
		return appendBench(path, id, text, stamp, publish)
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
		prevStamp, _ := time.Parse(time.RFC3339Nano, prev.Stamp)
		if err := ensurePointer(store, session, id, prevStamp); err != nil {
			return res, err
		}
		return AppendResult{Persisted: true, Published: false, Policy: prev.Publish, Duplicate: true}, nil
	}
	rec, _ := json.Marshal(entryFile{
		Session: session,
		ID:      id,
		Stamp:   stamp.Format(time.RFC3339Nano),
		Source:  source,
		Publish: publish,
		Text:    text,
	})
	if err := writeAtomic(final, rec); err != nil {
		return res, err
	}
	if err := ensurePointer(store, session, id, stamp); err != nil {
		return res, err
	}
	if err := appendLog(store, "append", session, id, stamp.Format(time.RFC3339Nano), publish); err != nil {
		return res, err
	}
	return AppendResult{Persisted: true, Published: false, Policy: publish}, nil
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
	if store == "" {
		return nil, 0, errors.New("no store given; refusing to guess")
	}
	root := filepath.Join(store, "entries")
	var rows []IndexRow
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, 0, nil
		}
		return nil, 0, err
	}
	for _, sess := range entries {
		if !sess.IsDir() || (session != "" && sess.Name() != session) {
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
	sessions, err := os.ReadDir(filepath.Join(store, "sessions"))
	if err == nil {
		for _, f := range sessions {
			if !f.IsDir() && strings.HasSuffix(f.Name(), ".md") {
				led.Sessions++
			}
		}
	}
	// A bench store keeps its records as <store>/<session>.md. They are
	// records the append verb writes into, so the ledger counts them: a
	// coverage line reading sessions=0 over a store this tool can append to
	// is the same false answer the refusal gave.
	if top, err := os.ReadDir(store); err == nil {
		for _, f := range top {
			if !f.IsDir() && strings.HasSuffix(f.Name(), ".md") {
				led.Sessions++
			}
		}
	}
	entries, err := os.ReadDir(filepath.Join(store, "entries"))
	if err == nil {
		for _, sess := range entries {
			if !sess.IsDir() {
				continue
			}
			files, err := os.ReadDir(filepath.Join(store, "entries", sess.Name()))
			if err != nil {
				continue
			}
			for _, f := range files {
				if !f.IsDir() && strings.HasSuffix(f.Name(), ".json") {
					led.Entries++
				}
			}
		}
	}
	return led
}
