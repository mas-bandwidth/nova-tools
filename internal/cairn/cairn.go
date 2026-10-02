// Package cairn is the store behind nova-cairn: session records holding a
// caller's exact words, each with a clock stamp, a stable identifier and a
// source pointer, and a bounded index and coverage count read back from them.
//
// It does nothing else. There is no seal, no consume, no delete, no grading,
// no consolidation and no liveness inference: those are separate choices a
// caller never gets by accident. The store is plain files under a directory
// the caller names, and a write is synced to disk before success is reported,
// with no Redis and no remote: local persistence is reported apart from remote
// publication, and neither implies the other.
//
// A store holds a session in one of two shapes, and both are read as they
// stand; a store is never converted to suit the tool.
//
// The nested shape is the one open creates:
//
//	sessions/<session>.md        the readable record; its header is convention only
//	entries/<session>/<id>.json  one file per entry, the source of truth
//	log.jsonl                    append-only event log: the open records and the appends
//
// The flat shape is one markdown file per session directly under the store,
// <store>/<session>.md, kept by hand or by another tool. The file is the
// record: an append lands a dated `## <stamp> — <entry>` section at its end,
// and no entries/ directory, log or index appears beside it. The nested record
// wins when a store holds both for one session.
//
// Each entry file is written atomically through internal/atomicfile (an
// exclusive temporary file beside the target, an explicit mode, fsync, then
// rename), so a retry after an interrupted append finishes the pointer line
// without duplicating the entry and without touching other writers' files. A
// stale temporary file is never indexed.
package cairn

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/mas-bandwidth/nova-tools/internal/atomicfile"
)

// Publish policies name who publishes a checkpoint and when. This package
// implements no transport: every append reports Published=false and the
// policy travels with the entry, so a later explicit act can carry it.
const (
	PublishNever     = "never"
	PublishManual    = "manual"
	PublishDeferred  = "deferred"
	PublishImmediate = "immediate"
	// PublishUnknown is what a flat record reports: its format stores no policy.
	PublishUnknown = "unknown"
)

// Policies is the publication policies a caller may choose, in the order help names them.
var Policies = []string{PublishNever, PublishManual, PublishDeferred, PublishImmediate}

// ValidPublish reports whether p is one of Policies.
func ValidPublish(p string) bool { return slices.Contains(Policies, p) }

// IDRule is what ValidID asks of a session or entry id, for a refusal to quote.
const IDRule = "an id names exactly one file or directory of that name inside the store, on every platform: " +
	"nonempty, at most 128 bytes, no whitespace or control characters, none of / \\ : * ? \" < > |, " +
	"not only dots, no `..`, no trailing dot, and not a Windows device name (CON, PRN, AUX, NUL, COM1-9, LPT1-9, with or without an extension)"

// idForbidden are the characters a path component cannot hold on some platform:
// the two separators, and the rest of what Windows refuses in a name (a colon
// there opens an alternate data stream of another file).
const idForbidden = `/\:*?"<>|`

// windowsDevices are the names Windows resolves to a device wherever they stand
// as a path component, whatever extension follows: entries/nul/ is no directory
// and con.md is no file.
var windowsDevices = []string{"CON", "PRN", "AUX", "NUL",
	"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
	"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9"}

// ValidID keeps an id one token on every output line and exactly one file or
// directory of that name inside the store: an id becomes a path component
// (sessions/<id>.md, <store>/<id>.md, entries/<id>/, entries/<s>/<id>.json),
// so every id that a path would read as something else is refused. "." would
// make entries/./ the entries directory itself, so an append lands where no
// index looks; ".." and any id holding it climb out; an id of only dots is one
// of those; a trailing dot or blank is stripped by Windows, folding "s." into
// "s"; a separator splits the id into two components; a device name or a
// Windows-forbidden character names no file at all there.
func ValidID(s string) bool {
	if s == "" || len(s) > 128 || strings.Contains(s, "..") || strings.Trim(s, ".") == "" ||
		strings.HasSuffix(s, ".") || strings.ContainsAny(s, idForbidden) {
		return false
	}
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	base, _, _ := strings.Cut(s, ".")
	return !slices.ContainsFunc(windowsDevices, func(d string) bool { return strings.EqualFold(d, base) })
}

