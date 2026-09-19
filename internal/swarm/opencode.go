package swarm

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// THE USAGE SOURCE IS THE DATABASE THE HARNESS WRITES (SPEC-SWARM rule 12, rule 13).
//
// A SOURCE IS NAMED FOR WHAT IT IS, and on 2026-09-11 this one was not: the enum said
// `opencode` while the reader read a tab-separated file no OpenCode writes, and two jobs
// that burned 61,875 and 85,308 tokens against `--tokens 20000` both reported
// `budget=-/20000`. The name is honest here: `usage: opencode` reads the job's OWN
// `opencode.db`, in the data home this tool exported for it, which is the whole reason
// slots exist -- one job, one database.
//
// THE ONE SUBPROCESS. This repo is standard library only (CONTRIBUTING.md), and the
// standard library cannot read SQLite; a driver would be this repo's first module
// dependency. So the source is read through `sqlite3`, which SPEC-TOKENS picks deliberately
// for the same reason and whose reader this one follows. It is the only program this
// package runs, it is run READ-ONLY, and no value ever reaches a shell: the binary is
// executed with arguments.

// SQLiteBinary is the one program a usage source needs on PATH.
const SQLiteBinary = "sqlite3"

// OpenCodeDB is the database's path inside the job's data home (XDG_DATA_HOME).
const OpenCodeDB = "opencode/opencode.db"

// ErrNoSQLite is the class of a usage source whose one program is not on PATH. It carries
// the literal refusal line so every caller that surfaces the error says the same thing and
// a missing reader is never a dash nobody explained.
var ErrNoSQLite = errors.New("USAGE REFUSED reason=no_sqlite")

// usageSettleWait is how long a read waits for a write-ahead log to be checkpointed. It is
// a property of this tool, like the sample interval, and never a fact about anybody's data.
const usageSettleWait = 5 * time.Second

// usageSettlePause is the gap between attempts while a -wal is still beside the database.
const usageSettlePause = 50 * time.Millisecond

// OpenCodeStoreLocations are the paths OpenCode may keep its database at inside ONE job's
// data home, in the order the reader tries them: the XDG_DATA_HOME spelling this tool
// exports, then the HOME/.local/share spelling OpenCode derives from HOME on Linux -- the
// bench carries its auth.json there. Both are joined to the job's own data home, so both are
// this job's database and never another job's.
func OpenCodeStoreLocations(dataHome string) []string {
	return []string{
		filepath.Join(dataHome, filepath.FromSlash(OpenCodeDB)),
		filepath.Join(dataHome, ".local", "share", "opencode", "opencode.db"),
	}
}

// UsageRefusalLine is the one line a usage read that did not answer leaves on the record, so
// that a row of dashes is never silent about the reader it needed.
func UsageRefusalLine(id string, err error) string {
	return fmt.Sprintf("%s id=%s", oneline.Escape(redactedReason(err)), oneline.Field(id))
}

// usageTimeout is how long this tool waits for one query before saying so. It is a property
// of the tool, like a deadline, and never a fact about anybody's data: the supervisor
// samples every few seconds and a query that has not answered in this long has failed.
const usageTimeout = 20 * time.Second

// usageWaitDelay is how long Run waits for the streams after the context is done. Without
// it a killed sqlite3 that left a grandchild holding the pipe would make the timeout above
// a deadline on the process and not on this tool.
const usageWaitDelay = 2 * time.Second

// messagesSQL is the one statement this tool runs. The numbers live in the `data` JSON of
// the message table -- the provider, the model, and the five token types nova-tokens counts
// (SPEC-TOKENS rule 14) -- and a type the provider did not report is NULL, which prints as
// the empty string and is read back as an absence, never as a zero.
const messagesSQL = `SELECT ` +
	`json_extract(data, '$.providerID'), ` +
	`json_extract(data, '$.modelID'), ` +
	`json_extract(data, '$.tokens.input'), ` +
	`json_extract(data, '$.tokens.output'), ` +
	`json_extract(data, '$.tokens.cache.write'), ` +
	`json_extract(data, '$.tokens.cache.read'), ` +
	`json_extract(data, '$.tokens.reasoning') ` +
	`FROM message WHERE json_extract(data, '$.tokens') IS NOT NULL`

