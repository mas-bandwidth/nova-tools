// table.go: the nova-sprint table verb. The wide table is read from Redis and
// written nowhere (#3326): --redis <addr> makes exactly one FCALL_RO
// ns_snapshot per rendered tick and prints it to stdout, and --check renders
// a fixture keyspace. The live layout (table_live.go) is the whole sprint
// table (#3530); its --out is the one published file, rewritten by rename
// once a tick, and `table clear` (#3637) zeroes its landed and done columns.
//
// Section 6 of #2756.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// tableWants is the whole table verb, named in every refusal.
const tableWants = "the wide table is read from Redis and written nowhere: --redis <addr> [--sprint <name>] (--once | --loop), or --check --redis <addr>; the whole sprint table is --layout live [--loop 1] [--out <file>]"

type tableOpts struct {
	once    bool
	redis   string
	sprint  string
	check   bool
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
	fs := flag.NewFlagSet("table", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	var opts tableOpts
	fs.BoolVar(&opts.once, "once", false, "")
	fs.StringVar(&opts.redis, "redis", "", "")
	fs.StringVar(&opts.sprint, "sprint", "", "")
	fs.BoolVar(&opts.check, "check", false, "")
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
	if opts.friends != "" || opts.xyFile != "" || opts.out != "" || opts.lockKey != "" {
		return tableRefuse(stderr, "--friends, --xy-file, --out and --lock belong to --layout live; the wide table is written nowhere (#3326)")
	}
	if opts.check {
		if opts.loop || opts.once || opts.sprint != "" {
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
	return cmdTableRedis(opts.redis, opts.sprint, opts.loop, opts.every, stdout, stderr)
}

func cmdTableRedis(addr, sprint string, loop bool, every time.Duration, stdout, stderr io.Writer) int {
	ctx := context.Background()
	st, err := store.Open(ctx, addr)
	if err != nil {
		return tableRefuse(stderr, err.Error())
	}
	defer st.Close()
	render := func() (int, error) {
		snap, err := table.ReadNamed(ctx, st.Client(), sprint)
		if err != nil {
			return 0, err
		}
		body := snap.Render()
		if _, err := io.WriteString(stdout, body); err != nil {
			return 0, err
		}
		if len(snap.Errors) > 0 {
			return 1, nil
		}
		return 0, nil
	}
	if !loop {
		code, err := render()
		if err != nil {
			return tableRefuse(stderr, err.Error())
		}
		return code
	}
	// One FCALL_RO per tick: one consistent server instant per rendered
	// table, never a pipeline of separate reads (6.2).
	ticker := time.NewTicker(tickEvery(every))
	defer ticker.Stop()
	for range ticker.C {
		if _, err := render(); err != nil {
			fmt.Fprintf(stderr, "nova-sprint table: %s; next tick\n", oneline.Escape(err.Error()))
		}
	}
	return 0
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
