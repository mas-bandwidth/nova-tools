package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/ntable"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/redis/go-redis/v9"
)

// watch: the named tables rendered once per --every, in place on a
// terminal (the ANSI home-and-clear sequence, then the text: a console tab
// shows the live table with no shell loop; Glenn 2026-09-27: "create a way
// to render this table to text, efficiently and mechanically, once
// per-second in a console window"), or published to --out by atomic rename
// the way the sprint table is. Efficient and mechanical: exactly one Redis
// pipeline per tick for every named table, including the first tick.
// Each read-only snapshot holds the shape and cells; the screen holds the table and nothing
// else (Glenn: "it should only contain that table data, no bullshit around
// it") -- no clock, no tick time, no key names; a store that did not answer
// leaves the last good text standing with ONE line, stale: <n>s, under it.

// clearScreen is the ANSI home-and-clear sequence.
const clearScreen = "\033[H\033[2J"

// nowMillis is the clock the default cell score reads.
func nowMillis() int64 { return time.Now().UnixMilli() }

func (app *application) cmdWatch(args []string, stdout, stderr io.Writer) int {
	const verb = "watch"
	fs := verbflag.New(verb)
	addr := app.redisFlag(fs)
	every := fs.Duration("every", time.Second, "the tick, a duration (1s)")
	out := fs.String("out", "", "publish to this file by atomic rename instead of drawing in place")
	title := fs.String("title", "", "a title line above the tables")
	view := fs.String("view", "", "a stored view: its tables and title, read every frame (view set <name> --tables ...)")
	once := fs.Bool("once", false, "render once and exit, with no clear")
	checkFlag := fs.Bool("check", false, "run table check every tick; show a stall row on invariant violation")
	rf := declareRenderFlags(fs)
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if (*view != "" && len(pos) != 0) || (*view == "" && len(pos) != 1) {
		return refuse(stderr, verb, "wants the tables to watch, comma-separated, or --view <name>: watch <table>[,<table>...] | --view <name> [--every 1s] [--out <file>] [--title <text>] [--once]")
	}
	var names []string
	if len(pos) == 1 {
		for _, n := range strings.Split(pos[0], ",") {
			if n = strings.TrimSpace(n); n != "" {
				names = append(names, n)
			}
		}
	}
	if len(names) == 0 && *view == "" {
		return refuse(stderr, verb, "wants at least one table name, or --view <name>")
	}
	if *every <= 0 || *every > time.Hour {
		return refuse(stderr, verb, "--every wants a duration between 1ms and 1h, got "+every.String())
	}
	opts, err := rf.opts()
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	// In a resident shell, SIGTERM keeps its process-wide default: terminate
	// immediately, including while reading input, with no following command.
	// Ctrl-C is the watch-local return-to-prompt action. Standalone watch keeps
	// its existing graceful SIGTERM behavior.
	signals := []os.Signal{os.Interrupt}
	if app.shared == nil {
		signals = append(signals, syscall.SIGTERM)
	}
	ctx, stop := signal.NotifyContext(context.Background(), signals...)
	defer stop()
	st, c, code := app.client(ctx, verb, *addr, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	read := tablesReader(c, names, *title, opts, *checkFlag)
	if *view != "" {
		read = viewReader(c, *view, opts, *checkFlag)
	}
	if *once {
		text, err := read(ctx)
		if err != nil {
			return st.refusal(stderr, verb, err)
		}
		return publish(*out, text, stdout, stderr, verb)
	}
	ticker := time.NewTicker(*every)
	defer ticker.Stop()
	return watchLoop(ctx, stdout, stderr, read, ticker.C, time.Now, *out)
}

// tablesReader reads and renders the named tables: one pipeline per tick
// over every reader, including changed shapes, the renders joined by one
// blank line, the title first when there is one. When check is true, it audits
// invariant integrity for each table and appends a stall row on violation.
// viewReader reads the view first, every frame (one extra trip), then its
// tables in one trip, so the tables a tab shows change by a verb and never
// by a restart (Glenn 2026-09-27: "restarting is not cool").
func viewReader(c redis.Cmdable, name string, opts ntable.RenderOpts, check bool) func(context.Context) (string, error) {
	return viewReaderWith(c, name, opts, check, ntable.ViewGet, tableSnapshots, ntable.Check)
}

func viewReaderWith(
	c redis.Cmdable,
	name string,
	opts ntable.RenderOpts,
	check bool,
	viewGet func(context.Context, redis.Cmdable, string) (ntable.View, error),
	snapshotter func(c redis.Cmdable, names []string) func(context.Context) ([]ntable.Table, error),
	checker func(context.Context, redis.Cmdable, string) (ntable.CheckReport, error),
) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		v, err := viewGet(ctx, c, name)
		if err != nil {
			return "", err
		}
		if len(v.Tables) == 0 {
			return oneline.Escape(v.Title) + "\n(no tables in view " + oneline.Escape(name) + ")\n", nil
		}
		// The view's frame (2026-09-27): the time to the second, a blank
		// line, the title, a blank line, the summary line, a blank line, the
		// tables. Nothing else goes in. The summary line is the view's state
		// alone while it has one ("STOPPED", nothing more), else
		// "x/y z% -> ETA" when the view names a done column.
		tables, err := snapshotter(c, v.Tables)(ctx)
		if err != nil {
			return "", err
		}
		var b strings.Builder
		b.WriteString(time.Now().Format("2006-01-02 15:04:05 MST"))
		b.WriteString("\n\n")
		if v.Title != "" {
			b.WriteString(oneline.Escape(v.Title))
			b.WriteString("\n\n")
		}
		// The summary reuses the same snapshot as the table body.
		if line := ntable.SummaryLine(v, tables[0]); line != "" {
			b.WriteString(line)
			b.WriteString("\n\n")
		}
		b.WriteString(renderAll("", tables, opts))
		if check && checker != nil {
			var stalls []string
			for _, t := range tables {
				if _, err := checker(ctx, c, t.Name); err != nil {
					stalls = append(stalls, formatStall(t.Name, err))
				}
			}
			return appendStalls(b.String(), stalls), nil
		}
		return b.String(), nil
	}
}