// ConflictError is a write that must not silently win: the same entry id
// carrying different words, or a re-open naming another policy or source. It
// is the caller's failure (exit 1), not a crash. Remedy is the command to run next.
type ConflictError struct{ Msg, Remedy string }

func (e *ConflictError) Error() string { return e.Msg }

// NotFoundError is a use before open, or a read of an entry nothing stored.
// Remedy is the command to run next.
type NotFoundError struct{ Msg, Remedy string }

func (e *NotFoundError) Error() string { return e.Msg }

// AppendResult separates what this package guarantees from what it does not:
// Persisted is true once the words are fsync-durable on this machine (false
// only for a plan, PlanAppend, of words not yet stored); Published names the
// remote, which this package never touches.
type AppendResult struct {
	Stamp     time.Time // the stamp actually stored, including on a duplicate retry
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

// IndexRow is one row of the entry index.
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

// entryFile is the on-disk form of one entry: the caller's exact words plus
// the stamp, identifiers and pointers that make the receipt checkable.
type entryFile struct {
	Session string `json:"session"`
	ID      string `json:"id"`
	Stamp   string `json:"stamp"`
	Source  string `json:"source"`
	Publish string `json:"publish"`
	Text    string `json:"text"`
}

func sessionFile(store, session string) string {
	return filepath.Join(store, "sessions", session+".md")
}

// flatFile is the flat shape's record: one markdown file per session directly
// under the store. An append that looked only under sessions/ would refuse a
// record that is there, and its remedy, open, would write a second record
// beside it and split one session in two; so the store's own shape is read,
// nothing is migrated or renamed, and the file gets no sidecar.
func flatFile(store, session string) string {
	return filepath.Join(store, session+".md")
}

// locateRecord returns the session record's path and whether it is a flat
// file. The nested record wins when both exist, so a store the tool opened
// keeps its own shape and no caller is switched between two records by a file
// appearing beside the store.
func locateRecord(store, session string) (path string, flat, ok bool) {
	if name := sessionFile(store, session); fileExists(name) {
		return name, false, true
	}
	if name := flatFile(store, session); fileExists(name) {
		return name, true, true
	}
	return "", false, false
}

func fileExists(name string) bool {
	info, err := os.Stat(name)
	return err == nil && !info.IsDir()
}

// noRecord is the refusal for an append to a session nothing holds. It names
// the remedy verb whole, flags and all, so the reader runs it rather than
// rebuilding it from the usage text.
func noRecord(store, session, publish string) error {
	if !ValidPublish(publish) {
		publish = PublishManual
	}
	return &NotFoundError{Msg: fmt.Sprintf(
		"no such session %q under store %q; open first: %s",
		session, store, command("open", "--store", store, "--session", session, "--publish", publish))}
}

// flatHeadingRe reads the one heading this tool writes into a flat record:
// `## <rfc3339> — <entry>`. It is the section boundary too, which is why the
// form is machine-tight: a hand-written `## 21:55Z beat: …` heading in the same
// file is not a boundary, so prose may carry its own `##` headings without an
// append cutting the record in two.
var flatHeadingRe = regexp.MustCompile(`^## ([0-9]{4}-[0-9]{2}-[0-9]{2}T[^ ]+) — (\S+)$`)

// flatHeading is the dated section heading for one entry.
func flatHeading(id string, stamp time.Time) string {
	return fmt.Sprintf("## %s — %s", stamp.UTC().Format(time.RFC3339), id)
}

// flatSection is one dated section of a flat record: its heading's line
// number (from 1), stamp and id as written, and its body, which runs from the
// heading to the next heading of the machine form, or the end of the file,
// trimmed of surrounding whitespace.
type flatSection struct {
	line            int
	stamp, id, body string
}

// flatSections is the one parser of the flat format: every machine-form
// section, in file order, as written. The readers (flatReceipts) hold the
// sections to the format; an append (appendFlat) looks its id up in them.
func flatSections(raw []byte) []flatSection {
	lines := strings.Split(string(raw), "\n")
	var out []flatSection
	for i := 0; i < len(lines); {
		m := flatHeadingRe.FindStringSubmatch(lines[i])
		if m == nil {
			i++
			continue
		}
		end := i + 1
		for end < len(lines) && !flatHeadingRe.MatchString(lines[end]) {
			end++
		}
		out = append(out, flatSection{line: i + 1, stamp: m[1], id: m[2], body: strings.TrimSpace(strings.Join(lines[i+1:end], "\n"))})
		i = end
	}
	return out
}

// findFlat is the first section filed under id, and whether there is one.
func findFlat(raw []byte, id string) (flatSection, bool) {
	for _, s := range flatSections(raw) {
		if s.id == id {
			return s, true
		}
	}
	return flatSection{}, false
}

// conflict is the refusal for an entry id that already holds other words:
// the remedy reads what it holds.
func conflict(store, session, id string) error {
	return &ConflictError{
		Msg:    fmt.Sprintf("entry %q already holds different prose; append these words under a new --entry id, or read what it holds", id),
		Remedy: command("receipt", "--store", store, "--session", session, "--entry", id, "--text"),
	}
}

// appendFlat files one entry into a flat record: a dated section at the end of
// the file, in the file's own shape (one blank line between sections), the
// words under it. A retry with the same id and the same words adds nothing;
// the same id with different words is a conflict, as it is in the nested
// store. No index is written and no directory appears beside the file. The
// flat format stores no policy, so publish "" reports PublishUnknown. With
// write false nothing is written: the result is the plan.
func appendFlat(store, session, path, id, text string, now time.Time, publish string, write bool) (AppendResult, error) {
	var res AppendResult
	if publish == "" {
		publish = PublishUnknown
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return res, err
	}
	if prev, found := findFlat(raw, id); found {
		if prev.body != strings.TrimSpace(text) {
			return res, conflict(store, session, id)
		}
		stamp, err := time.Parse(time.RFC3339Nano, prev.stamp)
		if err != nil {
			return res, fmt.Errorf("stored entry %q has an invalid stamp: %w", id, err)
		}
		return AppendResult{Stamp: stamp, Persisted: true, Policy: publish, Duplicate: true}, nil
	}
	stamp := now.UTC().Truncate(time.Second)
	if !write {
		return AppendResult{Stamp: stamp, Policy: publish}, nil
	}
	var b strings.Builder
	if len(raw) > 0 && !strings.HasSuffix(string(raw), "\n") {
		b.WriteString("\n")
	}
	b.WriteString("\n" + flatHeading(id, now) + "\n\n" + strings.TrimRight(text, "\n") + "\n")
	if err := appendBytes(path, b.String()); err != nil {
		return res, err
	}
	return AppendResult{Stamp: stamp, Persisted: true, Policy: publish}, nil
}

// appendBytes adds content to an existing file and fsyncs before return, so a
// flat append is as durable as a nested one before success is reported.
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
	// ignored: a directory fsync is best effort where the platform does not support it; the rename already landed
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

// OpenRecord is what a session's open recorded in log.jsonl: its source and
// its publication policy. Found is false when the log holds no open record
// for the session (a flat store, or no log at all).
type OpenRecord struct {
	Source, Publish string
	Found           bool
}

// ReadOpen reads the session's open record from log.jsonl. The session file's
// header is convention only and is never parsed, so the log is where the
// pointer and the policy are read from.
//
// Corrupt provenance never reads as none. An absent log answers Found=false
// with no error, as does a log with no open record for the session. A log that
// exists and cannot be read is an error, and so is a line that may be this
// session's open record and does not decode as one: a line naming the session
// and the open event that is not valid JSON, or whose fields are not strings.
// Answering "none" for either would let an append file source=- over a
// pointer open recorded.
func ReadOpen(store, session string) (OpenRecord, error) {
	name := filepath.Join(store, "log.jsonl")
	raw, err := os.ReadFile(name)
	if os.IsNotExist(err) {
		return OpenRecord{}, nil
	}
	if err != nil {
		var pe *os.PathError
		if errors.As(err, &pe) {
			err = pe.Err
		}
		return OpenRecord{}, fmt.Errorf("cannot read the session's open record from %s: %v", name, err)
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
				return OpenRecord{}, fmt.Errorf("%s:%d may be the open record of session %q and does not decode as one; refusing to read its source as none", name, i+1, session)
			}
			continue
		}
		if rec["event"] == "open" && rec["session"] == session {
			return OpenRecord{Source: rec["source"], Publish: rec["publish"], Found: true}, nil
		}
	}
	return OpenRecord{}, nil
}

