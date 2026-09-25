// table.go: the nova-sprint table verb. The wide table is read from Redis:
// --redis <addr> makes exactly one FCALL_RO ns_snapshot per rendered tick and
// prints it to stdout, or writes it to --out by atomic rename (#3343), and
// --check renders a fixture keyspace. The live layout (table_live.go) is the
// whole sprint table (#3530); its --out is the one published file, rewritten
// by rename once a tick, and `table clear` (#3637) zeroes its landed and done
// columns.
//
// Section 6 of #2756.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// tableWants is the whole table verb, named in every refusal.
const tableWants = "the wide table is read from Redis and written nowhere: --redis <addr> [--sprint <name>] (--once | --loop), or --check --redis <addr>; the whole sprint table is --layout live [--loop 1] [--out <file>]"

type tableOpts struct {
	once    bool
	redis   string
	sprint  string
	check   bool
	live    bool
	loop    bool
	every   time.Duration // --loop <seconds>; 1 s when --loop has no number
	out     string
	lockKey string
	layout  string
	compare string
	friends string
	xyFile  string
}

func cmdTable(args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "clear" {
		return cmdTableClear(args[1:], stdout, stderr)
	}
	args = joinLoopSeconds(args)
	fs := verbflag.New("table")
	var opts tableOpts
	fs.BoolVar(&opts.once, "once", false, "")
	fs.StringVar(&opts.redis, "redis", "", "")
	fs.StringVar(&opts.sprint, "sprint", "", "")
	fs.BoolVar(&opts.check, "check", false, "")
	fs.BoolVar(&opts.live, "live", false, "")
	fs.Var(&loopValue{on: &opts.loop, every: &opts.every}, "loop", "")
	fs.StringVar(&opts.out, "out", "", "")
	fs.StringVar(&opts.lockKey, "lock", "", "")
	fs.StringVar(&opts.layout, "layout", "wide", "")
	fs.StringVar(&opts.compare, "compare", "", "")
	fs.StringVar(&opts.friends, "friends", "", "")
	fs.StringVar(&opts.xyFile, "xy-file", "", "")
	if err := fs.Parse(args); err != nil {
		return tableRefuse(stderr, err.Error()+"; "+tableWants)
	}
	if fs.NArg() > 0 {
		return tableRefuse(stderr, "takes flags, not positional arguments")
	}
	if opts.layout == "live" || opts.compare != "" {
		return cmdTableLive(opts, stdout, stderr)
	}
	if opts.layout != "wide" {
		return tableRefuse(stderr, "--layout wants live or wide")
	}
	if opts.friends != "" || opts.xyFile != "" || opts.lockKey != "" {
		return tableRefuse(stderr, "--friends, --xy-file and --lock belong to --layout live; the wide table takes --out <file>")
	}
	if opts.check {
		if opts.live {
			if opts.loop || opts.once {
				return tableRefuse(stderr, "--check --live takes --redis <addr>, --out <file> and [--sprint <name>]")
			}
			if opts.out == "" {
				return tableRefuse(stderr, "--check --live needs --out <file>")
			}
			return cmdTableCheckLive(opts.redis, opts.out, opts.sprint, stdout, stderr)
		}
		if opts.loop || opts.once || opts.sprint != "" || opts.out != "" {
			return tableRefuse(stderr, "--check takes only --redis <addr>")
		}
		return cmdTableCheck(opts.redis, stdout, stderr)
	}
	if opts.redis == "" {
		return tableRefuse(stderr, "--redis <addr> is required; "+tableWants)
	}
	if opts.loop && opts.once {
		return tableRefuse(stderr, "--redis takes either --once or --loop, not both")
	}
	return cmdTableRedis(opts.redis, opts.sprint, opts.loop, opts.every, opts.out, stdout, stderr)
}

func cmdTableRedis(addr, sprint string, loop bool, every time.Duration, out string, stdout, stderr io.Writer) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	st, err := store.Open(ctx, addr)
	if err != nil {
		return tableRefuse(stderr, err.Error())
	}
	defer st.Close()
	// One FCALL_RO per tick: one consistent server instant per rendered
	// table, never a pipeline of separate reads (6.2).
	tick := func(ctx context.Context) (string, int, error) {
		snap, err := table.ReadNamed(ctx, st.Client(), sprint)
		if err != nil {
			return "", 0, err
		}
		code := 0
		if len(snap.Errors) > 0 {
			code = 1
		}
		return snap.Render(), code, nil
	}
	return tablePublishLoop(ctx, tick, loop, every, out, stdout, stderr)
}

// tableTick renders one table body, the exit code its errors want, and any
// read failure.
type tableTick func(context.Context) (body string, code int, err error)