// readOpenCodeUsage reads one job's accounting out of its own database.
//
// A DATABASE THAT IS NOT THERE IS NOT AN ERROR: it is the harness having reported nothing
// yet. A database that is there and cannot be read IS one, and rule 13 ends the job
// BUDGET-UNVERIFIABLE on the third such sample, because a numeric budget the tool has
// stopped being able to see is a budget the caller believes is enforced and is not.
func readOpenCodeUsage(dataHome string) (ProviderUsage, error) {
	path, err := findOpenCodeStore(dataHome)
	if err != nil {
		return ProviderUsage{}, err
	}
	if path == "" {
		// The harness has written no database at either standard location yet: it has
		// reported nothing, which is an absence and not a failure.
		return ProviderUsage{Values: map[string]string{}}, nil
	}
	if _, err := exec.LookPath(SQLiteBinary); err != nil {
		return ProviderUsage{}, fmt.Errorf("%w: the usage source %s could not be read: %s is not on PATH, and `usage: opencode` reads that database with `%s -readonly`",
			ErrNoSQLite, path, SQLiteBinary, SQLiteBinary)
	}
	rows, err := queryOpenCodeWaiting(path)
	if err != nil {
		return ProviderUsage{}, err
	}
	if len(rows) == 0 {
		// The database exists and holds no message with tokens: the harness has started and
		// reported nothing yet, which is an absence and not a failure.
		return ProviderUsage{Values: map[string]string{}}, nil
	}
	return foldOpenCodeRows(rows)
}

// findOpenCodeStore is the first of the standard locations that holds a database, or an
// empty path when none does. A location that exists but cannot be stat'd is an error, not a
// silent absence: the path is this tool's own and a read that stopped is a fact.
func findOpenCodeStore(dataHome string) (string, error) {
	for _, path := range OpenCodeStoreLocations(dataHome) {
		st, err := os.Stat(path)
		if err == nil {
			if st.IsDir() {
				continue
			}
			return path, nil
		}
		if !os.IsNotExist(err) {
			return "", fmt.Errorf("the usage source %s could not be read: %s", path, redactedReason(err))
		}
	}
	return "", nil
}

// queryOpenCodeWaiting runs the one statement, and while a write-ahead log sits beside the
// database it waits for the writer to checkpoint it, up to usageSettleWait. The read lands
// in the window between the harness spending and its connection closing, when the `-wal` is
// present and `sqlite3 -readonly` answers `database is locked`; recording a dash for that
// window loses tokens the harness really spent.
func queryOpenCodeWaiting(path string) ([][]string, error) {
	return walWait{
		first:  usageTimeout,
		settle: usageSettleWait,
		pause:  usageSettlePause,
		query:  queryOpenCode,
		now:    time.Now,
		sleep:  time.Sleep,
	}.read(path)
}

// walWait is that retry with its clock and its read named, so the waiting itself can be
// held by a test that turns on no clock of its own and starts no process. `first` is what
// the first read is allowed; every retry is allowed what is left of the window.
type walWait struct {
	first  time.Duration
	settle time.Duration
	pause  time.Duration
	query  func(path string, limit time.Duration) ([][]string, error)
	now    func() time.Time
	sleep  func(time.Duration)
}

// read takes one reading and, while the refusal came with a -wal still beside the
// database, waits for the writer to checkpoint it.
//
// THE WINDOW BOUNDS THE WAITING AND NEVER THE FIRST READ. The deadline is taken AFTER the
// first attempt returns, because usageSettleWait is how long this tool waits for a
// checkpoint -- not how long the whole read may take. Taken before the attempt, a slow
// first open spends the budget that exists to outlast the writer: on a loaded macOS bench
// the first `sqlite3` is a Mach-O the machine has never seen and the kernel assesses it on
// that first exec, which cost the reader its every retry and recorded `database is locked`
// for a writer that checkpointed a moment later. So a refusal with a -wal beside it always
// buys at least one more look, and the window measures the looking.
func (w walWait) read(path string) ([][]string, error) {
	rows, err := w.query(path, w.first)
	if err == nil {
		return rows, nil
	}
	deadline := w.now().Add(w.settle)
	for {
		if !walPending(path) || !w.now().Before(deadline) {
			return nil, err
		}
		w.sleep(w.pause)
		if rows, err = w.query(path, w.first); err == nil {
			return rows, nil
		}
	}
}