// Open starts one session record, or re-opens it: a re-open naming the
// recorded policy (and the recorded source, when it names one) changes
// nothing, and one naming another is a conflict. Concurrent records coexist:
// opening a second session never touches the first.
func Open(store, session, source string, now time.Time, publish string) error {
	_, err := open(store, session, source, now, publish, true)
	return err
}

// PlanOpen is Open with nothing written: every check Open makes and the same
// error, so a dry run refuses what the real run would. It returns the open
// record that would stand after the open (Found false for a flat record,
// which records none).
func PlanOpen(store, session, source string, now time.Time, publish string) (OpenRecord, error) {
	return open(store, session, source, now, publish, false)
}

func open(store, session, source string, now time.Time, publish string, write bool) (OpenRecord, error) {
	if store == "" {
		return OpenRecord{}, errors.New("no store given; refusing to guess")
	}
	if !ValidID(session) {
		return OpenRecord{}, fmt.Errorf("bad session id %q: %s", session, IDRule)
	}
	if !ValidPublish(publish) {
		return OpenRecord{}, fmt.Errorf("bad publish policy %q: %s", publish, strings.Join(Policies, "|"))
	}
	// The open record must be readable before anything is written or
	// reported: open prints its source, and append inherits it.
	rec, err := ReadOpen(store, session)
	if err != nil {
		return OpenRecord{}, err
	}
	// A re-open writes nothing: the record already stands, in whichever shape
	// the store keeps it. A flat file counts, or open would write a second
	// record beside one already being appended to.
	if _, flat, ok := locateRecord(store, session); ok {
		if flat || !rec.Found {
			return rec, nil
		}
		if rec.Publish != publish || (source != "" && source != rec.Source) {
			again := []string{"--store", store, "--session", session}
			if rec.Source != "" {
				again = append(again, "--source", rec.Source)
			}
			return rec, &ConflictError{
				Msg: fmt.Sprintf("session %q is already open with publish=%s source=%s; a re-open names the same, and another policy or source is a new session id",
					session, rec.Publish, cmpOr(rec.Source, "-")),
				Remedy: command("open", append(again, "--publish", rec.Publish)...),
			}
		}
		return rec, nil
	}
	planned := OpenRecord{Source: source, Publish: publish, Found: true}
	if !write {
		return planned, nil
	}
	name := sessionFile(store, session)
	stamp := now.UTC().Format(time.RFC3339Nano)
	header := fmt.Sprintf("# cairn %s\n\nOpened: %s\nSource: %s\nPublish: %s\n",
		session, stamp, source, publish)
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
		return OpenRecord{}, err
	}
	if err := atomicfile.WriteFile(name, []byte(header), 0o644); err != nil {
		return OpenRecord{}, err
	}
	return planned, appendLog(store, "open", session, "", stamp, publish, source)
}

