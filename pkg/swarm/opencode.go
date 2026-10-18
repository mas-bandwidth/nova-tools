package swarm

import (
	"bytes"
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/cardcost"
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
const messagesSQL = `SELECT ` + messageColumns +
	`FROM message WHERE json_extract(data, '$.tokens') IS NOT NULL`

// messageColumns are messagesSQL's columns, each a message's own figure.
const messageColumns = `json_extract(data, '$.providerID'), ` +
	`json_extract(data, '$.modelID'), ` +
	`json_extract(data, '$.tokens.input'), ` +
	`json_extract(data, '$.tokens.output'), ` +
	`json_extract(data, '$.tokens.cache.write'), ` +
	`json_extract(data, '$.tokens.cache.read'), ` +
	`json_extract(data, '$.tokens.reasoning'), ` +
	// the harness's own cost of the message, what a dollar budget is held to (#5094)
	`json_extract(data, '$.cost') `

// openCodeReader is the value under test: the store's locations, the one program that
// reads them, and how that program is found. A nil lookPath is the production default,
// exec.LookPath on the process's PATH; a test carries its own lookup naming the fake it
// built, or one that finds nothing -- the per-test seam the serial-tests ledger names
// ("a field on the value under test"), in place of a fake on the process's PATH.
type openCodeReader struct {
	lookPath func(name string) (string, error)
}

// binary resolves the one program this source runs: the reader's own lookup, else the
// process's PATH.
func (r openCodeReader) binary() (string, error) {
	if r.lookPath != nil {
		return r.lookPath(SQLiteBinary)
	}
	return exec.LookPath(SQLiteBinary)
}

// read reads one job's accounting out of its own database, resolving the one program
// through the reader's own lookup.
//
// A DATABASE THAT IS NOT THERE IS NOT AN ERROR: it is the harness having reported nothing
// yet. A database that is there and cannot be read IS one, and rule 13 ends the job
// BUDGET-UNVERIFIABLE on the third such sample, because a numeric budget the tool has
// stopped being able to see is a budget the caller believes is enforced and is not.
func (r openCodeReader) read(dataHome string) (ProviderUsage, error) {
	path, err := findOpenCodeStore(dataHome)
	if err != nil {
		return ProviderUsage{}, err
	}
	if path == "" {
		// The harness has written no database at either standard location yet: it has
		// reported nothing, which is an absence and not a failure.
		return ProviderUsage{Values: map[string]string{}}, nil
	}
	bin, err := r.binary()
	if err != nil {
		return ProviderUsage{}, fmt.Errorf("%w: the usage source %s could not be read: %s is not on PATH, and `usage: opencode` reads that database with `%s -readonly`",
			ErrNoSQLite, path, SQLiteBinary, SQLiteBinary)
	}
	rows, err := queryOpenCodeWaiting(bin, path)
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

// LiveSampleLimit is the least ONE live sample is given, whatever the interval (SPEC-SWARM
// rule 13d): "a read is given 5 seconds whatever the interval, no sample starts while one is
// unanswered, a read still unanswered at its limit is abandoned and counted as a failed
// read". The sampler raises it against the slowest read that answered (nova-swarm's
// liveSampler.readLimit), and never relates it to the interval: at a short interval slow
// reads would be counted as failures.
const LiveSampleLimit = 5 * time.Second

// ReadJobUsageLiveWithin is rule 13d's LIVE sample: the same statement openCodeReader.read runs,
// against the same database at the same two spellings, and different from it in exactly two
// ways that the rule names.
//
// IT WAITS FOR NO CHECKPOINT. "A sample reads what the database holds through whatever
// write-ahead log lies beside it, which is the ordinary state of a database a live harness
// has open, and it waits for no checkpoint; rule 13's five-second wait belongs to the final
// read, made when the harness is gone." A live sample that waited for a checkpoint would
// wait on a writer that is still running and has no reason to close its connection.
//
// AND IT IS BOUNDED BY the caller's limit (LiveSampleLimit is the least a sampler gives), not by the tool's 20-second query timeout: a sample
// that hung for twenty seconds would be a sample that cannot run at a five-second interval.
//
// A DATABASE THAT IS NOT THERE IS NOT AN ERROR, exactly as in the final read: it is the
// harness having reported nothing yet, which is an absence and not a failure. The three
// cases rule 13d keeps apart -- nothing observed, a partial observation, a read that FAILS
// -- are the caller's to tell apart, and this function's error is the third of them.
func ReadJobUsageLiveWithin(dataHome string, limit time.Duration) (ProviderUsage, error) {
	path, err := findOpenCodeStore(dataHome)
	if err != nil {
		return ProviderUsage{}, err
	}
	if path == "" {
		return ProviderUsage{Values: map[string]string{}}, nil
	}
	bin, err := (openCodeReader{}).binary()
	if err != nil {
		return ProviderUsage{}, fmt.Errorf("%w: the usage source %s could not be read: %s is not on PATH, and `usage: opencode` reads that database with `%s -readonly`",
			ErrNoSQLite, path, SQLiteBinary, SQLiteBinary)
	}
	rows, err := queryOpenCode(bin, path, limit)
	if err != nil {
		return ProviderUsage{}, err
	}
	if len(rows) == 0 {
		return ProviderUsage{Values: map[string]string{}}, nil
	}
	return foldOpenCodeRows(rows)
}

// LiveUsage is ReadJobUsageLiveWithin for one job, read as the job's database grows. Every
// sample re-read and json_extract-ed every message row of the job, so a card's samples cost
// the square of its messages over its life (2026-10-17). A LiveUsage keeps what it read and
// asks the store, on each read, only for the messages that may differ from what it keeps:
//
//   - every message after the highest rowid it has read;
//   - every message written at or after the newest write it has read, less liveMarkSlack
//     (COALESCE(time_updated, time_created)). OpenCode writes an assistant message's tokens
//     and cost in place as its steps finish, and a message written again after its
//     completion was measured on one machine's own store (22,265 assistant rows: none
//     updated before completion, none more than 2 s after it, the latest 1,013 ms after).
//     Any such rewrite advances time_updated, so this test reads it again whatever the
//     margin, and a rowid reused after a delete is a new write the same way;
//   - every message it read unfinished (the assistant's, with no time.completed).
//
// Every liveWholeEvery-th read is a whole one, and so is the first: it reads every message,
// and what it keeps is rebuilt from it, so a deletion or a store replaced under the same
// name (a file system may give a new file the inode of the one removed, so the identity
// check below is no proof) is reconciled within liveWholeEvery samples. A store whose table
// has no time_updated is read whole every time, as before. A store at another path, gone
// between two reads, another file, or smaller than at the last read is read whole at once.
// Between whole reads a deleted message stays counted: the budget errs high, never low.
//
// The zero LiveUsage reads with sqlite3; one LiveUsage is used by one goroutine (a sampler
// takes no read while one is unanswered).
type LiveUsage struct {
	// query runs one statement against the store at path within limit; nil is sqlite3.
	query func(path, sql string, limit time.Duration) ([][]string, error)

	path    string
	file    os.FileInfo
	size    int64              // the store's size at the last read
	reads   int                // reads since the last whole one
	seen    int64              // the highest rowid read
	mark    int64              // the newest write read: COALESCE(time_updated, time_created)
	kept    map[int64][]string // the finished messages with tokens, by rowid
	pending map[int64]bool     // the unfinished messages, read again every read
	noTime  bool               // the table has no time_updated: every read is whole
}

// liveWholeEvery is how many reads a LiveUsage takes between whole reads, the bound on how
// long a deletion or a replaced store can leave its count wrong.
const liveWholeEvery = 10

// liveMarkSlack is how far before the newest write it has read a LiveUsage asks again. It
// guards one case only: a write whose timestamp its writer took before a row the last read
// already saw was committed, and which itself commits after that read, so its time is under
// the newest the read saw. That case was never measured; the margin is a guess at how long
// a writer holds a timestamp before its commit. Rewrites after completion need no margin
// (LiveUsage).
const liveMarkSlack = 2000 // milliseconds, the unit of OpenCode's times

// liveColumns are a live read's first four columns: the rowid, whether the message is
// finished, whether it carries tokens, and when it was last written (0 without a time).
func liveColumns(timed bool) string {
	written := `COALESCE(time_updated, time_created)`
	if !timed {
		written = `0`
	}
	return `SELECT rowid, ` +
		`(json_extract(data, '$.role') IS NOT 'assistant' OR json_extract(data, '$.time.completed') IS NOT NULL), ` +
		`(json_extract(data, '$.tokens') IS NOT NULL), ` + written + `, ` +
		messageColumns + `FROM message`
}

// liveSQL is the statement of one incremental read: each message after seen, each written
// at or after mark less liveMarkSlack, and each pending one.
func liveSQL(seen, mark int64, pending map[int64]bool) string {
	where := ` WHERE rowid > ` + strconv.FormatInt(seen, 10) +
		` OR COALESCE(time_updated, time_created) >= ` + strconv.FormatInt(mark-liveMarkSlack, 10)
	if len(pending) > 0 {
		ids := make([]string, 0, len(pending))
		for _, id := range slices.Sorted(maps.Keys(pending)) {
			ids = append(ids, strconv.FormatInt(id, 10))
		}
		where += ` OR rowid IN (` + strings.Join(ids, ",") + `)`
	}
	return liveColumns(true) + where + ` ORDER BY rowid`
}

// liveWholeSQL is the statement of a whole read; timed says the table has time_updated.
func liveWholeSQL(timed bool) string { return liveColumns(timed) + ` ORDER BY rowid` }

// Read is one live sample of the job's usage under dataHome, bounded by limit: the same
// answer ReadJobUsageLiveWithin gives, from the messages kept and the ones read now.
func (u *LiveUsage) Read(dataHome string, limit time.Duration) (ProviderUsage, error) {
	path, err := findOpenCodeStore(dataHome)
	if err != nil {
		return ProviderUsage{}, err
	}
	if path == "" {
		*u = LiveUsage{query: u.query}
		return ProviderUsage{Values: map[string]string{}}, nil
	}
	fi, err := os.Stat(path)
	if err != nil {
		return ProviderUsage{}, fmt.Errorf("the usage source %s could not be read: %s", path, redactedReason(err))
	}
	if u.file == nil || u.path != path || !os.SameFile(u.file, fi) || fi.Size() < u.size {
		*u = LiveUsage{query: u.query, path: path, file: fi}
	}
	u.size = fi.Size()
	query := u.query
	if query == nil {
		bin, err := (openCodeReader{}).binary()
		if err != nil {
			return ProviderUsage{}, fmt.Errorf("%w: the usage source %s could not be read: %s is not on PATH, and `usage: opencode` reads that database with `%s -readonly`",
				ErrNoSQLite, path, SQLiteBinary, SQLiteBinary)
		}
		query = func(path, sql string, limit time.Duration) ([][]string, error) {
			return queryOpenCodeSQL(bin, path, sql, limit)
		}
	}
	whole := u.kept == nil || u.noTime || u.reads >= liveWholeEvery
	sql := liveSQL(u.seen, u.mark, u.pending)
	if whole {
		sql = liveWholeSQL(!u.noTime)
	}
	got, err := query(path, sql, limit)
	if err != nil && !u.noTime && strings.Contains(err.Error(), "no such column") {
		// a store with no time_updated (an older harness): read whole, every time
		u.noTime, whole = true, true
		got, err = query(path, liveWholeSQL(false), limit)
	}
	if err != nil {
		return ProviderUsage{}, err
	}
	kept := map[int64][]string{}
	if !whole {
		kept = maps.Clone(u.kept)
	}
	pending := map[int64]bool{}
	seen, mark := u.seen, u.mark
	if whole {
		seen, mark = 0, 0
	}
	var now []liveRow // this read's unfinished messages with tokens
	for i, row := range got {
		if len(row) != 4+2+len(TokenColumns)+1 {
			return ProviderUsage{}, fmt.Errorf("opencode usage row %d column count is invalid", i+1)
		}
		id, err := strconv.ParseInt(strings.TrimSpace(row[0]), 10, 64)
		if err != nil {
			return ProviderUsage{}, fmt.Errorf("opencode usage row %d rowid is not an integer", i+1)
		}
		written, err := strconv.ParseInt(strings.TrimSpace(row[3]), 10, 64)
		if err != nil {
			written = 0 // a time the store did not keep as an integer: the rowid still bounds the read
		}
		seen, mark = max(seen, id), max(mark, written)
		finished, tokens := strings.TrimSpace(row[1]) == "1", strings.TrimSpace(row[2]) == "1"
		delete(kept, id) // read again: what it says now replaces what was kept
		switch {
		case !finished:
			pending[id] = true
			if tokens {
				now = append(now, liveRow{id: id, cells: row[4:]})
			}
		case tokens:
			kept[id] = row[4:]
		}
	}
	u.seen, u.mark, u.kept, u.pending = seen, mark, kept, pending
	if whole {
		u.reads = 1
	} else {
		u.reads++
	}
	all := now
	for id, cells := range kept {
		all = append(all, liveRow{id: id, cells: cells})
	}
	if len(all) == 0 {
		return ProviderUsage{Values: map[string]string{}}, nil
	}
	slices.SortFunc(all, func(a, b liveRow) int { return cmp.Compare(a.id, b.id) })
	rows := make([][]string, len(all))
	for i, r := range all {
		rows[i] = r.cells
	}
	return foldOpenCodeRows(rows)
}

// liveRow is one message's rowid and its messageColumns.
type liveRow struct {
	id    int64
	cells []string
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
func queryOpenCodeWaiting(bin, path string) ([][]string, error) {
	return walWait{
		first:  usageTimeout,
		settle: usageSettleWait,
		pause:  usageSettlePause,
		query: func(path string, limit time.Duration) ([][]string, error) {
			return queryOpenCode(bin, path, limit)
		},
		now:   time.Now,
		sleep: time.Sleep,
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
//
// AND EVERY RETRY IS BOUNDED BY WHAT IS LEFT OF THE WINDOW. A retry is a `sqlite3` run,
// and handing it the tool's whole query timeout made the window a floor instead of a
// ceiling: two slow queries spent ~40s in one sample. The caller is why that matters --
// `supervise` calls ReadProviderUsage synchronously in its select loop, so a read that
// overruns is a stretch of time in which the worker's own deadline and budget cases cannot
// run. A retry is given exactly the remaining window, and one that has nothing left is not
// started at all.
//
// THE WORST CASE, STATED AND TRUE:
//
//	first read     usageTimeout                       20s
//	  its drain    usageWaitDelay, if it is killed     2s
//	the waiting    usageSettleWait                     5s   (all retries share it)
//	  its drain    usageWaitDelay, at most once        2s
//	                                                  ---
//	                                                   29s
//
// The second drain is paid at most once because a drain spends the window too: `now` is
// real time, so whatever a retry spends being killed and drained is time the next
// deadline check sees, and the loop ends as soon as the window is gone. The last retry is
// therefore the only one that can finish past the deadline, and it can overshoot by at
// most one usageWaitDelay. That holds however the drain arises -- a child killed at its
// deadline or one that exited leaving a pipe open -- so the bound does not rest on
// `sqlite3` spawning no children.
func (w walWait) read(path string) ([][]string, error) {
	rows, err := w.query(path, w.first)
	if err == nil {
		return rows, nil
	}
	deadline := w.now().Add(w.settle)
	for {
		if !walPending(path) {
			return nil, err
		}
		if !w.now().Before(deadline) {
			return nil, err
		}
		w.sleep(w.pause)
		// The pause spent part of the window, so the retry is allowed what is left AFTER
		// it -- never the figure taken before it, and never a fresh query timeout.
		remaining := deadline.Sub(w.now())
		if remaining <= 0 {
			return nil, err
		}
		if rows, err = w.query(path, remaining); err == nil {
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

// queryOpenCode runs the one statement, read-only, under the timeout, through the resolved
// `sqlite3` the caller names. The database is the job's own and this tool never writes it:
// `-readonly` is that promise kept by the program that opens it, and `-tabs` is the shape
// the rows come back in.
func queryOpenCode(bin, path string, limit time.Duration) ([][]string, error) {
	return queryOpenCodeSQL(bin, path, messagesSQL, limit)
}

// queryOpenCodeSQL is queryOpenCode with the statement named: messagesSQL, or a LiveUsage
// read's liveSQL.
func queryOpenCodeSQL(bin, path, sql string, limit time.Duration) ([][]string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), limit)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-readonly", "-tabs", path, sql)
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
	cost, costed := new(big.Rat), false
	for rowIndex, row := range rows {
		if len(row) != 2+len(TokenColumns)+1 {
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
		// the message's cost, exactly: a float the store printed, never added as a float
		if cell := strings.TrimSpace(row[2+len(TokenColumns)]); cell != "" && cell != Dash {
			r, ok := new(big.Rat).SetString(cell)
			if !ok || r.Sign() < 0 {
				return ProviderUsage{}, fmt.Errorf("opencode usage row %d column cost is not a nonnegative number", rowIndex+1)
			}
			cost.Add(cost, r)
			costed = true
		}
	}
	if costed {
		values["cost"] = cardcost.Text(cost)
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
