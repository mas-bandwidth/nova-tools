package main

import (
	"cmp"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// A store is a directory of plain files. open and append write the nested shape:
//
//	sessions/<session>.md        the readable record (header is convention only)
//	entries/<session>/<id>.json  one file per entry, the source of truth
//	log.jsonl                    append-only event log: the opens and the appends
//
// A flat record, <store>/<session>.md with a `## <rfc3339> — <entry>` section per
// entry (kept by hand), is read by index and receipt but never written. The
// nested record wins when both exist. The store's state machine is modelled in
// tla/CairnStore.tla.

var policies = []string{"never", "manual", "deferred", "immediate"}

const idRule = "an id names exactly one file or directory inside the store on every platform: nonempty, at most 128 bytes, " +
	"no whitespace or control characters, none of / \\ : * ? \" < > |, not only dots, no `..`, no trailing dot, " +
	"and not a Windows device name (CON, PRN, AUX, NUL, COM1-9, LPT1-9)"

var devices = regexp.MustCompile(`(?i)^(con|prn|aux|nul|com[1-9]|lpt[1-9])$`)

func validID(s string) bool {
	if s == "" || len(s) > 128 || strings.Contains(s, "..") || strings.Trim(s, ".") == "" ||
		strings.HasSuffix(s, ".") || strings.ContainsAny(s, `/\:*?"<>|`) {
		return false
	}
	for _, r := range s {
		if unicode.IsSpace(r) || unicode.IsControl(r) {
			return false
		}
	}
	base, _, _ := strings.Cut(s, ".")
	return !devices.MatchString(base)
}

// writableID is validID and valid UTF-8: an id written now goes into JSON, which
// holds only UTF-8. The read verbs take validID alone, so a store an older build
// wrote with other bytes in its ids still lists and reads.
func writableID(s string) bool { return validID(s) && utf8.ValidString(s) }

// refusal is a failed run: code 1 for a conflict (it ran and said no), 2 for
// anything that could not run. remedy is the command to run next ("" is help).
type refusal struct {
	msg, remedy string
	code        int
}

func (e *refusal) Error() string { return e.msg }

func refuse(remedy, format string, a ...any) error {
	return &refusal{msg: fmt.Sprintf(format, a...), remedy: remedy, code: 2}
}

func conflict(remedy, format string, a ...any) error {
	return &refusal{msg: fmt.Sprintf(format, a...), remedy: remedy, code: 1}
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_./=:@%+,-]+$`)

// command is a remedy line to paste: nova-cairn <verb> followed by flags and
// values, each quoted for a POSIX shell when it needs it.
func command(verb string, words ...string) string {
	return shell(append([]string{"nova-cairn", verb}, words...)...)
}

// shell joins words into one command line, each quoted for a POSIX shell when it needs it.
func shell(words ...string) string {
	out := make([]string, len(words))
	for i, w := range words {
		if !shellSafe.MatchString(w) {
			w = "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
		}
		out[i] = w
	}
	return strings.Join(out, " ")
}

// lineCmd is the remedy that shows line n of a file.
func lineCmd(name string, n int) string { return shell("sed", "-n", fmt.Sprintf("%dp", n), name) }

type entryFile struct {
	Session string `json:"session"`
	ID      string `json:"id"`
	Stamp   string `json:"stamp"`
	Source  string `json:"source"`
	Publish string `json:"publish"`
	Text    string `json:"text"`
}

// row is one entry as index and receipt print it.
func (ef entryFile) row(t time.Time) row {
	return row{ef.Session, ef.ID, ef.Source, ef.Publish, t, len(ef.Text)}
}

type openRec struct {
	source, publish string
	opened          time.Time
	found           bool
}

type row struct {
	session, id, source, policy string
	stamp                       time.Time
	bytes                       int
}

func stamp(t time.Time) string { return t.Format(time.RFC3339Nano) }

func sessionFile(store, s string) string   { return filepath.Join(store, "sessions", s+".md") }
func flatFile(store, s string) string      { return filepath.Join(store, s+".md") }
func entryPath(store, s, id string) string { return filepath.Join(store, "entries", s, id+".json") }
func logFile(store string) string          { return filepath.Join(store, "log.jsonl") }

// badUTF8 is the offset of the first byte of s that is not valid UTF-8, or -1.
func badUTF8(s string) int {
	for i := 0; i < len(s); {
		r, n := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && n == 1 {
			return i
		}
		i += n
	}
	return -1
}

// indexCmd is the remedy that lists what a store, or one session, holds.
func indexCmd(store, session string) string {
	if session == "" {
		return command("index", "--store", store)
	}
	return command("index", "--store", store, "--session", session)
}

func regular(name string) bool {
	info, err := os.Stat(name)
	return err == nil && info.Mode().IsRegular()
}

// locate returns the session's record and whether it is a flat file.
func locate(store, s string) (path string, flat, ok bool) {
	if p := sessionFile(store, s); regular(p) {
		return p, false, true
	}
	if p := flatFile(store, s); regular(p) {
		return p, true, true
	}
	return "", false, false
}

// writeNew writes data to name through a synced temporary file and an exclusive
// hard link, so an existing name is never replaced (os.ErrExist) and no reader
// sees half a file. A stale *.tmp left by a crash is never read as an entry.
func writeNew(name string, data []byte) error {
	if err := mkdir(filepath.Dir(name)); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(name), filepath.Base(name)+".*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(tmp.Name(), 0o644)
	}
	if err != nil {
		return err
	}
	if err := os.Link(tmp.Name(), name); err != nil {
		return err
	}
	return syncDir(filepath.Dir(name))
}

// mkdir makes dir and any missing parent, syncing the parent of each one it
// makes, so the new names are on disk too.
func mkdir(dir string) error {
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		return nil
	}
	parent := filepath.Dir(dir)
	if parent != dir {
		if err := mkdir(parent); err != nil {
			return err
		}
	}
	if err := os.Mkdir(dir, 0o755); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return syncDir(parent)
}

// syncDir syncs a directory, so a name just linked or created in it survives a
// crash. Windows has no directory sync, so there it does nothing.
func syncDir(dir string) error {
	if runtime.GOOS == "windows" {
		return nil
	}
	d, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	return err
}

// appendLine adds one line to a file, creating it, and syncs it and its
// directory before it returns.
func appendLine(name, line string) error {
	if err := mkdir(filepath.Dir(name)); err != nil {
		return err
	}
	f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	if _, err = f.WriteString(line + "\n"); err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return syncDir(filepath.Dir(name))
}

func appendLog(store, event, session, id, at, policy, source string) error {
	rec, _ := json.Marshal(map[string]string{"event": event, "session": session, "entry": id, "stamp": at, "publish": policy, "source": source})
	return appendLine(logFile(store), string(rec))
}

// readOpen reads the session's open record from log.jsonl. An unreadable log, or
// a line that may be this session's open and does not decode, is an error:
// corrupt provenance never reads as none.
func readOpen(store, session string) (openRec, error) {
	name := logFile(store)
	raw, err := os.ReadFile(name)
	if os.IsNotExist(err) {
		return openRec{}, nil
	}
	if err != nil {
		return openRec{}, refuse(shell("ls", "-l", name), "cannot read the session's open record from %s: %v; make the log readable", name, err)
	}
	quoted, _ := json.Marshal(session)
	for i, line := range strings.Split(string(raw), "\n") {
		var rec map[string]string
		if strings.TrimSpace(line) == "" {
			continue
		} else if json.Unmarshal([]byte(line), &rec) != nil {
			if strings.Contains(line, string(quoted)) && strings.Contains(line, `"open"`) {
				return openRec{}, refuse(lineCmd(name, i+1), "%s:%d may be the open record of session %q and does not decode as one; repair that line", name, i+1, session)
			}
		} else if rec["event"] == "open" && rec["session"] == session {
			var opened time.Time
			if rec["stamp"] != "" {
				if opened, err = time.Parse(time.RFC3339Nano, rec["stamp"]); err != nil {
					return openRec{}, refuse(lineCmd(name, i+1), "%s:%d open record of session %q has an invalid stamp %q; repair that line", name, i+1, session, rec["stamp"])
				}
			}
			return openRec{rec["source"], rec["publish"], opened, true}, nil
		}
	}
	return openRec{}, nil
}

// open starts a session record, or re-opens it: a re-open naming the recorded
// policy (and source, when it names one) changes nothing; another is a conflict.
// It returns the session's open record and whether one was there before.
func open(store, session, source string, now time.Time, publish string) (openRec, bool, error) {
	rec, err := readOpen(store, session)
	if err != nil {
		return rec, false, err
	}
	before := rec.found
	_, flat, ok := locate(store, session)
	switch {
	case flat:
		return rec, before, nil
	case !ok || !rec.found:
		logSource, logPublish := source, cmp.Or(publish, "never")
		if !ok {
			header := fmt.Sprintf("# cairn %s\n\nOpened: %s\nSource: %s\nPublish: %s\n", session, stamp(now), source, logPublish)
			if err := writeNew(sessionFile(store, session), []byte(header)); err != nil && !errors.Is(err, os.ErrExist) {
				return rec, false, err
			}
		} else if hs, hp := headerPolicy(store, session); slices.Contains(policies, hp) {
			// An open interrupted before its log line: log what its header recorded.
			logSource, logPublish = hs, hp
		}
		if err := appendLog(store, "open", session, "", stamp(now), logPublish, logSource); err != nil {
			return rec, false, err
		}
		if rec, err = readOpen(store, session); err != nil {
			return rec, false, err
		}
	}
	if (publish != "" && rec.publish != publish) || (source != "" && source != rec.source) {
		again := []string{"--store", store, "--session", session}
		if rec.source != "" {
			again = append(again, "--source", rec.source)
		}
		why := "a re-open names the same, and another policy or source is a new session id"
		if rec.publish != publish {
			why = fmt.Sprintf("--publish %s names another, and a re-open names the same", publish)
		}
		return rec, before, conflict(command("open", append(again, "--publish", rec.publish)...),
			"session %q is already open with publish=%s source=%s; %s", session, rec.publish, cmp.Or(rec.source, "-"), why)
	}
	return rec, before, nil
}

// healPolicy is the policy an open heal would log: the header's, else manual.
func healPolicy(store, session string) string {
	if _, p := headerPolicy(store, session); slices.Contains(policies, p) {
		return p
	}
	return "manual"
}

// headerPolicy reads the Source: and Publish: lines of a session's header. It is
// read only to heal an open interrupted before its log line.
func headerPolicy(store, session string) (source, publish string) {
	raw, _ := os.ReadFile(sessionFile(store, session))
	for _, l := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(l, "Source:"); ok && source == "" {
			source = strings.TrimSpace(v)
		} else if v, ok := strings.CutPrefix(l, "Publish:"); ok && publish == "" {
			publish = strings.TrimSpace(v)
		}
	}
	return source, publish
}

var flatHeading = regexp.MustCompile(`^## ([0-9]{4}-[0-9]{2}-[0-9]{2}T[^ ]+) — (\S+)$`)

// flatRows reads a flat record: one row per dated section, plus the section bodies.
func flatRows(path, session string) ([]row, []string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var rows []row
	var bodies []string
	seen := map[string]bool{}
	lines := strings.Split(string(raw), "\n")
	for i := 0; i < len(lines); i++ {
		m := flatHeading.FindStringSubmatch(lines[i])
		if m == nil {
			continue
		}
		end := i + 1
		for end < len(lines) && !flatHeading.MatchString(lines[end]) {
			end++
		}
		t, err := time.Parse(time.RFC3339Nano, m[1])
		if err != nil || !validID(m[2]) {
			return nil, nil, refuse(lineCmd(path, i+1), "invalid entry heading in session %q at line %d; a heading is `## <rfc3339> — <entry id>`", session, i+1)
		}
		if seen[m[2]] {
			return nil, nil, refuse(shell("grep", "-n", " — "+m[2]+"$", path), "duplicate entry %q in session %q; rename one of the two headings", m[2], session)
		}
		seen[m[2]] = true
		body := strings.TrimSpace(strings.Join(lines[i+1:end], "\n"))
		rows, bodies = append(rows, row{session: session, id: m[2], policy: "unknown", stamp: t, bytes: len(body)}), append(bodies, body)
		i = end - 1
	}
	return rows, bodies, nil
}

// appendEntry stores the exact words under a stable id: a retry of the same words
// is a duplicate, other words under the same id a conflict, never an overwrite.
func appendEntry(store, session, id, text, source string, now time.Time, publish string) (r row, dup, created, first bool, err error) {
	if text == "" {
		return r, false, false, false, refuse("", "empty words store nothing; give the words to keep with --text or --file")
	}
	_, flat, ok := locate(store, session)
	if !ok {
		if info, serr := os.Stat(store); serr != nil || !info.IsDir() {
			return r, false, false, false, refuse(command("open", "--store", store, "--session", session, "--publish", cmp.Or(publish, "manual")), "store %q does not exist; open it first, or fix --store", store)
		}
		if info, serr := os.Stat(filepath.Join(store, "sessions")); !regular(logFile(store)) && (serr != nil || !info.IsDir()) {
			return r, false, false, false, refuse(command("open", "--store", store, "--session", session, "--publish", cmp.Or(publish, "manual")), "directory %q holds no store; open it first, or fix --store", store)
		}
		if !slices.Contains(policies, publish) {
			publish = "manual"
		}
		if _, _, err := open(store, session, source, now, publish); err != nil {
			return r, false, false, false, err
		}
		created = true
	} else if flat {
		return r, false, false, false, refuse("", "session %q is a flat record, which this tool reads but does not append to; append to it by hand, or open another session id", session)
	}
	opened, err := readOpen(store, session)
	if err != nil {
		return r, false, created, false, err
	}
	named := source
	source = cmp.Or(source, opened.source)
	if publish == "" {
		if !opened.found {
			publish = healPolicy(store, session)
			if rec, _, err := open(store, session, source, now, publish); err != nil {
				return r, false, created, false, err
			} else {
				opened = rec
			}
			source = cmp.Or(named, opened.source)
		}
		if !slices.Contains(policies, opened.publish) {
			return r, false, created, false, refuse(command("open", "--store", store, "--session", session, "--publish", healPolicy(store, session)),
				"--publish is required: session %q has no open record naming a policy in %s; open it to log one", session, logFile(store))
		}
		publish = opened.publish
	} else if opened.found && slices.Contains(policies, opened.publish) && publish != opened.publish {
		return r, false, created, false, conflict(command("append", "--store", store, "--session", session, "--entry", id, "--publish", opened.publish),
			"session %q holds publish=%s; --publish %s names another", session, opened.publish, publish)
	}
	entries, rerr := os.ReadDir(filepath.Join(store, "entries", session))
	if rerr != nil && !os.IsNotExist(rerr) {
		return r, false, created, false, rerr
	}
	first = len(entries) == 0
	final := entryPath(store, session, id)
	kept := func(err error) error {
		if err == nil {
			return nil
		}
		return refuse(shell("ls", "-l", logFile(store)), "entry %q is kept, but its log line is not written: %v; make the log writable, then append the same words again to write it", id, err)
	}
	for try := 0; ; try++ {
		if _, err := os.Stat(final); err == nil {
			prev, t, err := readEntry(store, session, id)
			if err != nil {
				return r, false, created, false, err
			}
			if prev.Text != text {
				return r, false, created, false, conflict(command("receipt", "--store", store, "--session", session, "--entry", id, "--text"),
					"entry %q already holds different words; append these under a new --entry id, or read what it holds", id)
			}
			if named != "" && named != prev.Source { // a retry's --source is never ignored
				return r, false, created, false, conflict(command("receipt", "--store", store, "--session", session, "--entry", id),
					"entry %q already holds these words with source=%s; --source %s names another", id, cmp.Or(prev.Source, "-"), named)
			}
			if err := ensurePointer(store, session, id, t); err != nil {
				return r, false, created, false, err
			}
			return prev.row(t), true, created, false, kept(ensureLogged(store, session, id, prev))
		} else if !os.IsNotExist(err) {
			return r, false, created, false, err
		}
		rec, _ := json.Marshal(entryFile{session, id, stamp(now), source, publish, text})
		if err := writeNew(final, rec); errors.Is(err, os.ErrExist) && try == 0 {
			continue // lost a race to another writer: read the winner
		} else if err != nil {
			return r, false, created, false, err
		}
		break
	}
	if err := ensurePointer(store, session, id, now); err != nil {
		return r, false, created, false, err
	}
	return row{session, id, source, publish, now, len(text)}, false, created, first, kept(appendLog(store, "append", session, id, stamp(now), publish, source))
}

// ensureLogged lands the entry's append line in the log, once (it heals an append whose log write failed).
func ensureLogged(store, session, id string, ef entryFile) error {
	raw, err := os.ReadFile(logFile(store))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		var rec map[string]string
		if json.Unmarshal([]byte(line), &rec) == nil && rec["event"] == "append" && rec["session"] == session && rec["entry"] == id {
			return nil
		}
	}
	return appendLog(store, "append", session, id, ef.Stamp, ef.Publish, ef.Source)
}

