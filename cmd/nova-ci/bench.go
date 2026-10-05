package main

// bench.go is `nova-ci bench run`: one command on a Linux bench against a copy
// of a local tree, the recipe every brief used to type out by hand
// (internal/bench holds the run; this file is its flags and its lines).

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mas-bandwidth/nova-tools/internal/bench"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// cmdBench is `nova-ci bench <verb>`; run is the one verb.
func cmdBench(args []string, stdout, stderr io.Writer, t bench.Transport) int {
	if len(args) > 0 {
		verbflag.HelpIfAsked(args[:1], "bench")
	}
	if len(args) == 0 || args[0] != "run" {
		what := "a verb is needed after bench; the one verb is run"
		if len(args) > 0 {
			what = fmt.Sprintf("unknown verb %q after bench; the one verb is run", args[0])
		}
		return refuseRun(stderr, " bench", what, "nova-ci bench run -h")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return cmdBenchRun(ctx, args[1:], stdout, stderr, t)
}

// cmdBenchRun is `nova-ci bench run --host <h> [--fallback <h>] --dir <tree>
// -- <command>`. Its exit status is the command's; 2 is a run that never
// reached the command (usage, no bench answered, the copy failed), said in one
// REFUSED line. The command's output is its own on stdout and stderr; the run's
// own lines (CI BENCH PASSED for a host passed over, CI BENCH for the run) go
// to stderr, so stdout is exactly the command's.
func cmdBenchRun(ctx context.Context, args []string, stdout, stderr io.Writer, t bench.Transport) int {
	const where = " bench run"
	fs := flag.NewFlagSet("bench run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	host := fs.String("host", "", "the bench the command runs on, a tailnet host ssh reaches (required)")
	fallback := fs.String("fallback", "", "the bench tried when --host does not answer (ssh itself fails); never tried when --host answered")
	dir := fs.String("dir", "", "the local tree copied to the bench, its .git left out (required)")
	root := fs.String("root", bench.DefaultRoot, "where on the bench the run directory is made, relative to the login's home or absolute")
	cache := fs.String("cache", bench.DefaultCache, "GOCACHE on the bench, relative to the login's home or absolute; shared by every run there")
	withGit := fs.Bool("with-git", false, "copy the tree's .git as well, for a command that reads history")
	if err := verbflag.Parse(fs, args); err != nil {
		return refuse(stderr, where, verbflag.Explain(fs, err))
	}
	argv := fs.Args()
	var bad []string
	if *host == "" {
		bad = append(bad, "--host wants the bench the command runs on")
	}
	if *dir == "" {
		bad = append(bad, "--dir wants the local tree to copy")
	}
	if len(argv) == 0 {
		bad = append(bad, "no command after --; give the go command to run, such as -- go test ./cmd/nova-ci/")
	}
	if *fallback != "" && *fallback == *host {
		bad = append(bad, "--fallback names the same bench as --host")
	}
	if len(bad) == 0 {
		if info, err := os.Stat(*dir); err != nil || !info.IsDir() {
			bad = append(bad, fmt.Sprintf("--dir %s is not a directory here", oneline.Quote(*dir)))
		}
	}
	hosts := []string{*host}
	if *fallback != "" {
		hosts = append(hosts, *fallback)
	}
	abs, _ := filepath.Abs(*dir)
	o := bench.Options{Hosts: hosts, Dir: abs, Root: *root, Cache: *cache, WithGit: *withGit, Argv: argv,
		Stdout: stdout, Stderr: stderr, Notes: stderr}
	if len(bad) == 0 {
		if err := o.Validate(); err != nil {
			bad = append(bad, err.Error())
		}
	}
	if len(bad) > 0 {
		return refuse(stderr, where, strings.Join(bad, "; "))
	}
	res, err := bench.Run(ctx, t, o)
	if err != nil {
		next := "nova-ci bench run -h"
		if errors.Is(err, bench.ErrNoBench) {
			next = "ssh " + *host + " true"
		}
		if res.RunDir != "" {
			fmt.Fprintf(stderr, "CI BENCH host=%s run=%s exit=- removed=%s\n", res.Host, res.RunDir, removedWord(res))
		}
		return refuseRun(stderr, where, oneline.Err(err), next)
	}
	fmt.Fprintf(stderr, "CI BENCH host=%s run=%s exit=%d removed=%s\n", res.Host, res.RunDir, res.Code, removedWord(res))
	return res.Code
}

func removedWord(res bench.Result) string {
	if res.Removed {
		return "yes"
	}
	return "no reason=" + oneline.Quote(oneline.Err(res.RemoveErr))
}
