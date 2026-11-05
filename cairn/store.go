package main

import (
	"cmp"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
)

// A store is a directory of plain files. open and append write the nested shape:
//
//	sessions/<session>.md        the readable record (header is convention only)
//	entries/<session>/<id>.json  one file per entry, the source of truth
//	log.jsonl                    append-only event log: the opens and the appends
//
// A flat record, <store>/<session>.md with a `## <rfc3339> — <entry>` section per
// entry (kept by hand), is read by index and receipt but never written. The
// nested record wins when both exist.

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

// refusal is a failed run: code 1 for a conflict (it ran and said no), 2 for
// anything that could not run. remedy is the command to run next.
type refusal struct {
	msg, remedy string
	code        int
}

func (e *refusal) Error() string { return e.msg }

func refuse(format string, a ...any) error { return &refusal{msg: fmt.Sprintf(format, a...), code: 2} }

func conflict(remedy, format string, a ...any) error {
	return &refusal{msg: fmt.Sprintf(format, a...), remedy: remedy, code: 1}
}

func missing(remedy, format string, a ...any) error {
	return &refusal{msg: fmt.Sprintf(format, a...), remedy: remedy, code: 2}
}

var shellSafe = regexp.MustCompile(`^[A-Za-z0-9_./=:@%+,-]+$`)

// command is a remedy line to paste: nova-cairn <verb> followed by flags and values.
func command(verb string, words ...string) string {
	out := []string{"nova-cairn", verb}
	for _, w := range words {
		if !shellSafe.MatchString(w) {
			w = "'" + strings.ReplaceAll(w, "'", `'\''`) + "'"
		}
		out = append(out, w)
	}
	return strings.Join(out, " ")
}

type entryFile struct {
	Session string `json:"session"`
	ID      string `json:"id"`
	Stamp   string `json:"stamp"`
	Source  string `json:"source"`
	Publish string `json:"publish"`
	Text    string `json:"text"`
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

func sessionFile(store, s string) string { return filepath.Join(store, "sessions", s+".md") }
func flatFile(store, s string) string    { return filepath.Join(store, s+".md") }
func entryPath(store, s, id string) string {
	return filepath.Join(store, "entries", s, id+".json")
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
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
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
	return os.Link(tmp.Name(), name)
}

// appendLine adds one line to a file, creating it, and syncs before it returns.
func appendLine(name, line string) error {
	if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
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
	return err
}

func appendLog(store, event, session, id, stamp, policy, source string) error {
	rec, _ := json.Marshal(map[string]string{"event": event, "session": session, "entry": id, "stamp": stamp, "publish": policy, "source": source})
	return appendLine(filepath.Join(store, "log.jsonl"), string(rec))
}

// readOpen reads the session's open record from log.jsonl. An unreadable log, or
// a line that may be this session's open and does not decode, is an error:
// corrupt provenance never reads as none.
func readOpen(store, session string) (openRec, error) {
	name := filepath.Join(store, "log.jsonl")
	raw, err := os.ReadFile(name)
	if os.IsNotExist(err) {
		return openRec{}, nil
	}
	if err != nil {
		return openRec{}, refuse("cannot read the session's open record from %s: %v", name, err)
	}
	quoted, _ := json.Marshal(session)
	for i, line := range strings.Split(string(raw), "\n") {
		var rec map[string]string
		if strings.TrimSpace(line) == "" {
			continue
		} else if json.Unmarshal([]byte(line), &rec) != nil {
			if strings.Contains(line, string(quoted)) && strings.Contains(line, `"open"`) {
				return openRec{}, refuse("%s:%d may be the open record of session %q and does not decode as one", name, i+1, session)
			}
		} else if rec["event"] == "open" && rec["session"] == session {
			var opened time.Time
			if rec["stamp"] != "" {
				if opened, err = time.Parse(time.RFC3339Nano, rec["stamp"]); err != nil {
					return openRec{}, refuse("%s:%d open record of session %q has an invalid stamp %q", name, i+1, session, rec["stamp"])
				}
			}
			return openRec{rec["source"], rec["publish"], opened, true}, nil
		}
	}
	return openRec{}, nil
}

// open starts a session record, or re-opens it: a re-open naming the recorded
// policy (and source, when it names one) changes nothing; another is a conflict.
func open(store, session, source string, now time.Time, publish string) error {
	rec, err := readOpen(store, session)
	if err != nil {
		return err
	}
	stamp := now.UTC().Format(time.RFC3339Nano)
	if _, flat, ok := locate(store, session); ok {
		switch {
		case flat:
		case !rec.found: // an open interrupted before it was logged: log it now
			return appendLog(store, "open", session, "", stamp, publish, source)
		case rec.publish != publish || (source != "" && source != rec.source):
			again := []string{"--store", store, "--session", session}
			if rec.source != "" {
				again = append(again, "--source", rec.source)
			}
			msg := fmt.Sprintf("session %q is already open with publish=%s source=%s; a re-open names the same, and another policy or source is a new session id",
				session, rec.publish, cmp.Or(rec.source, "-"))
			if rec.publish != publish {
				msg = fmt.Sprintf("session %q is already open with publish=%s source=%s; --publish %s names another, and a re-open names the same",
					session, rec.publish, cmp.Or(rec.source, "-"), publish)
			}
			return conflict(command("open", append(again, "--publish", rec.publish)...), "%s", msg)
		}
		return nil
	}
	header := fmt.Sprintf("# cairn %s\n\nOpened: %s\nSource: %s\nPublish: %s\n", session, stamp, source, publish)
	if err := writeNew(sessionFile(store, session), []byte(header)); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	return appendLog(store, "open", session, "", stamp, publish, source)
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
			return nil, nil, refuse("invalid entry heading in session %q at line %d", session, i+1)
		}
		if seen[m[2]] {
			return nil, nil, refuse("duplicate entry %q in session %q", m[2], session)
		}
		seen[m[2]] = true
		body := strings.TrimSpace(strings.Join(lines[i+1:end], "\n"))
		rows, bodies = append(rows, row{session: session, id: m[2], policy: "unknown", stamp: t, bytes: len(body)}), append(bodies, body)
		i = end - 1
	}
	return rows, bodies, nil
}