func tablesReader(c redis.Cmdable, names []string, title string, opts ntable.RenderOpts, check bool) func(context.Context) (string, error) {
	return tablesReaderWith(c, names, title, opts, check, tableSnapshots(c, names), ntable.Check)
}

func tablesReaderWith(
	c redis.Cmdable,
	names []string,
	title string,
	opts ntable.RenderOpts,
	check bool,
	snapshots func(context.Context) ([]ntable.Table, error),
	checker func(context.Context, redis.Cmdable, string) (ntable.CheckReport, error),
) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		tables, err := snapshots(ctx)
		if err != nil {
			return "", err
		}
		text := renderAll(title, tables, opts)
		if check && checker != nil {
			var stalls []string
			for _, t := range tables {
				if _, err := checker(ctx, c, t.Name); err != nil {
					stalls = append(stalls, formatStall(t.Name, err))
				}
			}
			text = appendStalls(text, stalls)
		}
		return text, nil
	}
}

// formatStall formats a table invariant check failure as a stall row:
// stall: <table>: <detail>
// It strips table name prefixes and CLI remedy suffixes, and escapes
// terminal control bytes through oneline.Escape. Never repairs or mutates.
func formatStall(tableName string, err error) string {
	msg := err.Error()
	prefix := "table " + tableName + ": "
	if strings.HasPrefix(msg, prefix) {
		msg = strings.TrimPrefix(msg, prefix)
	} else if strings.HasPrefix(msg, "table \""+tableName+"\": ") {
		msg = strings.TrimPrefix(msg, "table \""+tableName+"\": ")
	}
	if idx := strings.Index(msg, "; run: "); idx != -1 {
		msg = msg[:idx]
	}
	return fmt.Sprintf("stall: %s: %s", tableName, oneline.Escape(msg))
}

// appendStalls appends stall rows to rendered table or view text.
func appendStalls(rendered string, stalls []string) string {
	if len(stalls) == 0 {
		return rendered
	}
	if rendered != "" && !strings.HasSuffix(rendered, "\n") {
		rendered += "\n"
	}
	return rendered + strings.Join(stalls, "\n") + "\n"
}