// ensurePointer lands the ENTRY line in the readable record, once (it heals an interrupted append).
func ensurePointer(store, session, id string, t time.Time) error {
	name := sessionFile(store, session)
	raw, err := os.ReadFile(name)
	if err != nil {
		return err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "ENTRY "+id+" ") {
			return nil
		}
	}
	return appendLine(name, "ENTRY "+id+" "+stamp(t))
}

func readEntry(store, session, id string) (entryFile, time.Time, error) {
	var ef entryFile
	name := entryPath(store, session, id)
	raw, err := os.ReadFile(name)
	if os.IsNotExist(err) {
		return ef, time.Time{}, refuse(indexCmd(store, session), "no such entry %q in session %q", id, session)
	}
	if err == nil {
		err = json.Unmarshal(raw, &ef)
	}
	var t time.Time
	if err == nil {
		t, err = time.Parse(time.RFC3339Nano, ef.Stamp)
	}
	if err != nil {
		return ef, t, refuse(shell("cat", name), "stored entry %q is corrupt: %v; repair or move %s", id, err, name)
	}
	return ef, t, nil
}

// readRecord finds the session for a read verb.
func readRecord(store, session string) (string, bool, error) {
	if p, flat, ok := locate(store, session); ok {
		return p, flat, nil
	}
	return "", false, refuse(indexCmd(store, ""), "no such session %q under store %q", session, store)
}