func conflictEntry(store, session, id string) error {
	return conflict(command("receipt", "--store", store, "--session", session, "--entry", id, "--text"),
		"entry %q already holds different prose; append these words under a new --entry id, or read what it holds", id)
}

// appendEntry stores the exact words under a stable id: a retry of the same words
// is a duplicate, other words under the same id a conflict, never an overwrite.
func appendEntry(store, session, id, text, source string, now time.Time, publish string) (r row, dup bool, err error) {
	if text == "" {
		return r, false, refuse("empty note stores nothing; refusing to file it")
	}
	_, flat, ok := locate(store, session)
	if !ok {
		if !slices.Contains(policies, publish) {
			publish = "manual"
		}
		return r, false, missing("", "no such session %q under store %q; open first: %s", session, store,
			command("open", "--store", store, "--session", session, "--publish", publish))
	}
	if flat {
		return r, false, refuse("session %q is a flat record, which this tool reads but does not append to", session)
	}
	opened, err := readOpen(store, session)
	if err != nil {
		return r, false, err
	}
	source = cmp.Or(source, opened.source)
	if publish == "" {
		if !slices.Contains(policies, opened.publish) {
			return r, false, refuse("--publish is required: session %q has no open record naming a policy in %s", session, filepath.Join(store, "log.jsonl"))
		}
		publish = opened.publish
	} else if opened.found && slices.Contains(policies, opened.publish) && publish != opened.publish {
		return r, false, conflict(command("append", "--store", store, "--session", session, "--entry", id, "--publish", opened.publish),
			"session %q holds publish=%s; --publish %s names another", session, opened.publish, publish)
	}
	final, stamp := entryPath(store, session, id), now.UTC()
	for try := 0; ; try++ {
		if raw, err := os.ReadFile(final); err == nil {
			var prev entryFile
			if err := json.Unmarshal(raw, &prev); err != nil {
				return r, false, refuse("stored entry %q is corrupt: %v", id, err)
			}
			if prev.Text != text {
				return r, false, conflictEntry(store, session, id)
			}
			t, err := time.Parse(time.RFC3339Nano, prev.Stamp)
			if err != nil {
				return r, false, refuse("stored entry %q has an invalid stamp: %v", id, err)
			}
			return row{session, id, prev.Source, prev.Publish, t, len(text)}, true, ensurePointer(store, session, id, t)
		} else if !os.IsNotExist(err) {
			return r, false, err
		}
		rec, _ := json.Marshal(entryFile{session, id, stamp.Format(time.RFC3339Nano), source, publish, text})
		if err := writeNew(final, rec); errors.Is(err, os.ErrExist) && try == 0 {
			continue // lost a race to another writer: read the winner
		} else if err != nil {
			return r, false, err
		}
		break
	}
	if err := ensurePointer(store, session, id, stamp); err != nil {
		return r, false, err
	}
	return row{session, id, source, publish, stamp, len(text)}, false, appendLog(store, "append", session, id, stamp.Format(time.RFC3339Nano), publish, source)
}

// ensurePointer lands the ENTRY line in the readable record, once (it heals an interrupted append).
func ensurePointer(store, session, id string, stamp time.Time) error {
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
	return appendLine(name, fmt.Sprintf("ENTRY %s %s", id, stamp.UTC().Format(time.RFC3339Nano)))
}

