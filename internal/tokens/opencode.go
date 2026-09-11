package tokens

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

// --opencode <label>=<file>: OpenCode's SQLite database.
//
// THE ONE SUBPROCESS. Standard-library Go cannot read SQLite and a driver would be the
// first module dependency in this repo, so this reader shells out to `sqlite3`, which
// SPEC-TOKENS.md picks deliberately and says so. It is the only program this tool runs:
// an earlier draft ran `git log` over the bus checkout to order competing notes, and that
// order is now explicit in the notes themselves.
//
// The live database is never opened for writing and never opened at all: it and its -wal
// and -shm siblings are COPIED into --scratch and queried there with -readonly, under
// --timeout. A source is read-only, and a fold that touched the file it is measuring
// would be measuring itself.

// SQLiteBinary is the one program this family requires on PATH.
const SQLiteBinary = "sqlite3"

// DefaultTimeout is the per-subprocess budget. It is the one flag with a default, for the
// reason SPEC.md gives nova-bus --git-timeout: it is how long the tool waits before saying
// so, not a fact about anybody's data.
const DefaultTimeout = 120 * time.Second

// waitDelay is how long Run waits for the streams after the context is done. Without it a
// --timeout is a deadline on the process and not on this tool.
const waitDelay = 2 * time.Second

// The three queries. OpenCode keeps the row itself in a JSON `data` column -- `session`
// is the one table with the fields as columns of its own -- so every field SPEC-TOKENS
// names (`providerID`, `modelID`, the five `tokens.*` counts, `path.cwd`, a tool part's
// inputs) is a json_extract, and the answers come back as `sqlite3 -json` prints them:
// a JSON array of row objects keyed by the SELECT's own aliases. Bare columns were
// `no such column: providerID`, one TOKENS UNREADABLE, and every OpenCode row of the day
// lost (measured 2026-09-11, folding the day beside the prototype).
//
// `time_created` is epoch MILLISECONDS; strftime renders it as the RFC 3339 UTC stamp
// rule 17 folds by, so the day is the message's own stamp and never the bench's clock.
// -json rather than -tabs because a tool part's `command` input holds tabs and newlines,
// and a row that splits on the data inside it is a row read wrong.
const (
	sessionsSQL = `SELECT id AS id, parent_id AS parent_id, directory AS directory FROM session`
	messagesSQL = `SELECT id AS id, session_id AS session_id, ` +
		`strftime('%Y-%m-%dT%H:%M:%SZ', time_created/1000, 'unixepoch') AS stamp, ` +
		`json_extract(data, '$.providerID') AS provider, json_extract(data, '$.modelID') AS model, ` +
		`json_extract(data, '$.tokens.input') AS input, json_extract(data, '$.tokens.output') AS output, ` +
		`json_extract(data, '$.tokens.cache.write') AS cache_write, json_extract(data, '$.tokens.cache.read') AS cache_read, ` +
		`json_extract(data, '$.tokens.reasoning') AS reasoning, json_extract(data, '$.path.cwd') AS cwd ` +
		`FROM message WHERE json_extract(data, '$.role') = 'assistant' ORDER BY time_created, id`
	partsSQL = `SELECT message_id AS message_id, session_id AS session_id, ` +
		`json_extract(data, '$.state.input.command') AS command, json_extract(data, '$.state.input.filePath') AS file_path, ` +
		`json_extract(data, '$.state.input.path') AS path, json_extract(data, '$.state.input.pattern') AS pattern ` +
		`FROM part WHERE json_extract(data, '$.type') = 'tool' ORDER BY message_id, id`
)

// partInputs are the four tool inputs SPEC-TOKENS names, in a fixed order so that one
// database folds the same way twice.
var partInputs = []string{"command", "file_path", "path", "pattern"}

// messageCounts maps the message row's aliases onto the five types.
var messageCounts = []struct {
	column string
	typ    Type
}{
	{"input", Input}, {"output", Output}, {"cache_write", CacheWrite},
	{"cache_read", CacheRead}, {"reasoning", Reasoning},
}