func cmpOr(s, empty string) string {
	if s == "" {
		return empty
	}
	return s
}

// pointerLine is the one machine-scannable line an append adds to the
// readable record. The header above it is convention only and is never
// parsed, so records with different headings lose nothing.
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
		return noRecord(store, session, "")
	}
	want := pointerLine(id, stamp)
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "ENTRY "+id+" ") || line == want {
			return nil
		}
	}
	return appendLine(name, want)
}

// Append stores the caller's exact words under a stable entry id with a clock
// stamp and a source pointer. A retry of the same request succeeds with
// Duplicate=true and no second entry; the same id with other words is a
// conflict, never an overwrite. Offline use succeeds: the result carries
// Persisted=true with Published=false, because local durability never waits
// for a remote.
//
// An empty source carries the session's, and an empty publish the session's
// recorded policy: open named where the record points back to and who
// publishes it, and an append that names neither came from the same place
// under the same policy. Both are read before any write, so a log that cannot
// be read refuses with nothing written.
func Append(store, session, id, text, source string, now time.Time, publish string) (AppendResult, error) {
	return appendEntry(store, session, id, text, source, now, publish, true)
}

// PlanAppend is Append with nothing written: every check Append makes, the
// same error, and the result the write would report, with Persisted false
// unless the words are already stored (a duplicate).
func PlanAppend(store, session, id, text, source string, now time.Time, publish string) (AppendResult, error) {
	return appendEntry(store, session, id, text, source, now, publish, false)
}

