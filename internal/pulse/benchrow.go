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

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
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

// BenchRow is one bench's row: what it pushes and what the table reads back.
type BenchRow struct {
	Host    string
	Queue   int
	Working int
	Done    int
	OK      int
	Fail    int
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
				"queue", row.Queue,
				"working", row.Working,
				"done", row.Done,
				"ok", row.OK,
				"fail", row.Fail,
				"load1", row.Load1,
				"ncpu", row.NCPU,
				"at", row.At,
			)
			pipe.Expire(ctx, key, BenchRowTTL)
			_, err := pipe.Exec(ctx)
			return err
		}
	}

	pushed, failed := 0, 0
	lastFail := ""
	for {
		row := ReadBenchRow(in, now())
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
			fmt.Fprintf(stdout, "ROW bench=%s queue=%d working=%d done=%d ok=%d fail=%d load1=%s ncpu=%d pushed=%s failed=%d\n",
				oneline.Field(row.Host), row.Queue, row.Working, row.Done, row.OK, row.Fail,
				oneline.Field(row.Load1), row.NCPU, pushedWord, failed)
			if failed > 0 {
				return 3
			}
			return 0
		}
		sleep(interval)
	}
}

// ReadBenchRow measures this bench once.
func ReadBenchRow(in BenchRowInput, now time.Time) BenchRow {
	row := BenchRow{
		Host:  in.Host,
		NCPU:  runtime.NumCPU(),
		Load1: ReadLoad1(),
		At:    now.UTC().Format(time.RFC3339),
	}
	row.Queue = countReadyCards(in.Queue)
	row.Working = countLiveLeases(in.Slots, now)
	row.Done, row.OK, row.Fail = CountResults(in.Roots, in.Results, in.Since)
	return row
}

// readyDirs are the two queues a bench pulls from, under its queue directory. They are
// named here and nowhere else.
var readyDirs = []string{"ready", "ready-pro"}

// countReadyCards counts the cards waiting on this bench: the .md files in <queue>/ready
// and <queue>/ready-pro. A directory that is not there is zero, not an error: a bench with
// no pro queue is an ordinary bench.
func countReadyCards(queue string) int {
	if strings.TrimSpace(queue) == "" {
		return 0
	}
	n := 0
	for _, d := range readyDirs {
		entries, err := os.ReadDir(filepath.Join(queue, d))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(e.Name(), ".md") {
				n++
			}
		}
	}
	return n
}

// countLiveLeases is the `working` column: the leases in this bench's slot store that are
// live right now. bin/bench-row shelled `nova-swarm slots list | grep -c state=live`; this
// reads the same store through the same package, so the two never disagree.
//
// LIVE ONLY, not DRIFT. A DRIFT lease -- past its until= with its process still running --
// is held for admission purposes (internal/pulse/fillstore.go says so), but the bash this
// replaces counted `state=live` and this column is a picture of what is running now.
func countLiveLeases(store string, now time.Time) int {
	if strings.TrimSpace(store) == "" {
		return 0
	}
	leases, err := swarm.ListSlotLeases(store, now)
	if err != nil {
		return 0
	}
	n := 0
	for _, l := range leases {
		if l.State(now) == "live" {
			n++
		}
	}
	return n
}

// failPatterns are the RESULT.md lines that make a finished card a FAIL.
//
// ONE SET FOR BOTH SOURCES. bin/bench-row used a narrower set for the job roots than for
// the results directory: `sprint-requeue`, `bench-sweep` and `RESULT: SILENT` counted as
// failures under <results> and as OK under a job root, so the same card changed column when
// its job directory was swept. This is the wider set, applied to both, and it is the one
// intentional difference from the counts the bash printed.
var failPatterns = regexp.MustCompile(`(?m)^(written-by: (nova-swarm native|sprint-requeue|bench-sweep)|RESULT: (BLOCKED|FAILED|RED|SILENT))`)

// CountResults counts the cards this bench finished since the sprint started, and splits
// them into ok and fail.
//
// The sprint stamp is a FILE and its mtime is the clock: `<since>` is
// <home>/nova-bench/SPRINT-START, written when the sprint is lined up. With no stamp there
// is no sprint and the counts are zero -- never "everything ever", which on a bench with a
// year of job roots is a number nobody can act on.
func CountResults(roots []string, results, since string) (done, ok, fail int) {
	if strings.TrimSpace(since) == "" {
		return 0, 0, 0
	}
	st, err := os.Stat(since)
	if err != nil {
		return 0, 0, 0
	}
	start := st.ModTime()

	count := func(path string) {
		raw, err := os.ReadFile(path)
		if err != nil {
			return
		}
		done++
		if failPatterns.Match(raw) {
			fail++
		} else {
			ok++
		}
	}

	// The job roots: <root>/<dir>/*/jobs/*/RESULT.md, for the immediate children of each
	// root whose own mtime is newer than the stamp. The shape is bin/bench-row's find,
	// walked directly instead of through two processes and an xargs.
	for _, root := range roots {
		if strings.TrimSpace(root) == "" {
			continue
		}
		entries, err := os.ReadDir(root)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			info, err := e.Info()
			if err != nil || !info.ModTime().After(start) {
				continue
			}
			matches, err := filepath.Glob(filepath.Join(root, e.Name(), "*", "jobs", "*", "RESULT.md"))
			if err != nil {
				continue
			}
			for _, m := range matches {
				count(m)
			}
		}
	}

	// The results directory, where a finished card's RESULT.md survives the deletion of
	// its job directory: <results>/<label>/RESULT.md, newer than the stamp.
	if strings.TrimSpace(results) != "" {
		matches, _ := filepath.Glob(filepath.Join(results, "*", "RESULT.md"))
		for _, m := range matches {
			info, err := os.Stat(m)
			if err != nil || !info.ModTime().After(start) {
				continue
			}
			count(m)
		}
	}
	return done, ok, fail
}

// WriteBenchTextfile writes the same counts as a node_exporter textfile-collector file
// (nova-tools #2535 / next-sprint-prep B20), so the cards show up beside the load, memory
// and network collectors. It is ADDITIVE: the Redis push is unchanged by it.
//
// The write is atomic -- a temporary file in the same directory, then a rename -- because
// the collector reads the directory on its own schedule and must never see half a file.
func WriteBenchTextfile(path string, row BenchRow) error {
	var b strings.Builder
	metric := func(name, help string, value int) {
		fmt.Fprintf(&b, "# HELP %s %s\n# TYPE %s gauge\n%s{bench=%q} %d\n", name, help, name, name, row.Host, value)
	}
	metric("nova_cards_queue", "Cards waiting in this bench's ready queues.", row.Queue)
	metric("nova_cards_working", "Cards this bench has live right now.", row.Working)
	metric("nova_cards_done", "Cards this bench finished since SPRINT-START.", row.Done)
	metric("nova_cards_ok", "Finished cards that were OK.", row.OK)
	metric("nova_cards_fail", "Finished cards that were BLOCKED, FAILED, RED or SILENT.", row.Fail)

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
