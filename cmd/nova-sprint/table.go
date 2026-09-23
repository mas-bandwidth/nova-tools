// table.go: the nova-sprint table verb. The table is read from Redis and
// written nowhere (#3326): --redis <addr> makes exactly one FCALL_RO
// ns_snapshot per rendered tick and prints it to stdout, and --check renders
// a fixture keyspace. There is no published file and no "last table" kept on
// disk; a restarted unit re-renders from Redis on its next tick.
//
// Section 6 of #2756.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// tableWants is the whole table verb, named in every refusal.
const tableWants = "the table is read from Redis and written nowhere: --redis <addr> [--sprint <name>] (--once | --loop), or --check --redis <addr>"

type tableOpts struct {
	once   bool
	redis  string
	sprint string
	check  bool
	loop   bool
}

func cmdTable(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("table", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	var opts tableOpts
	fs.BoolVar(&opts.once, "once", false, "")
	fs.StringVar(&opts.redis, "redis", "", "")
	fs.StringVar(&opts.sprint, "sprint", "", "")
	fs.BoolVar(&opts.check, "check", false, "")
	fs.BoolVar(&opts.loop, "loop", false, "")
	if err := fs.Parse(args); err != nil {
		return tableRefuse(stderr, err.Error()+"; "+tableWants)
	}
	if fs.NArg() > 0 {
		return tableRefuse(stderr, "takes flags, not positional arguments")
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
	return cmdTableRedis(opts.redis, opts.sprint, opts.loop, stdout, stderr)
}

func cmdTableRedis(addr, sprint string, loop bool, stdout, stderr io.Writer) int {
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
	ticker := time.NewTicker(time.Second)
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

func tableRefuse(stderr io.Writer, what string) int {
	return refuse(stderr, "table", what)
}