func tableSnapshots(c redis.Cmdable, names []string) func(context.Context) ([]ntable.Table, error) {
	readers := make([]*ntable.Reader, len(names))
	for i, n := range names {
		readers[i] = ntable.NewReader(n)
	}
	return func(ctx context.Context) ([]ntable.Table, error) {
		pipe := c.Pipeline()
		cmds := make([]*ntable.ReadCmd, len(readers))
		for i, r := range readers {
			cmds[i] = r.Queue(ctx, pipe)
		}
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) && !isReplyError(err) {
			return nil, err
		}
		tables := make([]ntable.Table, 0, len(readers))
		for _, cmd := range cmds {
			t, _, err := cmd.Result()
			if err != nil {
				return nil, err
			}
			tables = append(tables, t)
		}
		return tables, nil
	}
}

func isReplyError(err error) bool {
	var re redis.Error
	return errors.As(err, &re)
}

// renderAll is the title line, then every table's render, one blank line
// between two that print; an empty table prints nothing and leaves no gap.
func renderAll(title string, tables []ntable.Table, opts ntable.RenderOpts) string {
	var parts []string
	if title != "" {
		parts = append(parts, oneline.Escape(title)+"\n")
	}
	for _, t := range tables {
		if t.HiddenTable {
			continue // set --hidden: kept and read, not drawn (Glenn 2026-09-27: "hide it" / "show it again", no restart)
		}
		o := opts
		o.Title = t.Name // every block says which table it is (Glenn 2026-09-27)
		if text := ntable.Render(t, o); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

// watchLoop draws once per tick until ctx ends (a signal: exit 0): in
// place on w (clearScreen then the text) when out is "", else to out by
// atomic rename. A tick whose read fails draws the last good text with one
// stale: line under it, and says why on stderr once, and once more on
// recovery. The ticks and the clock are handed in so a test injects both.
func watchLoop(ctx context.Context, w, stderr io.Writer, read func(context.Context) (string, error), ticks <-chan time.Time, now func() time.Time, out string) int {
	var last string
	var lastGood time.Time
	failing := false
	for {
		text, err := read(ctx)
		if ctx.Err() != nil {
			return 0
		}
		if err == nil {
			last, lastGood = text, now()
			if failing {
				fmt.Fprintln(stderr, "nova-table watch: Redis answers again")
				failing = false
			}
		} else {
			if !failing {
				fmt.Fprintf(stderr, "nova-table watch: %s; the last good table stands until it answers\n", oneline.Escape(err.Error()))
				failing = true
			}
			if lastGood.IsZero() {
				text = "stale: never read\n"
			} else {
				text = last + fmt.Sprintf("stale: %ds\n", int64(now().Sub(lastGood).Seconds()))
			}
		}
		if out == "" {
			if _, err := io.WriteString(w, clearScreen+text); err != nil {
				fmt.Fprintf(stderr, "nova-table watch: stdout: %s\n", oneline.Escape(err.Error()))
				return 1
			}
		} else if err := writeAtomic(out, text); err != nil {
			fmt.Fprintf(stderr, "nova-table watch: %s\n", oneline.Escape(err.Error()))
		}
		select {
		case <-ctx.Done():
			return 0
		case <-ticks:
		}
	}
}

// publish prints text, or writes it to out by rename (--once).
func publish(out, text string, stdout, stderr io.Writer, verb string) int {
	if out == "" {
		if _, err := io.WriteString(stdout, text); err != nil {
			return refuse(stderr, verb, "stdout: "+err.Error())
		}
		return 0
	}
	if err := writeAtomic(out, text); err != nil {
		return refuse(stderr, verb, err.Error())
	}
	return 0
}

// writeAtomic writes body to <path>.tmp.<pid> in path's own directory,
// fsyncs it, and renames it over path, the publish the sprint table used
// (deprecated/cmd/nova-sprint/table_live.go): a reader sees the old text or
// the new one, never half of one.
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