// tablePublishLoop is the wide table's tick loop. Each tick prints to stdout,
// or with --out writes to a temp file beside out, fsyncs it and renames it
// onto out, so a reader sees the old table or the new one, never half of one
// (#3343). It returns when ctx is done.
func tablePublishLoop(ctx context.Context, tick tableTick, loop bool, every time.Duration, out string, stdout, stderr io.Writer) int {
	render := func() (int, error) {
		body, code, err := tick(ctx)
		if err != nil {
			return 0, err
		}
		if out == "" {
			if _, err := io.WriteString(stdout, body); err != nil {
				return 0, err
			}
		} else if err := writeAtomic(out, body); err != nil {
			return 0, err
		}
		return code, nil
	}
	if !loop {
		code, err := render()
		if err != nil {
			return tableRefuse(stderr, err.Error())
		}
		return code
	}
	ticker := time.NewTicker(tickEvery(every))
	defer ticker.Stop()
	for {
		if _, err := render(); err != nil {
			fmt.Fprintf(stderr, "nova-sprint table: %s; next tick\n", oneline.Escape(err.Error()))
		}
		select {
		case <-ctx.Done():
			return 0
		case <-ticker.C:
		}
	}
}

func cmdTableCheck(addr string, stdout, stderr io.Writer) int {
	if addr == "" {
		return tableRefuse(stderr, "--check needs --redis <addr>, a throwaway server for the fixture keyspace")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, addr)
	if err != nil {
		return tableRefuse(stderr, err.Error())
	}
	defer st.Close()
	// An empty store is seeded with the fixture; a live fleet is refused (#3253).
	if _, err := table.PrepareCheck(ctx, st.Client()); err != nil {
		return tableRefuse(stderr, err.Error())
	}
	snap, err := table.Read(ctx, st.Client())
	if err != nil {
		return tableRefuse(stderr, err.Error())
	}
	body := snap.Render()
	if _, err := io.WriteString(stdout, body); err != nil {
		return tableRefuse(stderr, err.Error())
	}
	if len(snap.Errors) > 0 {
		fmt.Fprintf(stderr, "nova-sprint table --check: %s\n", oneline.Escape(strings.Join(snap.Errors, "; ")))
		return 1
	}
	if body != table.DefectGolden() {
		return tableRefuse(stderr, "--check: rendered output is not the fixture output")
	}
	return 0
}

// cmdTableCheckLive is --check --live (#3253): the --out file is younger
// than table.LiveWindow and every cell equals a direct read in the same
// second; the receipt is one line.
func cmdTableCheckLive(addr, out, sprint string, stdout, stderr io.Writer) int {
	if addr == "" {
		return tableRefuse(stderr, "--check --live needs --redis <addr>")
	}
	ctx := context.Background()
	st, err := store.Open(ctx, addr)
	if err != nil {
		return tableRefuse(stderr, err.Error())
	}
	defer st.Close()
	receipt, err := table.CheckLive(ctx, st.Client(), out, sprint, time.Now())
	if err != nil {
		fmt.Fprintf(stderr, "nova-sprint table %s\n", oneline.Escape(err.Error()))
		return 1
	}
	fmt.Fprintln(stdout, receipt)
	return 0
}

// loopValue is --loop: bare, it is a loop at one tick a second (the form the
// wide table always took); --loop <seconds> sets the tick.
type loopValue struct {
	on    *bool
	every *time.Duration
}

func (v *loopValue) IsBoolFlag() bool { return true }

func (v *loopValue) String() string {
	if v == nil || v.on == nil || !*v.on {
		return "false"
	}
	return v.every.String()
}

func (v *loopValue) Set(s string) error {
	switch s {
	case "true":
		*v.on, *v.every = true, time.Second
		return nil
	case "false":
		*v.on, *v.every = false, 0
		return nil
	}
	secs, err := strconv.ParseFloat(s, 64)
	if err != nil || secs <= 0 || secs > 3600 {
		return fmt.Errorf("--loop wants seconds between ticks (0 < n <= 3600), got %q", s)
	}
	*v.on, *v.every = true, time.Duration(secs*float64(time.Second))
	return nil
}

// joinLoopSeconds turns `--loop 1` into `--loop=1`: a boolean-shaped flag
// never consumes the next argument, and the loops table writes the space.
func joinLoopSeconds(args []string) []string {
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		a := args[i]
		if (a == "--loop" || a == "-loop") && i+1 < len(args) {
			if _, err := strconv.ParseFloat(args[i+1], 64); err == nil {
				out = append(out, a+"="+args[i+1])
				i++
				continue
			}
		}
		out = append(out, a)
	}
	return out
}

func tableRefuse(stderr io.Writer, what string) int {
	return refuse(stderr, "table", what)
}