// readRecord finds the session for a read verb.
func readRecord(store, session string) (string, bool, error) {
	if !validID(session) {
		return "", false, refuse("bad session id %q: %s", session, idRule)
	}
	if p, flat, ok := locate(store, session); ok {
		return p, flat, nil
	}
	return "", false, missing(command("index", "--store", store), "no such session %q under store %q", session, store)
}

func readEntry(store, session, id string) (entryFile, time.Time, error) {
	var ef entryFile
	raw, err := os.ReadFile(entryPath(store, session, id))
	if os.IsNotExist(err) {
		return ef, time.Time{}, missing(command("index", "--store", store, "--session", session), "no such entry %q in session %q", id, session)
	}
	if err == nil {
		err = json.Unmarshal(raw, &ef)
	}
	if err != nil {
		return ef, time.Time{}, refuse("stored entry %q is corrupt: %v", id, err)
	}
	t, err := time.Parse(time.RFC3339Nano, ef.Stamp)
	if err != nil {
		return ef, t, refuse("stored entry %q is corrupt: invalid stamp: %v", id, err)
	}
	return ef, t, nil
}

func existingStore(store string) error {
	if info, err := os.Stat(store); err != nil || !info.IsDir() {
		return refuse("cannot read store %q: not a directory I can open", store)
	}
	return nil
}

// receipt reads one entry back: its row and its words.
func receipt(store, session, id string) (row, string, error) {
	if !validID(id) {
		return row{}, "", refuse("bad entry id %q: %s", id, idRule)
	}
	path, flat, err := readRecord(store, session)
	if err != nil {
		return row{}, "", err
	}
	if flat {
		rows, bodies, err := flatRows(path, session)
		for i, r := range rows {
			if r.id == id {
				return r, bodies[i], nil
			}
		}
		if err == nil {
			err = missing(command("index", "--store", store, "--session", session), "no such entry %q in session %q", id, session)
		}
		return row{}, "", err
	}
	ef, t, err := readEntry(store, session, id)
	return row{ef.Session, ef.ID, ef.Source, ef.Publish, t, len(ef.Text)}, ef.Text, err
}

// index returns every entry row, oldest first, and every session id.
func index(store, session string) ([]row, []string, error) {
	if session != "" {
		if _, _, err := readRecord(store, session); err != nil {
			return nil, nil, err
		}
	}
	names := map[string]bool{}
	var rows []row
	flat := map[string]bool{}
	for _, dir := range []string{filepath.Join(store, "sessions"), store} {
		files, _ := os.ReadDir(dir)
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
				rows = append(rows, row{ef.Session, ef.ID, ef.Source, ef.Publish, t, len(ef.Text)})
			}
		}
	}
	slices.SortFunc(rows, func(a, b row) int {
		return cmp.Or(a.stamp.Compare(b.stamp), strings.Compare(a.session, b.session), strings.Compare(a.id, b.id))
	})
	sessions := make([]string, 0, len(names))
	for n := range names {
		sessions = append(sessions, n)
	}
	slices.Sort(sessions)
	return rows, sessions, nil
}

// raw is a value printed as it is, already quoted.
type raw string

// line prints "<HEAD> k=v ..."; a string value is made one token, an empty one is -.
func line(w io.Writer, head string, kv ...any) {
	fmt.Fprint(w, head)
	for i := 0; i < len(kv); i += 2 {
		v := kv[i+1]
		if s, ok := v.(string); ok {
			v = field(cmp.Or(s, "-"))
		}
		fmt.Fprintf(w, " %s=%v", kv[i], v)
	}
	fmt.Fprintln(w)
}

func stamp(t time.Time) string { return t.Format(time.RFC3339Nano) }

