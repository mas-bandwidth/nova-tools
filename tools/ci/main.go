// Command ci holds the verbs this repository's own CI and Makefile call: the
// programs that were shell steps and scripts, in Go, each with a test that pins
// what it does. A workflow step or a Makefile target calls one verb and passes
// it the values the workflow knows; the logic is here.
//
// Each verb lives in its own file and registers itself with register in that
// file's init, so a verb is added by adding a file.
//
//	example:
//	  go run ./tools/ci help
//	  go run ./tools/ci <verb> [flags]
package main

import (
	"fmt"
	"io"
	"maps"
	"os"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/tools/internal/runframe"
)

const tool = "ci"

// cmdline is what a refusal tells the reader to run.
const cmdline = "go run ./tools/ci"

// env is everything a verb reads from outside itself, so a test runs a verb
// with none of the process's own: its streams, its environment, its directory.
type env struct {
	stdout, stderr io.Writer
	getenv         func(string) string
	dir            string // the working directory; "" means the process's own
}

// Out and Err are the streams the runframe dispatch frame reads.
func (e env) Out() io.Writer { return e.stdout }
func (e env) Err() io.Writer { return e.stderr }

// verb is one command: its name, a one-line summary for the banner, the help
// text `ci help <verb>` prints, and the function that runs it. do returns the
// process's exit code: 0 done, 1 the thing checked is not so, 2 refused (bad
// usage, or the verb could not run).
type verb struct {
	name    string
	summary string
	help    string
	do      func(e env, args []string) int
}

// Help and Do present a verb to the runframe dispatch frame.
func (v verb) Help() string                { return v.help }
func (v verb) Do(e env, args []string) int { return v.do(e, args) }

var registry = map[string]verb{}

// register adds a verb. A second verb under one name is a programming error and
// panics at init, before any verb runs.
func register(v verb) {
	if _, dup := registry[v.name]; dup {
		panic("tools/ci: verb registered twice: " + v.name)
	}
	registry[v.name] = v
}

func names() []string {
	return slices.Sorted(maps.Keys(registry))
}

func main() {
	os.Exit(run(os.Args[1:], env{stdout: os.Stdout, stderr: os.Stderr, getenv: os.Getenv}))
}

func banner() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: the verbs this repository's CI and Makefile call\n\nusage: go run ./tools/ci <verb> [flags]\n       go run ./tools/ci help <verb>\n\n", tool)
	for _, n := range names() {
		fmt.Fprintf(&b, "  %-22s %s\n", n, registry[n].summary)
	}
	return b.String()
}

func run(args []string, e env) int {
	return runframe.Run(e, args, tool, cmdline, banner, names(), registry)
}
