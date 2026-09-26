// table_live.go: `nova-sprint table --layout live` (#3530), the whole sprint
// table from Redis once a tick, and `--compare` (#2674), the Go port of
// rowan-tools bin/sprint-table-redis diffed against the file that script
// publishes, until the switch.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// cmdTableLive is `--layout live`: the whole sprint table (#3530) from the
// ws index, bench:* and friend:* keys (internal/nsprint/table/sprint.go).
// --compare keeps the #2674 port, the byte-for-byte twin of the bash of
// record, until that script is retired.
func cmdTableLive(opts tableOpts, stdout, stderr io.Writer) int {
	if opts.compare != "" {
		return cmdTableCompare(opts, stdout, stderr)
	}
	var problems []string
	addr := taskAddr(opts.redis)
	if addr == "" {
		problems = append(problems, "--layout live needs --redis <addr> (or NOVA_SPRINT_REDIS / NOVA_REDIS_ADDR)")
	}
	if opts.check {
		problems = append(problems, "--layout live takes no --check")
	}
	if opts.xyFile != "" {
		problems = append(problems, "--xy-file belongs to --compare")
	}
	if opts.loop && opts.once {
		problems = append(problems, "--loop takes no --once")
	}
	if opts.lockKey != "" && (!opts.loop || opts.out == "") {
		problems = append(problems, "--lock names the writer lock of --loop --out")
	}
	if len(problems) > 0 {
		return tableRefuse(stderr, strings.Join(problems, "; "))
	}
	cfg := table.SprintConfig{Sprint: opts.sprint, Friends: splitRoster(opts.friends)}
	if cfg.Sprint == "" {
		cfg.Sprint = os.Getenv("NOVA_SPRINT")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if !opts.loop {
		st, err := store.Open(ctx, addr)
		if err != nil {
			return tableRefuse(stderr, err.Error())
		}
		defer st.Close()
		snap, err := table.NewSprintReader(st.Client(), cfg).Read(ctx, time.Now())
		if err != nil {
			return tableRefuse(stderr, err.Error())
		}
		snap.LastGood = time.Now()
		return publishTable(opts.out, snap.Render(time.Now()), stdout, stderr)
	}
	return loopTable(ctx, addr, cfg, opts, stdout, stderr)
}

// defaultTableLock is the one writer lock of the published table.
const defaultTableLock = "lock:nova-sprint-table"

// loopTable renders once a tick until SIGINT/SIGTERM. With --out it is the
// table's one writer: it takes the lock key before the first publish, keeps
// it alive in every tick's own pipeline, refuses (exit 3) when another
// writer holds it, and publishes by rename; stdout gets one start line, not a
// table a second (a unit's log is a file too). Without --out every tick is
// printed. A tick whose read fails publishes the last good rows with a stale
// line; stderr hears about a failure once, and once more on recovery.
func loopTable(ctx context.Context, addr string, cfg table.SprintConfig, opts tableOpts, stdout, stderr io.Writer) int {
	token := ""
	if opts.out != "" {
		host, _ := os.Hostname()
		token = fmt.Sprintf("%s:%d:%d", host, os.Getpid(), time.Now().UnixNano())
		cfg.LockKey, cfg.LockToken = opts.lockKey, token
		if cfg.LockKey == "" {
			cfg.LockKey = defaultTableLock
		}
		cfg.LockTTL = 5 * tickEvery(opts.every)
	}
	var st *store.Store
	var reader *table.SprintReader
	locked := false
	defer func() {
		if st != nil {
			if locked {
				_ = table.ReleaseLock(context.Background(), st.Client(), cfg.LockKey, token)
			}
			_ = st.Close()
		}
	}()
	ticker := time.NewTicker(tickEvery(opts.every))
	defer ticker.Stop()
	var last *table.SprintSnapshot
	failing := false
	for {
		now := time.Now()
		var snap *table.SprintSnapshot
		var err error
		if st == nil {
			if st, err = store.Open(ctx, addr); err != nil {
				st = nil
			}
		}
		if st != nil && cfg.LockKey != "" && !locked {
			holder, lerr := table.AcquireLock(ctx, st.Client(), cfg.LockKey, token, cfg.LockTTL)
			switch {
			case lerr != nil:
				err = lerr
			case holder != token:
				fmt.Fprintf(stderr, "nova-sprint table: REFUSED: %s is held by %s; one table writer at a time\n", cfg.LockKey, oneline.Escape(holder))
				return 3
			default:
				locked = true
				fmt.Fprintf(stdout, "TABLE loop out=%s every=%s lock=%s\n", opts.out, tickEvery(opts.every), cfg.LockKey)
			}
		}
		if st != nil && (cfg.LockKey == "" || locked) {
			if reader == nil {
				reader = table.NewSprintReader(st.Client(), cfg)
			}
			snap, err = reader.Read(ctx, now)
		}
		if snap != nil && snap.LockLost {
			fmt.Fprintf(stderr, "nova-sprint table: REFUSED: %s no longer holds this writer's token; exiting\n", cfg.LockKey)
			locked = false
			return 3
		}
		if snap != nil {
			snap.LastGood = now
			last = snap
			if failing {
				fmt.Fprintln(stderr, "nova-sprint table: Redis answers again")
				failing = false
			}
		} else {
			snap = table.FailedSprint(cfg, last)
			if !failing && ctx.Err() == nil {
				what := "Redis did not answer"
				if err != nil {
					what = err.Error()
				}
				fmt.Fprintf(stderr, "nova-sprint table: %s; stale table published until it answers\n", oneline.Escape(what))
				failing = true
			}
		}
		if ctx.Err() != nil {
			return 0
		}
		body := snap.Render(now)
		if opts.out == "" {
			_, _ = io.WriteString(stdout, body)
		} else if err := writeAtomic(opts.out, body); err != nil {
			fmt.Fprintf(stderr, "nova-sprint table: %s\n", oneline.Escape(err.Error()))
		}
		select {
		case <-ctx.Done():
			return 0
		case <-ticker.C:
		}
	}
}

func tickEvery(every time.Duration) time.Duration {
	if every <= 0 {
		return time.Second
	}
	return every
}

// publishTable prints body, or writes it to out by rename.
func publishTable(out, body string, stdout, stderr io.Writer) int {
	if out == "" {
		_, _ = io.WriteString(stdout, body)
		return 0
	}
	if err := writeAtomic(out, body); err != nil {
		return tableRefuse(stderr, err.Error())
	}
	return 0
}

// writeAtomic writes body to <path>.tmp.<pid> in path's own directory, fsyncs
// it, and renames it over path: same directory so the rename is atomic, and a
// pid in the name so two writers never share a temp file (#3343). A reader
// sees the old table or the new one, never half of one.
func writeAtomic(path, body string) error {
	dir, base := filepath.Split(path)
	if dir == "" {
		dir = "."
	}
	tmp := filepath.Join(dir, fmt.Sprintf("%s.tmp.%d", base, os.Getpid()))
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("--out: %w", err)
	}
	if _, err := io.WriteString(f, body); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("--out: %w", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("--out: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("--out: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("--out: %w", err)
	}
	return nil
}

func splitRoster(list string) []string {
	return strings.FieldsFunc(list, func(r rune) bool { return r == ',' || r == ' ' })
}

// cmdTableCompare is `--compare <file>` (#2674): the bash-parity render of
// the live Redis diffed against the file the bash of record publishes.
func cmdTableCompare(opts tableOpts, stdout, stderr io.Writer) int {
	var problems []string
	if opts.redis == "" {
		problems = append(problems, "--compare needs --redis <addr>")
	}
	if opts.sprint == "" {
		problems = append(problems, "--compare needs --sprint <name> (sprint:<name>:xy and :landed)")
	}
	if opts.friends == "" {
		problems = append(problems, "--compare needs --friends <a,b,...>, the roster in display order")
	}
	if opts.check || opts.loop || opts.once || opts.out != "" {
		problems = append(problems, "--compare takes none of --check, --loop, --once, --out")
	}
	if len(problems) > 0 {
		return tableRefuse(stderr, strings.Join(problems, "; "))
	}
	cfg := table.LiveConfig{Sprint: opts.sprint, XYFile: opts.xyFile, Friends: splitRoster(opts.friends), RowStale: 10 * time.Second}
	ctx := context.Background()
	st, err := store.Open(ctx, opts.redis)
	if err != nil {
		return tableRefuse(stderr, err.Error())
	}
	defer st.Close()
	return compareLive(ctx, st, cfg, opts.compare, stdout, stderr)
}

// compareLive renders from the live Redis and diffs against path's content:
// MATCH and exit 0, or a unified diff and exit 1. The table under comparison
// is rewritten once a second from counts that move every second, so compare
// first waits (at most comparePublishWait) for the writer's next publish of
// path and reads Redis the moment it lands: both renders then come from the
// same second. A file nobody rewrites is compared as it stands.
func compareLive(ctx context.Context, st *store.Store, cfg table.LiveConfig, path string, stdout, stderr io.Writer) int {
	waitForPublish(path, comparePublishWait)
	snap, err := table.ReadLive(ctx, st.Client(), cfg)
	if err != nil {
		return tableRefuse(stderr, err.Error())
	}
	wantBytes, err := os.ReadFile(path)
	if err != nil {
		return tableRefuse(stderr, "--compare: "+err.Error())
	}
	want := maskVolatile(string(wantBytes))
	got := maskVolatile(snap.RenderLive(time.Now()))
	if got == want {
		fmt.Fprintln(stdout, "MATCH (load and age masked)")
		return 0
	}
	_, _ = io.WriteString(stdout, unifiedDiff(path, "nova-sprint table --layout live", want, got))
	return 1
}

func maskVolatile(s string) string {
	var out []string
	inHost := false
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, "host       |") {
			inHost = true
		} else if strings.HasPrefix(l, "total      |") && inHost {
			inHost = false
		} else if strings.HasPrefix(l, "friend     |") {
			inHost = false
		}
		if inHost && len(l) > 62 && !strings.HasPrefix(l, "-----------") && !strings.HasPrefix(l, "host ") {
			l = l[:62] + " MASKED"
		}
		if strings.HasPrefix(l, "stale: ") {
			if idx := strings.Index(l, "s ("); idx != -1 {
				l = "stale: MASKED" + l[idx:]
			}
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n")
}

const comparePublishWait = 3 * time.Second

// waitForPublish returns once path is replaced (a new inode or mtime: the
// writers publish by rename) or after limit.
func waitForPublish(path string, limit time.Duration) {
	before, err := os.Stat(path)
	if err != nil {
		return
	}
	stop := time.Now().Add(limit)
	for time.Now().Before(stop) {
		time.Sleep(5 * time.Millisecond)
		now, err := os.Stat(path)
		if err == nil && (!os.SameFile(before, now) || !now.ModTime().Equal(before.ModTime())) {
			return
		}
	}
}

// unifiedDiff is a whole-file unified diff (one hunk, 3 lines of context
// folded to the changed span), enough to read a table of ~30 lines.
func unifiedDiff(aName, bName, a, b string) string {
	al, bl := splitLines(a), splitLines(b)
	// longest common subsequence over lines
	n, m := len(al), len(bl)
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if al[i] == bl[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else if lcs[i+1][j] >= lcs[i][j+1] {
				lcs[i][j] = lcs[i+1][j]
			} else {
				lcs[i][j] = lcs[i][j+1]
			}
		}
	}
	var body strings.Builder
	i, j := 0, 0
	for i < n || j < m {
		switch {
		case i < n && j < m && al[i] == bl[j]:
			body.WriteString(" " + al[i] + "\n")
			i, j = i+1, j+1
		case i < n && (j == m || lcs[i+1][j] >= lcs[i][j+1]):
			body.WriteString("-" + al[i] + "\n")
			i++
		default:
			body.WriteString("+" + bl[j] + "\n")
			j++
		}
	}
	return fmt.Sprintf("--- %s\n+++ %s\n@@ -1,%d +1,%d @@\n%s", aName, bName, n, m, body.String())
}

func splitLines(s string) []string {
	s = strings.TrimSuffix(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}
