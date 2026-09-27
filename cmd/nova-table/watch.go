package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
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

func cmdWatch(args []string, stdout, stderr io.Writer) int {
	const verb = "watch"
	fs := verbflag.New(verb)
	addr := redisFlag(fs)
	every := fs.Duration("every", time.Second, "the tick, a duration (1s)")
	out := fs.String("out", "", "publish to this file by atomic rename instead of drawing in place")
	title := fs.String("title", "", "a title line above the tables")
	view := fs.String("view", "", "a stored view: its tables and title, read every frame (view set <name> --tables ...)")
	once := fs.Bool("once", false, "render once and exit, with no clear")
	rf := declareRenderFlags(fs)
	pos, err := parseInterleaved(fs, args)
	if err != nil {
		return refuse(stderr, verb, err.Error())
	}
	if len(pos) != 1 && *view == "" {
		return refuse(stderr, verb, "wants the tables to watch, comma-separated, or --view <name>: watch <table>[,<table>...] [--view <name>] [--every 1s] [--out <file>] [--title <text>] [--once]")
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	st, c, code := client(ctx, verb, *addr, stderr)
	if code != 0 {
		return code
	}
	defer st.Close()
	read := tablesReader(c, names, *title, opts)
	if *view != "" {
		read = viewReader(c, *view, opts)
	}
	if *once {
		text, err := read(ctx)
		if err != nil {
			return storeRefusal(stderr, verb, err)
		}
		return publish(*out, text, stdout, stderr, verb)
	}
	ticker := time.NewTicker(*every)
	defer ticker.Stop()
	return watchLoop(ctx, stdout, stderr, read, ticker.C, time.Now, *out)
}

// tablesReader reads and renders the named tables: one pipeline per tick
// over every reader, including changed shapes, the renders joined by one
// blank line, the title first when there is one.
// viewReader reads the view first, every frame (one extra trip), then its
// tables in one trip, so the tables a tab shows change by a verb and never
// by a restart (Glenn 2026-09-27: "restarting is not cool").
func viewReader(c redis.Cmdable, name string, opts ntable.RenderOpts) func(context.Context) (string, error) {
	return func(ctx context.Context) (string, error) {
		v, err := ntable.ViewGet(ctx, c, name)
		if err != nil {
			return "", err
		}
		if len(v.Tables) == 0 {
			return v.Title + "\n(no tables in view " + name + ")\n", nil
		}
		text, err := tablesReader(c, v.Tables, v.Title, opts)(ctx)
		if err != nil || v.Summary == "" {
			return text, err
		}
		// the summary line under the view (Glenn 2026-09-27: "the x/y z%
		// -> ETA summary we are used to"): the named count column of the
		// first table summed over its rows, over every count summed, and
		// the ETA once a rate exists (the change stream; owed: "-")
		t, err := ntable.Read(ctx, c, v.Tables[0])
		if err != nil {
			return text, nil
		}
		var part, total int64
		for _, r := range t.Rows {
			for k, col := range t.Columns {
				if col.Projection != ntable.Count || k >= len(r.Cells) {
					continue
				}
				total += r.Cells[k].Count
				if col.Name == v.Summary {
					part += r.Cells[k].Count
				}
			}
		}
		pct := "-"
		if total > 0 {
			pct = strconv.FormatFloat(100*float64(part)/float64(total), 'f', 1, 64) + "%"
		}
		return text + fmt.Sprintf("\n%s %d/%d %s -> ETA -\n", v.Summary, part, total, pct), nil
	}
}

func tablesReader(c redis.Cmdable, names []string, title string, opts ntable.RenderOpts) func(context.Context) (string, error) {
	readers := make([]*ntable.Reader, len(names))
	for i, n := range names {
		readers[i] = ntable.NewReader(n)
	}
	return func(ctx context.Context) (string, error) {
		pipe := c.Pipeline()
		cmds := make([]*ntable.ReadCmd, len(readers))
		for i, r := range readers {
			cmds[i] = r.Queue(ctx, pipe)
		}
		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) && !isReplyError(err) {
			return "", err
		}
		tables := make([]ntable.Table, 0, len(readers))
		for _, cmd := range cmds {
			t, _, err := cmd.Result()
			if err != nil {
				return "", err
			}
			tables = append(tables, t)
		}
		return renderAll(title, tables, opts), nil
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
		parts = append(parts, title+"\n")
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
// fsyncs it, and renames it over path, the sprint table's publish
// (cmd/nova-sprint/table_live.go): a reader sees the old text or the new
// one, never half of one.
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
