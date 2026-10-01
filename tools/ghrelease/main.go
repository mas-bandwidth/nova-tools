// Command ghrelease holds the verbs a GitHub release runs: the ldflags a build
// is stamped with, the build of every shipped tool for one platform, the
// checksums over the whole shipped set, the assertion that every binary
// reports the tag it was built from, the gate that asks whether a green
// certification run vouches for a commit, and the upload that attaches the set
// to a release draft and never to a published release. release.yml and
// certification.yml call one verb per step and pass it the values the
// workflow knows; the logic is here, under unit tests.
//
// The platforms a release ships are the file release-targets, embedded in the
// tool; the tools it ships are every directory of cmd/.
//
// Each verb lives in its own file and registers itself with register in that
// file's init, so a verb is added by adding a file.
//
//	example:
//	  go run ./tools/ghrelease help
//	  go run ./tools/ghrelease ldflags v1.2.3
//	  go run ./tools/ghrelease build --require-v-tag v1.2.3 linux amd64 dist
package main

import (
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

const tool = "ghrelease"

// env is everything a verb reads from outside itself, so a test runs a verb
// with none of the process's own: its streams, its environment, its directory,
// the programs it runs and the GitHub it asks.
type env struct {
	stdout, stderr io.Writer
	getenv         func(string) string
	dir            string // the working directory; "" means the process's own

	// run starts the programs a verb runs (go, git, a built binary); nil means
	// the operating system's.
	run runner
	// gh is the GitHub a verb asks; nil means the gh command line.
	gh ghClient
	// targets is the shipped platforms; nil means the embedded release-targets.
	targets []target
	// legacy names the tools exempt from the stamp assertion; nil means the
	// package's own list, legacyNoVersionVerb.
	legacy []string
}

// verb is one command: its name, a one-line summary for the banner, the help
// text `ghrelease help <verb>` prints, and the function that runs it. do
// returns the process's exit code: 0 done, 1 the thing checked is not so (a
// refusal), 2 refused as used (bad usage, or the verb could not run).
type verb struct {
	name    string
	summary string
	help    string
	do      func(e env, args []string) int
}

var registry = map[string]verb{}

// register adds a verb. A second verb under one name is a programming error and
// panics at init, before any verb runs.
func register(v verb) {
	if _, dup := registry[v.name]; dup {
		panic("tools/ghrelease: verb registered twice: " + v.name)
	}
	registry[v.name] = v
}

func names() []string {
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func main() {
	os.Exit(run(os.Args[1:], env{stdout: os.Stdout, stderr: os.Stderr, getenv: os.Getenv}))
}

func banner() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s: the verbs a GitHub release runs\n\nusage: go run ./tools/ghrelease <verb> [args]\n       go run ./tools/ghrelease help <verb>\n\n", tool)
	for _, n := range names() {
		fmt.Fprintf(&b, "  %-12s %s\n", n, registry[n].summary)
	}
	return b.String()
}

func run(args []string, e env) int {
	if len(args) == 0 {
		fmt.Fprint(e.stderr, banner())
		return 2
	}
	switch args[0] {
	case "help", "-h", "--help":
		if len(args) > 1 && args[0] == "help" {
			v, ok := registry[args[1]]
			if !ok {
				fmt.Fprintf(e.stderr, "%s help: unknown verb %s; run: go run ./tools/ghrelease help\n", tool, oneline.Quote(args[1]))
				return 2
			}
			fmt.Fprint(e.stdout, v.help)
			return 0
		}
		fmt.Fprint(e.stdout, banner())
		return 0
	}
	v, ok := registry[args[0]]
	if !ok {
		fmt.Fprintf(e.stderr, "%s: unknown verb %s; the verbs are %s; run: go run ./tools/ghrelease help\n", tool, oneline.Quote(args[0]), strings.Join(names(), ", "))
		return 2
	}
	return v.do(e, args[1:])
}

// root is the directory a verb's relative paths are read from.
func (e env) root() string {
	if e.dir == "" {
		return "."
	}
	return e.dir
}

// requireEnv returns the named variables' values, or names the first one that
// is unset or empty and reports the verb could not run.
func (e env) requireEnv(verbName string, names ...string) (map[string]string, bool) {
	vals := make(map[string]string, len(names))
	for _, n := range names {
		v := e.getenv(n)
		if v == "" {
			fmt.Fprintf(e.stderr, "%s %s: %s is not set; the workflow hands it in the environment\n", tool, verbName, n)
			return nil, false
		}
		vals[n] = v
	}
	return vals, true
}