// walPending says whether the database has a write-ahead log beside it that sqlite3 cannot
// replay read-only. That file, and not the error text, is what a retry waits on.
func walPending(path string) bool {
	_, err := os.Stat(path + "-wal")
	return err == nil
}

// queryOpenCode runs the one statement, read-only, under the timeout. The database is the
// job's own and this tool never writes it: `-readonly` is that promise kept by the program
// that opens it, and `-tabs` is the shape the rows come back in.
func queryOpenCode(path string, limit time.Duration) ([][]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, SQLiteBinary, "-readonly", "-tabs", path, messagesSQL)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	cmd.WaitDelay = usageWaitDelay
	err := cmd.Run()
	if ctx.Err() != nil {
		return nil, fmt.Errorf("the usage source %s could not be read: %s did not answer within %s",
			path, SQLiteBinary, limit)
	}
	if err != nil {
		return nil, fmt.Errorf("the usage source %s could not be read: %s: %v: %s",
			path, SQLiteBinary, err, oneLine(errb.String()))
	}
	var rows [][]string
	for _, line := range strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		rows = append(rows, strings.Split(line, "\t"))
	}
	return rows, nil
}

// foldOpenCodeRows sums the message rows into the columns the usage file carries.
//
// A FIELD THE PROVIDER DID NOT REPORT IS THE LITERAL DASH, NEVER 0 (rule 12): a zero is a
// measurement and a dash is an absence, and nova-tokens reads this file and reads a dash as
// unknown. So a token type no message reported stays a dash, and one that some message
// reported is the sum of the messages that did.
func foldOpenCodeRows(rows [][]string) (ProviderUsage, error) {
	values := map[string]string{}
	sums := make([]int64, len(TokenColumns))
	reported := make([]bool, len(TokenColumns))
	maxInt := int64(^uint(0) >> 1)
	var total int64
	provider, model := "", ""
	for rowIndex, row := range rows {
		if len(row) != 2+len(TokenColumns) {
			return ProviderUsage{}, fmt.Errorf("opencode usage row %d column count is invalid", rowIndex+1)
		}
		if v := strings.TrimSpace(row[0]); v != "" {
			provider = v
		}
		if v := strings.TrimSpace(row[1]); v != "" {
			model = v
		}
		for i, column := range TokenColumns {
			cell := strings.TrimSpace(row[2+i])
			if cell == "" || cell == Dash {
				continue
			}
			n, err := strconv.ParseInt(cell, 10, 64)
			if err != nil || n < 0 || n > maxInt {
				return ProviderUsage{}, fmt.Errorf("opencode usage row %d column %s is not a nonnegative host integer", rowIndex+1, column)
			}
			if sums[i] > maxInt-n {
				return ProviderUsage{}, fmt.Errorf("opencode usage row %d column %s exceeds the host integer maximum", rowIndex+1, column)
			}
			if total > maxInt-n {
				return ProviderUsage{}, fmt.Errorf("opencode usage row %d column %s makes the token total exceed the host integer maximum", rowIndex+1, column)
			}
			sums[i] += n
			total += n
			reported[i] = true
		}
	}
	for i, c := range TokenColumns {
		if reported[i] {
			values[c] = strconv.FormatInt(sums[i], 10)
			continue
		}
		values[c] = Dash
	}
	values["provider"] = dashOr(provider)
	values["model"] = dashOr(model)
	// The message table holds no repository, and this reader never invents one: the usage
	// row's `repo` is what the caller knew, or a dash.
	values["repo"] = Dash
	return ProviderUsage{Values: values, Observed: true, Turns: len(rows)}, nil
}

// oneLine keeps a subprocess's complaint on the one line a RUN BUDGET-UNVERIFIABLE carries.
func oneLine(s string) string {
	return strings.TrimSpace(strings.NewReplacer("\n", " ", "\r", " ", "\t", " ").Replace(s))
}