func appendEntry(store, session, id, text, source string, now time.Time, publish string, write bool) (AppendResult, error) {
	var res AppendResult
	if store == "" {
		return res, errors.New("no store given; refusing to guess")
	}
	if !ValidID(session) {
		return res, fmt.Errorf("bad session id %q: %s", session, IDRule)
	}
	if !ValidID(id) {
		return res, fmt.Errorf("bad entry id %q: %s", id, IDRule)
	}
	if publish != "" && !ValidPublish(publish) {
		return res, fmt.Errorf("bad publish policy %q: %s", publish, strings.Join(Policies, "|"))
	}
	if text == "" {
		return res, errors.New("empty note stores nothing; refusing to file it")
	}
	path, flat, ok := locateRecord(store, session)
	if !ok {
		return res, noRecord(store, session, publish)
	}
	stamp := now.UTC()
	if flat {
		return appendFlat(store, session, path, id, text, stamp, publish, write)
	}
	if source == "" || publish == "" {
		rec, err := ReadOpen(store, session)
		if err != nil {
			return res, err
		}
		if source == "" {
			source = rec.Source
		}
		if publish == "" {
			if !ValidPublish(rec.Publish) {
				return res, fmt.Errorf("--publish is required: session %q has no open record naming a policy in %s; name one (%s)",
					session, filepath.Join(store, "log.jsonl"), strings.Join(Policies, "|"))
			}
			publish = rec.Publish
		}
	}
	final := entryPath(store, session, id)
	raw, err := os.ReadFile(final)
	if err != nil && !os.IsNotExist(err) {
		return res, err
	}
	if err == nil {
		var prev entryFile
		if err := json.Unmarshal(raw, &prev); err != nil {
			return res, fmt.Errorf("stored entry %q is corrupt: %v", id, err)
		}
		if prev.Text != text {
			return res, conflict(store, session, id)
		}
		prevStamp, err := time.Parse(time.RFC3339Nano, prev.Stamp)
		if err != nil {
			return res, fmt.Errorf("stored entry %q has an invalid stamp: %w", id, err)
		}
		if write {
			if err := ensurePointer(store, session, id, prevStamp); err != nil {
				return res, err
			}
		}
		return AppendResult{Stamp: prevStamp, Persisted: true, Policy: prev.Publish, Source: prev.Source, Duplicate: true}, nil
	}
	if !write {
		return AppendResult{Stamp: stamp, Policy: publish, Source: source}, nil
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
	if err := atomicfile.WriteFile(final, rec, 0o644); err != nil {
		return res, err
	}
	if err := ensurePointer(store, session, id, stamp); err != nil {
		return res, err
	}
	if err := appendLog(store, "append", session, id, stamp.Format(time.RFC3339Nano), publish, source); err != nil {
		return res, err
	}
	return AppendResult{Stamp: stamp, Persisted: true, Policy: publish, Source: source}, nil
}

// noEntry is the refusal for an entry id nothing stored: the remedy lists the
// session's entries.
func noEntry(store, session, id string) error {
	return &NotFoundError{
		Msg:    fmt.Sprintf("no such entry %q in session %q", id, session),
		Remedy: command("index", "--store", store, "--session", session),
	}
}

// readEntry loads one stored entry or explains its absence. It validates that
// the entry is valid JSON and that its stored stamp parses as RFC 3339 nano.
func readEntry(store, session, id string) (entryFile, time.Time, error) {
	var ef entryFile
	if store == "" {
		return ef, time.Time{}, errors.New("no store given; refusing to guess")
	}
	raw, err := os.ReadFile(entryPath(store, session, id))
	if err != nil {
		if os.IsNotExist(err) {
			return ef, time.Time{}, noEntry(store, session, id)
		}
		return ef, time.Time{}, err
	}
	if err := json.Unmarshal(raw, &ef); err != nil {
		return ef, time.Time{}, fmt.Errorf("stored entry %q is corrupt: %v", id, err)
	}
	stamp, err := time.Parse(time.RFC3339Nano, ef.Stamp)
	if err != nil {
		return ef, time.Time{}, fmt.Errorf("stored entry %q is corrupt: invalid stamp: %w", id, err)
	}
	return ef, stamp, nil
}

// EntryText returns the stored words of a nested entry byte for byte. A flat
// record returns the section body in the form its reader indexes.
func EntryText(store, session, id string) (string, error) {
	if store == "" {
		return "", errors.New("no store given; refusing to guess")
	}
	if !ValidID(id) {
		return "", fmt.Errorf("bad entry id %q: %s", id, IDRule)
	}
	path, flat, err := recordForRead(store, session)
	if err != nil {
		return "", err
	}
	if flat {
		raw, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("cannot read session %q: %w", session, err)
		}
		if _, err := flatReceipts(raw, session); err != nil {
			return "", err
		}
		s, found := findFlat(raw, id)
		if !found {
			return "", noEntry(store, session, id)
		}
		return s.body, nil
	}
	ef, _, err := readEntry(store, session, id)
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
	if !ValidID(id) {
		return rc, fmt.Errorf("bad entry id %q: %s", id, IDRule)
	}
	path, flat, err := recordForRead(store, session)
	if err != nil {
		return rc, err
	}
	if flat {
		raw, err := os.ReadFile(path)
		if err != nil {
			return rc, err
		}
		rows, err := flatReceipts(raw, session)
		if err != nil {
			return rc, err
		}
		for _, row := range rows {
			if row.ID == id {
				return row, nil
			}
		}
		return rc, noEntry(store, session, id)
	}
	ef, stamp, err := readEntry(store, session, id)
	if err != nil {
		return rc, err
	}
	return ReceiptInfo{
		Session:   ef.Session,
		ID:        ef.ID,
		Stamp:     stamp,
		Source:    ef.Source,
		Bytes:     len(ef.Text),
		Policy:    ef.Publish,
		Persisted: true,
	}, nil
}