// runStore runs open, append, index and receipt. Output is one line per fact group;
// a refusal or a conflict goes to stderr, exit 2 or 1.
func runStore(verb string, args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("nova-cairn "+verb, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprintln(stderr, "run: nova-cairn help") }
	store := fs.String("store", "", "the store directory")
	session := fs.String("session", "", "the stable session identifier")
	entry := fs.String("entry", "", "the stable entry identifier")
	text, wantText := new(string), new(bool)
	if verb == "receipt" {
		wantText = fs.Bool("text", false, "include the entry's stored words")
	} else {
		text = fs.String("text", "", "the exact words")
	}
	file := fs.String("file", "", "file holding the exact words; - reads stdin")
	source := fs.String("source", "", "where the words came from")
	publish := fs.String("publish", "", "the publication policy: "+strings.Join(policies, ", "))
	maxN := fs.Int("max", 20, "lines to list per kind before MORE; 0 lists all")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	given := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { given[f.Name] = true })
	var problems []string
	for _, want := range []struct {
		name, v string
		ok      bool
	}{{"store", *store, true}, {"session", *session, verb != "index"}, {"entry", *entry, verb == "append" || verb == "receipt"}, {"publish", *publish, verb == "open"}} {
		if want.ok && want.v == "" {
			problems = append(problems, "--"+want.name+" is required")
		}
	}
	for _, id := range []struct{ name, v string }{{"session", *session}, {"entry", *entry}} {
		if id.v != "" && !validID(id.v) {
			problems = append(problems, fmt.Sprintf("--%s %s is not an id: %s", id.name, quote(id.v), idRule))
		}
	}
	if *publish != "" && !slices.Contains(policies, *publish) {
		problems = append(problems, fmt.Sprintf("--publish %s is not a policy; it is one of %s", quote(*publish), strings.Join(policies, ", ")))
	}
	if verb == "append" && given["text"] == given["file"] {
		problems = append(problems, "the words come from exactly one of --text or --file")
	}
	if *maxN < 0 || fs.NArg() > 0 {
		problems = append(problems, "--max must be zero or more, and no argument stands outside a flag")
	}
	var err error
	if len(problems) > 0 {
		err = refuse("%s", strings.Join(problems, "; "))
	} else if verb == "index" || verb == "receipt" {
		err = existingStore(*store)
	}
	var r row
	var words string
	var dup bool
	var rec openRec
	var rows []row
	var names []string
	now := time.Now().UTC()
	if err == nil {
		switch verb {
		case "open":
			before, rerr := readOpen(*store, *session)
			if err = rerr; err == nil {
				err = open(*store, *session, *source, now, *publish)
			}
			if err == nil {
				rec, err = readOpen(*store, *session)
				if before.found && !before.opened.IsZero() {
					now = before.opened
				}
				dup = before.found
			}
		case "append":
			words = *text
			if given["file"] {
				var b []byte
				if *file == "-" {
					b, err = io.ReadAll(stdin)
				} else {
					b, err = os.ReadFile(*file)
				}
				words = string(b)
			}
			if err == nil {
				r, dup, err = appendEntry(*store, *session, *entry, words, *source, now, *publish)
			}
		case "receipt":
			r, words, err = receipt(*store, *session, *entry)
		case "index":
			rows, names, err = index(*store, *session)
		}
	}
	if err != nil {
		rf, ok := err.(*refusal)
		if !ok {
			rf = &refusal{msg: err.Error(), code: 2}
		}
		status, lead := "REFUSED", ""
		if rf.code == 1 {
			status = "FAILED"
			if verb == "append" {
				lead = fmt.Sprintf(" session=%s entry=%s", *session, *entry)
			}
		}
		fmt.Fprintf(stderr, "%s %s%s: %s; run: %s\n", strings.ToUpper(verb), status, lead, esc(rf.msg), esc(cmp.Or(rf.remedy, "nova-cairn help")))
		return rf.code
	}
	switch verb {
	case "open":
		args := []any{"session", *session, "store", *store, "source", rec.source, "publish", *publish}
		if dup {
			args = append(args, "reopened", true)
		}
		line(stdout, "OPEN OK", append(args, "stamp", stamp(now))...)
	case "append":
		line(stdout, "APPEND OK", "session", *session, "entry", *entry, "source", r.source, "persisted", true,
			"published", false, "publish", r.policy, "duplicate", dup, "stamp", stamp(r.stamp))
	case "receipt":
		args := []any{"session", r.session, "entry", r.id, "stamp", stamp(r.stamp), "bytes", r.bytes, "source", r.source,
			"persisted", true, "published", false, "publish", r.policy}
		if *wantText {
			args = append(args, "text", raw(quote(words)))
		}
		line(stdout, "RECEIPT OK", args...)
	case "index":
		per := map[string]int{}
		for _, r := range rows {
			per[r.session]++
		}
		line(stdout, "INDEX OK", "sessions", len(names), "entries", len(rows))
		for i, s := range names {
			if *maxN > 0 && i >= *maxN {
				fmt.Fprintf(stdout, "INDEX MORE kind=session shown=%d total=%d --max <n> raises the ceiling, --max 0 lists all\n", *maxN, len(names))
				break
			}
			o, _ := readOpen(*store, s)
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
				fmt.Fprintf(stdout, "INDEX MORE kind=entry shown=%d total=%d --max <n> raises the ceiling, --max 0 lists all\n", *maxN, len(rows))
				break
			}
			line(stdout, "INDEX ENTRY", "session", r.session, "entry", r.id, "stamp", stamp(r.stamp), "bytes", r.bytes, "source", r.source)
		}
	}
	return 0
}
