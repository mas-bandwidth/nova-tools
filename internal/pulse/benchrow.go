package pulse

// `nova-pulse row`: THIS bench pushes its own swarm-table row into the fleet Redis, once a
// second, with a five-second TTL, so a bench that stops pushing VANISHES from the table
// rather than going stale on it (nova-tools #2561, replacing bin/bench-row).
//
// Glenn 2026-09-22 renamed the table: the per-bench table is the SWARM TABLE, and a SPRINT
// is the bounded task set whose x/y goes on its header (#2593).
//
// The row is `bench:<host>` = {host, queue, working, done, ok, fail, load1, ncpu, at}. Three
// things the bash could not do are done here:
//
//   - The password never reaches a second process. bin/bench-row re-exec'd itself under
//     nova-secrets exec and handed REDISCLI_AUTH to a redis-cli; this reads the variable
//     nova-secrets exec already put in the environment and hands it to the client.
//   - The pusher is one process with one loop. bin/bench-row piped a `while :` into
//     redis-cli, which had to be restarted by hand on every bench three times on the night
//     of 2026-09-21 when the pipe's far end died quietly.
//   - A push that fails is SAID. The bash discarded redis-cli's output to /dev/null, so a
//     bench whose ACL had lapsed looked exactly like a bench with nothing to do.
//
// THE COUNT IS A FLOOR AND SAYS SO. A finished card's job directory is deleted at card end
// (the fleet's hygiene rule), and its RESULT.md survives under <results>/<label>/. Both are
// counted, exactly as bin/bench-row and sprint-table's count() did, and a card whose result
// was swept from both places is not counted by anything -- which is why this is the bench's
// own floor and never the fleet's total of record.
//
// NO EVIDENCE IS NOT NEGATIVE EVIDENCE (Stella's HOLD 7 at 7bba9a90). Each counted cell has
// three answers, never two: the count, `-` when its source is intentionally not there (no
// queue given, no slot store, no sprint stamp), and `?` when the source is there and could
// not be read. A permission or I/O error used to come out as 0, and an unreadable queue or
// lease store rendered as a healthy idle bench. The row is still pushed with a `?` in it,
// so the bench's presence on the table is not lost with its count, and the reason travels
// with the row as `unavailable: <source>: <err>`.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/swarm"
)

// BenchRowTTL is how long a pushed row lives. It is five times the default interval: a
// bench that misses one push is still on the table, and a bench that has stopped is off it
// within five seconds.
const BenchRowTTL = 5 * time.Second

// DefaultRowInterval is the once-a-second push Glenn asked for on 2026-09-21.
const DefaultRowInterval = time.Second

// BenchKeyPrefix is the namespace a bench may write under its ACL user. The fleet-wide
// stuck-DONE total shares it (`bench:stuck_done`) for exactly that reason and is NOT a
// host row; the table skips it by name.
const BenchKeyPrefix = "bench:"

// StuckDoneKey is the one key under BenchKeyPrefix that is not a bench.
const StuckDoneKey = "bench:stuck_done"

// The two marks a counted cell carries when there is no count to show.
const (
	// CellAbsent is a source that is intentionally not there: no queue was given, the bench
	// has no slot store, there is no sprint. It adds nothing to a total.
	CellAbsent = "-"
	// CellUnavailable is a source that is there and could not be read, or a pushed count
	// that will not parse. It makes its column's total unknown too.
	CellUnavailable = "?"
)

// BenchRow is one bench's row: what it pushes and what the table reads back.
type BenchRow struct {
	Host    string
	Queue   int
	Working int
	Done    int
	OK      int
	Fail    int
	// QueueMark, WorkingMark and ResultsMark are "" when the column's count stands, and
	// CellAbsent or CellUnavailable when it does not; the int beside a mark is 0 and is
	// never shown. ResultsMark covers done, ok and fail, which one reader counts together.
	QueueMark   string
	WorkingMark string
	ResultsMark string
	// Unavailable is one `<source>: <err>` for each source that could not be read.
	Unavailable []string
	// Load1 is the one-minute load as the machine printed it, kept as text so a table
	// shows what was read rather than a reformatting of it.
	Load1 string
	NCPU  int
	At    string
}

// OKPct is the ok share of done, floored at 0 when nothing is done. It is integer division,
// the same as the shell's, so one fleet's number matches the table it replaces.
func (r BenchRow) OKPct() int {
	if r.Done <= 0 {
		return 0
	}
	return 100 * r.OK / r.Done
}