// Index builds the entry index from the stored entries: no words are
// recopied, entries are linked by session and entry id, and a stale *.tmp
// from an interrupted append is never a row. session "" indexes every record;
// max <= 0 lifts the ceiling. It returns the rows kept and the total.
func Index(store, session string, max int) ([]IndexRow, int, error) {
	if err := existingStore(store); err != nil {
		return nil, 0, err
	}
	if session != "" {
		if _, _, err := recordForRead(store, session); err != nil {
			return nil, 0, err
		}
	}
	rows, flat, err := flatIndexRows(store, session)
	if err != nil {
		return nil, 0, err
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
			ef, stamp, err := readEntry(store, sess.Name(), strings.TrimSuffix(name, ".json"))
			if err != nil {
				return nil, 0, err
			}
			rows = append(rows, IndexRow{Session: ef.Session, ID: ef.ID, Stamp: stamp, Source: ef.Source, Bytes: len(ef.Text)})
		}
	}
	slices.SortFunc(rows, func(a, b IndexRow) int {
		if c := a.Stamp.Compare(b.Stamp); c != 0 {
			return c
		}
		if c := strings.Compare(a.Session, b.Session); c != 0 {
			return c
		}
		return strings.Compare(a.ID, b.ID)
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
	for _, dir := range []string{filepath.Join(store, "sessions"), store} {
		if files, err := os.ReadDir(dir); err == nil {
			for _, f := range files {
				if !f.IsDir() && strings.HasSuffix(f.Name(), ".md") {
					id := strings.TrimSuffix(f.Name(), ".md")
					if ValidID(id) {
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
