package tokens

import (
	"bytes"
	"context"
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

// The three queries. Assistant messages carry the usage; sessions carry the parent link a
// child session inherits its repo across; parts carry the tool inputs a path is found in.
const (
	sessionsSQL = `SELECT id, parent_id, directory FROM session`
	messagesSQL = `SELECT id, session_id, time_created, providerID, modelID, tokens_input, tokens_output, tokens_cache_write, tokens_cache_read, tokens_reasoning, cwd FROM message WHERE role = 'assistant'`
	partsSQL    = `SELECT message_id, session_id, input FROM part WHERE input IS NOT NULL`
)

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
		if len(row) >= 2 {
			parent[row[0]] = row[1]
		}
	}
	inputs := map[string][]string{}
	for _, row := range parts {
		if len(row) >= 3 && row[0] != "" {
			inputs[row[0]] = append(inputs[row[0]], row[2])
		}
	}

	sort.SliceStable(messages, func(i, j int) bool {
		if len(messages[i]) < 3 || len(messages[j]) < 3 {
			return false
		}
		if messages[i][2] != messages[j][2] {
			return messages[i][2] < messages[j][2]
		}
		return messages[i][0] < messages[j][0]
	})

	prev := map[string]string{}
	seen := map[string]Message{}
	var order []string
	for _, row := range messages {
		if len(row) < 11 {
			continue
		}
		id, session, stamp, model := row[0], row[1], row[2], row[4]
		day := dayOfStamp(stamp)
		if day == "" {
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
		prev[session] = repo
		if id == "" {
			s.Stat.NoID++
			continue
		}
		m := Message{Day: day, Basis: UTC, Model: model, Repo: repo, Turn: true}
		for i, t := range []Type{Input, Output, CacheWrite, CacheRead, Reasoning} {
			cell := strings.TrimSpace(row[5+i])
			if cell == "" || cell == Dash {
				continue
			}
			v, err := strconv.ParseInt(cell, 10, 64)
			if err != nil {
				continue
			}
			m.Counts.Set(t, v)
		}
		if _, dup := seen[id]; dup {
			s.Stat.Dup++
		} else {
			order = append(order, id)
		}
		seen[id] = m
	}
	for _, id := range order {
		s.Stream = append(s.Stream, seen[id])
	}
	s.Stat.Messages = len(s.Stream)
	return s
}

// query runs one statement against the COPY, read-only, under the timeout.
func query(db, sql string, timeout time.Duration) ([][]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, SQLiteBinary, "-readonly", "-tabs", db, sql)
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
	var rows [][]string
	for _, line := range strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n") {
		if line == "" {
			continue
		}
		rows = append(rows, strings.Split(line, "\t"))
	}
	return rows, nil
}

// HaveSQLite reports whether the one required program is on PATH.
func HaveSQLite() error {
	if _, err := exec.LookPath(SQLiteBinary); err != nil {
		return errors.New(SQLiteBinary + " is not on PATH, and --opencode needs it: this family's one subprocess reads the copied database with " + SQLiteBinary + " -readonly")
	}
	return nil
}

func copyFile(from, to string) error {
	src, err := os.Open(from)
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