// cellText is a count as the row and the table print it: the mark when there is one.
func cellText(n int, mark string) string {
	if mark != "" {
		return mark
	}
	return strconv.Itoa(n)
}

// The cells as text: what the ROW line prints, what is pushed, what the table shows.
func (r BenchRow) QueueCell() string   { return cellText(r.Queue, r.QueueMark) }
func (r BenchRow) WorkingCell() string { return cellText(r.Working, r.WorkingMark) }
func (r BenchRow) DoneCell() string    { return cellText(r.Done, r.ResultsMark) }
func (r BenchRow) OKCell() string      { return cellText(r.OK, r.ResultsMark) }
func (r BenchRow) FailCell() string    { return cellText(r.Fail, r.ResultsMark) }
func (r BenchRow) OKPctCell() string   { return cellText(r.OKPct(), r.ResultsMark) }

// SourceError is a source a counter could not read: which one, and why.
type SourceError struct {
	Source string // queue, slots, since, roots or results
	Err    error
}

func (e *SourceError) Error() string { return e.Source + ": " + e.Err.Error() }
func (e *SourceError) Unwrap() error { return e.Err }

// markOf is the cell mark for a counter's answer: unavailable on any error, absent when the
// source was not there, and none when the count stands.
func markOf(present bool, err error) string {
	switch {
	case err != nil:
		return CellUnavailable
	case !present:
		return CellAbsent
	}
	return ""
}

