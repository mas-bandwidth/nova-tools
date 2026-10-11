package main

// bench.go is `nova-ci bench run`: one command on a Linux bench against a copy
// of a local tree, the recipe every brief used to type out by hand
// (pkg/bench holds the run; this file is its flags and its lines). It is
// nova-ci's first verb on pkg/tool: the skeleton parses its flags and
// renders its refusals, and the verb prints only the command's own output and
// its one CI BENCH line (Flags.Prints).

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

	"github.com/mas-bandwidth/nova-tools/pkg/bench"
	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/tool"
)

// benchEffect is what `bench run` does beyond printing, the last line of its -h.
const benchEffect tool.Effect = "delivery: copies the tree to a run directory on the bench, runs the command there and removes that directory"

// benchTool is the tool behind `nova-ci bench`: one verb, run over t, which
// runs argv, the words after --. The skeleton parses the flags before --; a
// word there that is no flag is refused as a positional argument.
func benchTool(t bench.Transport, argv []string) *tool.Tool {
	return &tool.Tool{
		Name:      "nova-ci",
		What:      "nova-ci bench runs one command on a Linux bench against a copy of a local tree.",
		How:       "make a run directory on the bench, copy the tree into it, run the command\nunder nice with the bench's cache, then remove that directory and nothing else.",
		ExitTable: exitTable("bench run"),
		Verbs: []tool.Verb{{
			Name:      "bench run",
			Usage:     "bench run --host <h> [--fallback <h>] --dir <tree> [--root <dir>] [--cache <dir>] [--with-git] -- <go command>",
			Effect:    benchEffect,
			ExitTable: exitTable("bench run"),
			Flags:     benchRunFlags,
			Run:       func(c *tool.Call) *tool.Out { return runBench(c, t, argv) },
		}},
	}
}

// cmdBench is `nova-ci bench <verb>`; run is the one verb. The words after
// the first -- are the command, never flags of the verb.
func cmdBench(ctx context.Context, args []string, stdin io.Reader, stdout, stderr io.Writer, t bench.Transport) int {
	head, argv := args, []string(nil)
	if i := slices.Index(args, "--"); i >= 0 {
		head, argv = args[:i], args[i+1:]
	}
	return benchTool(t, argv).RunContext(ctx, append([]string{"bench"}, head...), stdin, stdout, stderr)
}

func benchRunFlags(f *tool.Flags) {
	f.Prints()
	f.Required("host", "the bench the command runs on, a host ssh reaches")
	f.String("fallback", "", "the bench tried when --host does not answer (ssh itself fails); never tried when --host answered")
	f.Required("dir", "the local tree copied to the bench, its .git left out")
	f.String("root", bench.DefaultRoot, "where on the bench the run directory is made, relative to the login's home or absolute")
	f.String("cache", bench.DefaultCache, "GOCACHE on the bench, relative to the login's home or absolute; shared by every run there")
	f.Bool("with-git", false, "copy the tree's .git as well, for a command that reads history")
	f.Check(func(c *tool.Call) {
		if c.Str("fallback") != "" && c.Str("fallback") == c.Str("host") {
			c.Problem("--fallback names the same bench as --host")
		}
		if dir := c.Str("dir"); dir != "" {
			if info, err := os.Stat(dir); err != nil || !info.IsDir() {
				c.Problem(fmt.Sprintf("--dir %s is not a directory here", oneline.Quote(dir)))
			}
		}
	})
}

// runBench is `nova-ci bench run --host <h> [--fallback <h>] --dir <tree> --
// <command>`. Its exit status is the command's; 2 is a run that never reached
// the command (usage, no bench answered, the copy failed), said in one REFUSED
// line. The command's output is its own on stdout and stderr; the run's own
// lines (CI BENCH PASSED for a host passed over, CI BENCH for the run) go to
// stderr, so stdout is exactly the command's.
func runBench(c *tool.Call, t bench.Transport, argv []string) *tool.Out {
	if len(argv) == 0 {
		return tool.Refuse("no command after --; give the go command to run, such as -- go test ./cmd/nova-ci/")
	}
	host, fallback := c.Str("host"), c.Str("fallback")
	hosts := []string{host}
	if fallback != "" {
		hosts = append(hosts, fallback)
	}
	abs, err := filepath.Abs(c.Str("dir"))
	if err != nil {
		return tool.Refuse(oneline.Err(err))
	}
	o := bench.Options{Hosts: hosts, Dir: abs, Root: c.Str("root"), Cache: c.Str("cache"), WithGit: c.Bool("with-git"), Argv: argv,
		Stdout: c.Stdout, Stderr: c.Stderr, Notes: c.Stderr}
	if err := o.Validate(); err != nil {
		out := tool.Refuse(err.Error())
		out.Remedy = "nova-ci bench run -h"
		return out
	}
	res, err := bench.Run(c.Ctx, t, o)
	if err != nil {
		if res.RunDir != "" {
			fmt.Fprintf(c.Stderr, "CI BENCH host=%s run=%s exit=- removed=%s\n", res.Host, res.RunDir, removedWord(res))
		}
		out := tool.Refuse(oneline.Err(err))
		out.Remedy = "nova-ci bench run -h"
		if errors.Is(err, bench.ErrNoBench) {
			out.Remedy = "ssh " + host + " true"
		}
		return out
	}
	fmt.Fprintf(c.Stderr, "CI BENCH host=%s run=%s exit=%d removed=%s\n", res.Host, res.RunDir, res.Code, removedWord(res))
	return tool.Exit(res.Code)
}

func removedWord(res bench.Result) string {
	if res.Removed {
		return "yes"
	}
	return "no reason=" + oneline.Quote(oneline.Err(res.RemoveErr))
}
