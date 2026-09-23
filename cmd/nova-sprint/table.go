// table.go: the nova-sprint table verb. Two cuts share one dispatch:
//
//   - the old restart cut (--fixture/--refresh) keeps its exact behavior;
//   - the one-second cut (--redis <addr>) makes exactly one FCALL_RO
//     ns_snapshot per rendered tick, and --check renders a fixture keyspace.
//
// Section 6 of #2756.
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/table"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/sprinttable"
)

type tableOpts struct {
	once    bool
	fixture string
	out     string
	refresh string
	redis   string
	check   bool
	loop    bool
}

func cmdTable(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("table", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	var opts tableOpts
	fs.BoolVar(&opts.once, "once", false, "")
	fs.StringVar(&opts.fixture, "fixture", "", "")
	fs.StringVar(&opts.out, "out", "", "")
	fs.StringVar(&opts.refresh, "refresh", "", "")
	fs.StringVar(&opts.redis, "redis", "", "")
	fs.BoolVar(&opts.check, "check", false, "")
	fs.BoolVar(&opts.loop, "loop", false, "")
	if err := fs.Parse(args); err != nil {
		return tableRefuse(stderr, err.Error()+"; it wants --once and either --fixture <file> or --refresh pending --out <file>")
	}
	if fs.NArg() > 0 {
		return tableRefuse(stderr, "takes flags, not positional arguments")
	}
	if opts.check {
		if opts.fixture != "" || opts.out != "" || opts.refresh != "" || opts.loop || opts.once {
			return tableRefuse(stderr, "--check takes only --redis <addr>")
		}
		return cmdTableCheck(opts.redis, stdout, stderr)
	}
	if opts.redis != "" {
		if opts.fixture != "" || opts.refresh != "" || (opts.loop && opts.once) {
			return tableRefuse(stderr, "--redis takes either --once or --loop, with optional --out; no --fixture or --refresh")
		}
		return cmdTableRedis(opts.redis, opts.out, opts.loop, stdout, stderr)
	}
	if opts.loop {
		return tableRefuse(stderr, "--loop requires --redis <addr>")
	}
	return cmdTableRestart(opts, fs.NArg(), stdout, stderr)
}

func cmdTableRedis(addr, out string, loop bool, stdout, stderr io.Writer) int {
	ctx := context.Background()
	st, err := store.Open(ctx, addr)
	if err != nil {
		return tableRefuse(stderr, err.Error())
	}
	defer st.Close()
	render := func() (int, error) {
		snap, err := table.Read(ctx, st.Client())
		if err != nil {
			return 0, err
		}
		body := snap.Render()
		if out != "" {
			if err := publishAtomic(out, body); err != nil {
				return 0, err
			}
		}
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

// publishAtomic is the atomic file write of 6.7: temp file then rename.
func publishAtomic(path, body string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// cmdTableRestart is the original restart cut, unchanged.
func cmdTableRestart(opts tableOpts, narg int, stdout, stderr io.Writer) int {
	var problems []string
	if !opts.once {
		problems = append(problems, "--once is required; the one-second loop is not this cut")
	}
	if narg > 0 {
		problems = append(problems, "takes flags, not positional arguments")
	}
	switch opts.refresh {
	case "", "pending":
	default:
		problems = append(problems, "--refresh wants pending, which keeps the last table until the next render is ready")
	}
	if opts.fixture != "" && opts.refresh == "pending" {
		problems = append(problems, "--fixture and --refresh pending disagree; one is a ready render and the other is not")
	}
	if opts.fixture == "" && opts.refresh != "pending" {
		problems = append(problems, "pass --fixture <file> to render a ready table, or --refresh pending with --out to keep the last one")
	}
	if opts.refresh == "pending" && opts.out == "" {
		problems = append(problems, "--refresh pending needs --out, the published table to keep")
	}
	if len(problems) > 0 {
		return tableRefuse(stderr, strings.Join(problems, "; "))
	}

	var next []byte
	ready := false
	if opts.fixture != "" {
		body, err := os.ReadFile(opts.fixture)
		if err != nil {
			return tableRefuse(stderr, "cannot read --fixture "+opts.fixture+": "+err.Error())
		}
		if len(bytes.TrimSpace(body)) == 0 {
			return tableRefuse(stderr, "--fixture is empty; refusing to blank the table")
		}
		next = body
		ready = true
	}
	res, err := sprinttable.Publish(opts.out, next, ready)
	if err != nil {
		return tableRefuse(stderr, err.Error())
	}
	if opts.out == "" {
		if _, err := stdout.Write(res.Body); err != nil {
			return tableRefuse(stderr, err.Error())
		}
		return 0
	}
	if res.Wrote {
		fmt.Fprintf(stdout, "TABLE PUBLISHED out=%s bytes=%d\n", oneline.Field(opts.out), len(res.Body))
		return 0
	}
	fmt.Fprintf(stdout, "TABLE KEPT out=%s bytes=%d reason=%s\n", oneline.Field(opts.out), len(res.Body), oneline.Field(res.Reason))
	if len(res.Body) == 0 {
		return 0
	}
	if _, err := stdout.Write(res.Body); err != nil {
		return tableRefuse(stderr, err.Error())
	}
	if !bytes.HasSuffix(res.Body, []byte("\n")) {
		fmt.Fprintln(stdout)
	}
	return 0
}

func tableRefuse(stderr io.Writer, what string) int {
	return refuse(stderr, "table", what)
}