// notThere is the one answer a reader treats as "this optional source is absent": the path
// does not exist, or a component of it is a file (which is the same answer a glob gives).
// Everything else -- permission, I/O -- is a source that is there and could not be read.
func notThere(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

// BenchRowInput is one `nova-pulse row` invocation.
type BenchRowInput struct {
	Store    StoreOptions
	Host     string
	Interval time.Duration
	Once     bool
	// Print measures this bench and prints the row WITHOUT a store and without pushing.
	// It is the answer to "why does my bench say 0 done?", which under bin/bench-row could
	// only be asked by reading the script and re-running its find by hand.
	Print bool

	// Where this bench's work is. Every one of these is a flag with a default under
	// <home>/nova-bench: no path in this file is baked, because a verb carrying one
	// session's queue directory in its source would freeze the scripts it exists to retire.
	Queue    string   // the bench queue dir; its ready/ and ready-pro/ hold the waiting cards
	Slots    string   // the nova-swarm slots store
	Results  string   // <results>/<label>/RESULT.md, where a finished card's result survives
	Roots    []string // job roots; <root>/<dir>/*/jobs/*/RESULT.md for dirs newer than Since
	Since    string   // the SPRINT-START stamp file; counting is from its mtime
	Textfile string   // optional node_exporter textfile-collector path

	Stdout io.Writer
	Stderr io.Writer
	Now    func() time.Time
	Sleep  func(time.Duration)

	// Push is the seam: a test hands in its own sink and opens no socket. The production
	// default writes the hash and its TTL to the fleet store.
	Push func(ctx context.Context, row BenchRow) error
}

// Row pushes this bench's row until it is stopped, or once with --once.
func Row(in BenchRowInput) int {
	stdout, stderr := in.Stdout, in.Stderr
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	now := in.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	sleep := in.Sleep
	if sleep == nil {
		sleep = time.Sleep
	}
	interval := in.Interval
	if interval <= 0 {
		interval = DefaultRowInterval
	}

	// Named sendRow and not `push`: internal/pulse has a package-level `push` leaf (the
	// git push behind the secret scan), and a local of that name makes this function look
	// like a fifth publish site to the class test that guards every path to a forge.
	sendRow := in.Push
	if sendRow == nil && in.Print {
		sendRow = func(context.Context, BenchRow) error { return nil }
	}
	if sendRow == nil {
		rdb, err := DialStore(context.Background(), in.Store)
		if err != nil {
			fmt.Fprintf(stderr, "ROW REFUSED store: %s\n", oneline.Err(err))
			return 2
		}
		defer func() { _ = rdb.Close() }()
		sendRow = func(ctx context.Context, row BenchRow) error {
			key := BenchKeyPrefix + row.Host
			pipe := rdb.TxPipeline()
			pipe.HSet(ctx, key,
				"host", row.Host,
				"queue", row.QueueCell(),
				"working", row.WorkingCell(),
				"done", row.DoneCell(),
				"ok", row.OKCell(),
				"fail", row.FailCell(),
				"load1", row.Load1,
				"ncpu", row.NCPU,
				"at", row.At,
				// Always written, empty when every source read: HSET does not clear a
				// field it is not given, and a reason from a second ago must not outlive
				// the fault on a row that is pushed every second.
				"unavailable", strings.Join(row.Unavailable, "\n"),
			)
			pipe.Expire(ctx, key, BenchRowTTL)
			_, err := pipe.Exec(ctx)
			return err
		}
	}

	pushed, failed := 0, 0
	lastFail := ""
	lastWhy := ""
	for {
		row := ReadBenchRow(in, now())
		// An unreadable source is SAID: on the first tick, and again whenever the set of
		// reasons changes, rather than once a second into a daemon's log.
		why := strings.Join(row.Unavailable, "; ")
		if why != lastWhy || in.Once || in.Print {
			for _, u := range row.Unavailable {
				fmt.Fprintf(stderr, "ROW UNAVAILABLE bench=%s unavailable: %s\n", oneline.Field(row.Host), u)
			}
			if why == "" && lastWhy != "" {
				fmt.Fprintf(stderr, "ROW AVAILABLE bench=%s: every source read\n", oneline.Field(row.Host))
			}
			lastWhy = why
		}
		if in.Textfile != "" {
			if err := WriteBenchTextfile(in.Textfile, row); err != nil {
				fmt.Fprintf(stderr, "ROW TEXTFILE %s: %s\n", oneline.Field(in.Textfile), oneline.Err(err))
			}
		}
		if err := sendRow(context.Background(), row); err != nil {
			failed++
			// A push that fails is SAID, every time, because a bench whose ACL lapsed
			// used to look exactly like a bench with nothing to do.
			lastFail = oneline.Err(err)
			fmt.Fprintf(stderr, "ROW PUSH FAILED bench=%s: %s\n", oneline.Field(row.Host), lastFail)
		} else {
			pushed++
		}
		if in.Once || in.Print {
			// A count nobody took is a DASH, never a zero: --print pushed nothing, and
			// printing pushed=0 there would read as a push that failed silently.
			pushedWord := strconv.Itoa(pushed)
			if in.Print {
				pushedWord = "-"
			}
			unavailable := ""
			if len(row.Unavailable) > 0 {
				sources := make([]string, 0, len(row.Unavailable))
				for _, u := range row.Unavailable {
					src, _, _ := strings.Cut(u, ":")
					sources = append(sources, src)
				}
				unavailable = " unavailable=" + strings.Join(sources, ",")
			}
			fmt.Fprintf(stdout, "ROW bench=%s queue=%s working=%s done=%s ok=%s fail=%s load1=%s ncpu=%d pushed=%s failed=%d%s\n",
				oneline.Field(row.Host), row.QueueCell(), row.WorkingCell(), row.DoneCell(), row.OKCell(), row.FailCell(),
				oneline.Field(row.Load1), row.NCPU, pushedWord, failed, unavailable)
			if failed > 0 {
				return 3
			}
			// The row was pushed, `?` and all, and the exit still says a source could not
			// be read: a script that checks the exit is not told this bench is fine.
			if len(row.Unavailable) > 0 {
				return 4
			}
			return 0
		}
		sleep(interval)
	}
}

// ReadBenchRow measures this bench once. A counter that could not read its source leaves a
// `?` in its cell and its reason in Unavailable; it never leaves a 0.
func ReadBenchRow(in BenchRowInput, now time.Time) BenchRow {
	row := BenchRow{
		Host:  in.Host,
		NCPU:  runtime.NumCPU(),
		Load1: ReadLoad1(),
		At:    now.UTC().Format(time.RFC3339),
	}
	say := func(err error) {
		if err != nil {
			row.Unavailable = append(row.Unavailable, oneline.Err(err))
		}
	}

	n, present, err := countReadyCards(in.Queue)
	row.Queue, row.QueueMark = n, markOf(present, err)
	say(err)

	n, present, err = countLiveLeases(in.Slots, now)
	row.Working, row.WorkingMark = n, markOf(present, err)
	say(err)

	c, present, err := CountResults(in.Roots, in.Results, in.Since)
	row.Done, row.OK, row.Fail, row.ResultsMark = c.Done, c.OK, c.Fail, markOf(present, err)
	say(err)
	return row
}

// readyDirs are the two queues a bench pulls from, under its queue directory. They are
// named here and nowhere else.
var readyDirs = []string{"ready", "ready-pro"}

// countReadyCards counts the cards waiting on this bench: the .md files in <queue>/ready
// and <queue>/ready-pro. One of the two not being there is an ordinary bench (no pro
// queue); NEITHER being there is an absent queue (`-`), and any other error reading either
// is an unreadable one (`?`), never a zero.
func countReadyCards(queue string) (n int, present bool, err error) {
	if strings.TrimSpace(queue) == "" {
		return 0, false, nil
	}
	for _, d := range readyDirs {
		entries, rerr := os.ReadDir(filepath.Join(queue, d))
		if rerr != nil {
			if notThere(rerr) {
				continue
			}
			return 0, true, &SourceError{Source: "queue", Err: rerr}
		}
		present = true
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				n++
			}
		}
	}
	return n, present, nil
}