// receipt reads one entry back: its row and its words.
func receipt(store, session, id string) (row, string, error) {
	path, flat, err := readRecord(store, session)
	if err != nil {
		return row{}, "", err
	}
	if !flat {
		ef, t, err := readEntry(store, session, id)
		r := ef.row(t)
		r.session, r.id = session, id // the file names, as index prints them
		return r, ef.Text, err
	}
	rows, bodies, err := flatRows(path, session)
	if err != nil {
		return row{}, "", err
	}
	for i, r := range rows {
		if r.id == id {
			return r, bodies[i], nil
		}
	}
	return row{}, "", refuse(indexCmd(store, session), "no such entry %q in session %q", id, session)
}

// index returns every entry row, oldest first, and every session id.
func index(store, session string) ([]row, []string, error) {
	if session != "" {
		if _, _, err := readRecord(store, session); err != nil {
			return nil, nil, err
		}
	}
	names, flat := map[string]bool{}, map[string]bool{}
	var rows []row
	for _, dir := range []string{filepath.Join(store, "sessions"), store} {
		files, err := os.ReadDir(dir)
		if err != nil && !os.IsNotExist(err) {
			return nil, nil, refuse(shell("ls", "-ld", dir), "cannot read %s: %v; make it readable", dir, err)
		}
		for _, f := range files {
			id, ok := strings.CutSuffix(f.Name(), ".md")
			if !ok || f.IsDir() || !validID(id) || (session != "" && id != session) {
				continue
			}
			names[id] = true
			if dir == store && !regular(sessionFile(store, id)) {
				fr, _, err := flatRows(flatFile(store, id), id)
				if err != nil {
					return nil, nil, err
				}
				flat[id] = true
				rows = append(rows, fr...)
			}
		}
	}
	root := filepath.Join(store, "entries")
	dirs, err := os.ReadDir(root)
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, err
	}
	for _, d := range dirs {
		if !d.IsDir() || flat[d.Name()] || (session != "" && d.Name() != session) {
			continue
		}
		files, err := os.ReadDir(filepath.Join(root, d.Name()))
		if err != nil {
			return nil, nil, err
		}
		for _, f := range files {
			if id, ok := strings.CutSuffix(f.Name(), ".json"); ok && !f.IsDir() { // a stale *.tmp is not a row
				ef, t, err := readEntry(store, d.Name(), id)
				if err != nil {
					return nil, nil, err
				}
				r := ef.row(t)
				r.session, r.id = d.Name(), id // the names receipt takes, also for ids an older build wrote that are not UTF-8
				rows = append(rows, r)
			}
		}
	}
	slices.SortFunc(rows, func(a, b row) int {
		return cmp.Or(a.stamp.Compare(b.stamp), strings.Compare(a.session, b.session), strings.Compare(a.id, b.id))
	})
	return rows, slices.Sorted(maps.Keys(names)), nil
}