// ReadOpenCode copies the database into scratch and reads it there.
func ReadOpenCode(label, dbPath, scratch string, timeout time.Duration, rules *Rules) *Source {
	s := &Source{Label: Label(KindOpenCode, label), Kind: KindOpenCode, Path: dbPath, Reports: AllTypes, Basis: UTC}
	s.Stat.Files = 1

	copyDir := filepath.Join(scratch, "opencode-"+label)
	if err := os.MkdirAll(copyDir, 0o755); err != nil {
		s.unreadable(dbPath, err.Error())
		return s
	}
	copied := filepath.Join(copyDir, filepath.Base(dbPath))
	if err := copyFile(dbPath, copied); err != nil {
		s.unreadable(dbPath, err.Error())
		return s
	}
	for _, sib := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(dbPath + sib); err == nil {
			s.Stat.Files++
			if err := copyFile(dbPath+sib, copied+sib); err != nil {
				s.unreadable(dbPath+sib, err.Error())
			}
		}
	}

	sessions, err := query(copied, sessionsSQL, timeout)
	if err != nil {
		s.unreadable(dbPath, err.Error())
		return s
	}
	messages, err := query(copied, messagesSQL, timeout)
	if err != nil {
		s.unreadable(dbPath, err.Error())
		return s
	}
	parts, err := query(copied, partsSQL, timeout)
	if err != nil {
		s.unreadable(dbPath, err.Error())
		return s
	}

	parent := map[string]string{}
	for _, row := range sessions {
		parent[row["id"]] = row["parent_id"]
	}
	inputs := map[string][]string{}
	for _, row := range parts {
		id := row["message_id"]
		if id == "" {
			continue
		}
		for _, col := range partInputs {
			if v := row[col]; v != "" {
				inputs[id] = append(inputs[id], v)
			}
		}
	}

	sort.SliceStable(messages, func(i, j int) bool {
		if messages[i]["stamp"] != messages[j]["stamp"] {
			return messages[i]["stamp"] < messages[j]["stamp"]
		}
		return messages[i]["id"] < messages[j]["id"]
	})

	prev := map[string]string{}
	for _, row := range messages {
		id, session := row["id"], row["session_id"]
		day, ok := DayOfStamp(row["stamp"])
		if !ok {
			s.unparsed(dbPath, 0, "message "+id+": time_created is not a stamp this tool can read: "+row["stamp"])
			continue
		}
		// A child session inherits its parent's repo at its first message, and never
		// across sources.
		if _, ok := prev[session]; !ok {
			if p, ok := parent[session]; ok {
				prev[session] = prev[p]
			}
		}
		repo := rules.AttributeInputs(inputs[id], prev[session])
		if repo == Unknown && row["cwd"] != "" {
			// path.cwd is the same lowest rung as a transcript's cwd: a turn with no
			// tool part named no path, and the session was `unknown` for the whole day.
			repo = rules.Attribute(PathTokens([]string{row["cwd"]}), "")
		}
		prev[session] = repo
		m := Message{Day: day, Basis: UTC, Model: row["model"], Repo: repo, Turn: true}
		for _, c := range messageCounts {
			cell := strings.TrimSpace(row[c.column])
			if cell == "" || cell == Dash {
				continue
			}
			v, err := strconv.ParseInt(cell, 10, 64)
			if err != nil {
				continue
			}
			m.Counts.Set(c.typ, v)
		}
		s.AddMessage(id, m)
	}
	s.Collapse()
	return s
}

// query runs one statement against the COPY, read-only, under the timeout, and returns
// each row keyed by the SELECT's own aliases.
func query(db, sql string, timeout time.Duration) ([]map[string]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, SQLiteBinary, "-readonly", "-json", db, sql)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	// A killed sqlite3 can leave a grandchild holding the pipe, and Run would then wait
	// for THAT rather than for the timeout the caller set. WaitDelay is the second
	// deadline that makes the first one true.
	cmd.WaitDelay = waitDelay
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("timeout after %ds waiting for %s; --timeout is how long this tool waits before saying so", int(timeout/time.Second), SQLiteBinary)
	}
	if err != nil {
		return nil, fmt.Errorf("%s: %v: %s", SQLiteBinary, err, strings.TrimSpace(errb.String()))
	}
	text := strings.TrimSpace(out.String())
	if text == "" {
		return nil, nil
	}
	dec := json.NewDecoder(strings.NewReader(text))
	dec.UseNumber()
	var raw []map[string]any
	if err := dec.Decode(&raw); err != nil {
		return nil, fmt.Errorf("%s -json did not answer with an array of rows: %v", SQLiteBinary, err)
	}
	rows := make([]map[string]string, 0, len(raw))
	for _, r := range raw {
		row := make(map[string]string, len(r))
		for k, v := range r {
			row[k] = cellText(v)
		}
		rows = append(rows, row)
	}
	return rows, nil
}

// cellText renders one JSON cell as text. SQL NULL is the empty string, which is a column
// the row does not carry: a dash in the day file and never a zero (rule 15).
func cellText(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case json.Number:
		return t.String()
	case bool:
		if t {
			return "1"
		}
		return "0"
	default:
		return fmt.Sprint(t)
	}
}

// HaveSQLite reports whether the one required program is on PATH.
func HaveSQLite() error {
	if _, err := exec.LookPath(SQLiteBinary); err != nil {
		return errors.New(SQLiteBinary + " is not on PATH, and --opencode needs it: this family's one subprocess reads the copied database with " + SQLiteBinary + " -readonly")
	}
	return nil
}

func copyFile(from, to string) error {
	src, err := openSource(from)
	if err != nil {
		return err
	}
	defer src.Close()
	dst, err := os.Create(to)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		dst.Close()
		return err
	}
	return dst.Close()
}