// countLiveLeases is the `working` column: the leases in this bench's slot store that are
// live right now. bin/bench-row shelled `nova-swarm slots list | grep -c state=live`; this
// reads the same store through the same package, so the two never disagree.
//
// LIVE ONLY, not DRIFT. A DRIFT lease -- past its until= with its process still running --
// is held for admission purposes (internal/pulse/fillstore.go says so), but the bash this
// replaces counted `state=live` and this column is a picture of what is running now.
//
// A store directory that is not there is absent; a store that is there with no lease taken
// yet is a real 0; a store that cannot be read is unavailable.
func countLiveLeases(store string, now time.Time) (n int, present bool, err error) {
	if strings.TrimSpace(store) == "" {
		return 0, false, nil
	}
	if _, serr := os.Stat(store); serr != nil {
		if notThere(serr) {
			return 0, false, nil
		}
		return 0, true, &SourceError{Source: "slots", Err: serr}
	}
	leases, lerr := swarm.ListSlotLeases(store, now)
	if lerr != nil {
		return 0, true, &SourceError{Source: "slots", Err: lerr}
	}
	for _, l := range leases {
		if l.State(now) == "live" {
			n++
		}
	}
	return n, true, nil
}

// failPatterns are the RESULT.md lines that make a finished card a FAIL.
//
// ONE SET FOR BOTH SOURCES. bin/bench-row used a narrower set for the job roots than for
// the results directory: `sprint-requeue`, `bench-sweep` and `RESULT: SILENT` counted as
// failures under <results> and as OK under a job root, so the same card changed column when
// its job directory was swept. This is the wider set, applied to both, and it is the one
// intentional difference from the counts the bash printed.
var failPatterns = regexp.MustCompile(`(?m)^(written-by: (nova-swarm native|sprint-requeue|bench-sweep)|RESULT: (BLOCKED|FAILED|RED|SILENT))`)

// ResultCounts is what CountResults found: the finished cards, split into ok and fail.
type ResultCounts struct {
	Done, OK, Fail int
}

// CountResults counts the cards this bench finished since the sprint started, and splits
// them into ok and fail.
//
// The sprint stamp is a FILE and its mtime is the clock: `<since>` is
// <home>/nova-bench/SPRINT-START, written when the sprint is lined up. With no stamp there
// is no sprint and the answer is ABSENT (present=false) -- never "everything ever", which on
// a bench with a year of job roots is a number nobody can act on, and never a 0, which reads
// as a sprint in which nothing finished.
//
// With a stamp, a job root or results directory that is not there contributes nothing (a
// sprint where no card has finished yet has none), and the count is a real number. Any
// other error on the stamp or on any directory or RESULT.md the count walks is a
// *SourceError naming since, roots or results, and no partial count is returned: a floor
// with an unknown hole in it is not a floor. The walk is done directory by directory rather
// than by filepath.Glob, because Glob skips a directory it cannot read without a word --
// which is the very zero this refuses.
func CountResults(roots []string, results, since string) (c ResultCounts, present bool, err error) {
	if strings.TrimSpace(since) == "" {
		return c, false, nil
	}
	st, serr := os.Stat(since)
	if serr != nil {
		if notThere(serr) {
			return c, false, nil
		}
		return c, true, &SourceError{Source: "since", Err: serr}
	}
	start := st.ModTime()

	// count reads one RESULT.md. One that vanished between the listing and the read was
	// swept at card end, which is ordinary; one that cannot be read is not.
	count := func(path string) error {
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			if notThere(rerr) || errors.Is(rerr, syscall.EISDIR) {
				return nil
			}
			return rerr
		}
		c.Done++
		if failPatterns.Match(raw) {
			c.Fail++
		} else {
			c.OK++
		}
		return nil
	}

	// The job roots: <root>/<dir>/*/jobs/*/RESULT.md, for the immediate children of each
	// root whose own mtime is newer than the stamp. The shape is bin/bench-row's find.
	for _, root := range roots {
		if strings.TrimSpace(root) == "" {
			continue
		}
		if werr := countJobRoot(root, start, count); werr != nil {
			return ResultCounts{}, true, &SourceError{Source: "roots", Err: werr}
		}
	}

	// The results directory, where a finished card's RESULT.md survives the deletion of
	// its job directory: <results>/<label>/RESULT.md, newer than the stamp.
	if strings.TrimSpace(results) != "" {
		if werr := countResultsDir(results, start, count); werr != nil {
			return ResultCounts{}, true, &SourceError{Source: "results", Err: werr}
		}
	}
	return c, true, nil
}