// runStore runs open, append, index and receipt: one line per fact group on
// stdout; a refusal (exit 2) or a conflict (exit 1) is one line on stderr.
func runStore(verb string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	// Each verb takes only its own flags: any other is refused as unknown.
	fs := flags()
	store, session, entry, text, file, source, publish := new(string), new(string), new(string), new(string), new(string), new(string), new(string)
	wantText, maxN := new(bool), new(int)
	*maxN = 20
	fs.StringVar(store, "store", "", "the store directory")
	fs.StringVar(session, "session", "", "the stable session id")
	if verb == "append" || verb == "receipt" {
		fs.StringVar(entry, "entry", "", "the stable entry id")
	}
	if verb == "open" || verb == "append" {
		fs.StringVar(source, "source", "", "a pointer to where the words came from")
		fs.StringVar(publish, "publish", "", "the publication policy: "+strings.Join(policies, ", "))
	}
	switch verb {
	case "append":
		fs.StringVar(text, "text", "", "the exact words")
		fs.StringVar(file, "file", "", "a file holding the exact words; - reads stdin")
	case "receipt":
		fs.BoolVar(wantText, "text", false, "true or false")
	case "index":
		fs.IntVar(maxN, "max", 20, "a number of lines per kind: 0 lists all")
	}
	if code := parse(fs, verb, args, stdout, stderr); code >= 0 {
		return code
	}
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })

	// Every problem with the line is named at once.
	var problems []string
	for _, want := range []struct {
		name, v string
		ok      bool
	}{{"store", *store, true}, {"session", *session, verb != "index"}, {"entry", *entry, verb == "append" || verb == "receipt"}} {
		if want.ok && want.v == "" {
			problems = append(problems, "--"+want.name+" is required")
		}
	}
	for _, id := range []struct{ name, v string }{{"session", *session}, {"entry", *entry}} {
		if id.v != "" && !validID(id.v) {
			problems = append(problems, fmt.Sprintf("--%s %s is not an id: %s", id.name, strconv.Quote(id.v), idRule))
		} else if id.v != "" && (verb == "open" || verb == "append") && !writableID(id.v) {
			problems = append(problems, fmt.Sprintf("--%s %s is not valid UTF-8 at byte %d: an id written now must be valid UTF-8", id.name, strconv.Quote(id.v), badUTF8(id.v)))
		}
	}
	if at := badUTF8(*source); at >= 0 {
		problems = append(problems, fmt.Sprintf("--source is not valid UTF-8 at byte %d", at))
	}
	if (*publish != "" || given["publish"]) && !slices.Contains(policies, *publish) {
		problems = append(problems, fmt.Sprintf("--publish %s is not a policy; it is one of %s", strconv.Quote(*publish), strings.Join(policies, ", ")))
	}
	if verb == "append" && given["text"] == given["file"] {
		problems = append(problems, "the words come from exactly one of --text or --file")
	}
	if *maxN < 0 || fs.NArg() > 0 {
		problems = append(problems, "--max must be zero or more, and no argument stands outside a flag")
	}
	// fail prints a refusal (exit 2) or a conflict, a FAILED event (exit 1), as one line on stderr.
	fail := func(err error) int {
		rf, ok := err.(*refusal)
		if !ok { // the file system said no: look at the store, or its parent when there is none
			look := *store
			if _, serr := os.Stat(look); serr != nil {
				look = filepath.Dir(look)
			}
			rf = &refusal{msg: err.Error(), remedy: shell("ls", "-ld", look), code: 2}
		}
		if rf.code == 2 {
			return refused(stderr, verb, rf.msg, rf.remedy)
		}
		head := strings.ToUpper(verb) + " FAILED"
		if verb == "append" {
			head += fmt.Sprintf(" session=%s entry=%s", field(*session), field(*entry))
		}
		say(stderr, head, rf.msg, rf.remedy)
		return 1
	}
	if len(problems) > 0 {
		return fail(refuse("", "%s", strings.Join(problems, "; ")))
	}
	if info, err := os.Stat(*store); (verb == "index" || verb == "receipt") && (err != nil || !info.IsDir()) {
		return fail(refuse(command("open", "--store", *store, "--session", cmp.Or(*session, "first"), "--publish", "manual"), "--store %q is not a directory I can open; name the directory open wrote", *store))
	}

	now := time.Now().UTC()
	switch verb {
	case "open":
		rec, reopened, err := open(*store, *session, *source, now, *publish)
		if err != nil {
			return fail(err)
		}
		kv := []any{"session", *session, "store", *store, "source", rec.source, "publish", rec.publish}
		if reopened {
			kv = append(kv, "reopened", true)
			if !rec.opened.IsZero() {
				now = rec.opened
			}
		}
		line(stdout, "OPEN OK", append(kv, "stamp", stamp(now))...)
		fmt.Fprintln(stdout, "next: "+command("append", "--store", *store, "--session", *session, "--entry", "first", "--text", "the words to keep"))
	case "append":
		words := []byte(*text)
		if given["file"] {
			b, err := input(*file, stdin)
			if err != nil {
				next := shell("ls", "-l", *file)
				if *file == "-" {
					next = "nova-cairn append -h"
				}
				return fail(refuse(next, "cannot read --file %s: %v", *file, err))
			}
			words = b
		}
		// The entry file is JSON, which can hold only UTF-8: anything else would not
		// be kept byte for byte, so it is refused before anything is written.
		if at := badUTF8(string(words)); at >= 0 {
			next := shell("file", *file)
			if !given["file"] || *file == "-" {
				next = "nova-cairn append -h"
			}
			return fail(refuse(next, "text is not valid UTF-8 at byte %d; convert it first (iconv -f <its encoding> -t UTF-8)", at))
		}
		r, dup, created, first, err := appendEntry(*store, *session, *entry, string(words), *source, now, *publish)
		if err != nil {
			return fail(err)
		}
		kv := []any{"session", *session, "entry", *entry, "source", r.source, "persisted", true, "published", false, "publish", r.policy, "duplicate", dup, "stamp", stamp(r.stamp), "opened", created}
		line(stdout, "APPEND OK", kv...)
		if first {
			fmt.Fprintln(stdout, "next: "+command("index", "--store", *store))
		}
	case "receipt":
		r, words, err := receipt(*store, *session, *entry)
		if err != nil {
			return fail(err)
		}
		kv := []any{"session", r.session, "entry", r.id, "stamp", stamp(r.stamp), "bytes", r.bytes, "source", r.source,
			"persisted", true, "published", false, "publish", r.policy}
		if *wantText {
			kv = append(kv, "text", raw(strconv.Quote(words)))
		}
		line(stdout, "RECEIPT OK", kv...)
	case "index":
		rows, names, err := index(*store, *session)
		if err != nil {
			return fail(err)
		}
		per := map[string]int{}
		for _, r := range rows {
			per[r.session]++
		}
		opens := make([]openRec, len(names))
		for i, s := range names[:min(len(names), cmp.Or(*maxN, len(names)))] {
			if opens[i], err = readOpen(*store, s); err != nil {
				return fail(err) // as append refuses it: corrupt provenance never reads as unknown
			}
		}
		line(stdout, "INDEX OK", "sessions", len(names), "entries", len(rows))
		for i, s := range names {
			if *maxN > 0 && i >= *maxN {
				more(stdout, "INDEX", "session", *maxN, len(names), "--max")
				break
			}
			o := opens[i]
			policy, opened := "unknown", "-"
			if o.found && slices.Contains(policies, o.publish) {
				policy = o.publish
				if !o.opened.IsZero() {
					opened = stamp(o.opened)
				}
			}
			line(stdout, "INDEX SESSION", "session", s, "publish", policy, "opened", opened, "entries", per[s])
		}
		for i, r := range rows {
			if *maxN > 0 && i >= *maxN {
				more(stdout, "INDEX", "entry", *maxN, len(rows), "--max")
				break
			}
			line(stdout, "INDEX ENTRY", "session", r.session, "entry", r.id, "stamp", stamp(r.stamp), "bytes", r.bytes, "source", r.source)
		}
	}
	return 0
}