// countJobRoot walks <root>/<dir>/*/jobs/*/RESULT.md for the <dir>s newer than start. A
// root, a directory or a jobs/ that is not there holds no result; one that cannot be read is
// an error.
func countJobRoot(root string, start time.Time, count func(string) error) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		if notThere(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			if notThere(err) {
				continue
			}
			return err
		}
		if !info.ModTime().After(start) {
			continue
		}
		dir := filepath.Join(root, e.Name())
		children, err := os.ReadDir(dir)
		if err != nil {
			if notThere(err) {
				continue
			}
			return err
		}
		for _, child := range children {
			jobsDir := filepath.Join(dir, child.Name(), "jobs")
			jobs, err := os.ReadDir(jobsDir)
			if err != nil {
				if notThere(err) {
					continue
				}
				return err
			}
			for _, j := range jobs {
				if err := count(filepath.Join(jobsDir, j.Name(), "RESULT.md")); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

// countResultsDir counts <results>/<label>/RESULT.md newer than start.
func countResultsDir(results string, start time.Time, count func(string) error) error {
	entries, err := os.ReadDir(results)
	if err != nil {
		if notThere(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		path := filepath.Join(results, e.Name(), "RESULT.md")
		info, err := os.Stat(path)
		if err != nil {
			if notThere(err) {
				continue
			}
			return err
		}
		if !info.ModTime().After(start) {
			continue
		}
		if err := count(path); err != nil {
			return err
		}
	}
	return nil
}

// WriteBenchTextfile writes the same counts as a node_exporter textfile-collector file
// (nova-tools #2535 / next-sprint-prep B20), so the cards show up beside the load, memory
// and network collectors. It is ADDITIVE: the Redis push is unchanged by it.
//
// The write is atomic -- a temporary file in the same directory, then a rename -- because
// the collector reads the directory on its own schedule and must never see half a file.
func WriteBenchTextfile(path string, row BenchRow) error {
	var b strings.Builder
	// A count nobody took -- an absent or unreadable source -- is LEFT OUT, which the
	// collector reads as no data. Writing it as 0 would be the idle bench this row refuses.
	metric := func(name, help string, value int, mark string) {
		if mark != "" {
			return
		}
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s gauge\n%s{bench=%q} %d\n", name, help, name, name, row.Host, value)
	}
	metric("nova_cards_queue", "Cards waiting in this bench's ready queues.", row.Queue, row.QueueMark)
	metric("nova_cards_working", "Cards this bench has live right now.", row.Working, row.WorkingMark)
	metric("nova_cards_done", "Cards this bench finished since SPRINT-START.", row.Done, row.ResultsMark)
	metric("nova_cards_ok", "Finished cards that were OK.", row.OK, row.ResultsMark)
	metric("nova_cards_fail", "Finished cards that were BLOCKED, FAILED, RED or SILENT.", row.Fail, row.ResultsMark)

	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".nova-row-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.WriteString(b.String()); err != nil {
		tmp.Close()
		_ = os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Chmod(name, 0o644); err != nil {
		_ = os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		_ = os.Remove(name)
		return err
	}
	return nil
}

// ReadLoad1 is the one-minute load average as text, on both of the fleet's platforms:
// /proc/loadavg on Linux and `sysctl -n vm.loadavg` on darwin, which prints
// `{ 2.45 2.29 2.31 }`. A machine neither reader can answer for gives "-", never 0: a zero
// load reads as an idle bench, and an idle bench is what the fleet spent 2026-09-17 looking
// at while the Studio drowned.
func ReadLoad1() string {
	if raw, err := os.ReadFile("/proc/loadavg"); err == nil {
		if f := strings.Fields(string(raw)); len(f) > 0 {
			return f[0]
		}
	}
	if out, err := sysctlLoadavg(); err == nil {
		f := strings.Fields(strings.NewReplacer("{", " ", "}", " ").Replace(out))
		if len(f) > 0 {
			return f[0]
		}
	}
	return "-"
}
