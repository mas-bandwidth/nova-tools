package main

import (
	"context"
	"flag"
	"io"
	"sort"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
)

// runFunc is a verb's entry point.
type runFunc = func(context.Context, []string, io.Writer, io.Writer) int

// helpRunner is the entry point -h runs for a noun: a registered verb, or
// the built-in table and refresh.
func helpRunner(noun string) (runFunc, bool) {
	switch noun {
	case "table":
		return func(_ context.Context, args []string, out, errOut io.Writer) int { return cmdTable(args, out, errOut) }, true
	case "refresh":
		return func(_ context.Context, args []string, out, errOut io.Writer) int { return cmdRefresh(args, out, errOut) }, true
	}
	v, ok := verbs[noun]
	return v.Run, ok
}

// helpAsked is whether -h, -help or --help comes before any "--".
func helpAsked(args []string) bool {
	for _, a := range args {
		switch a {
		case "--":
			return false
		case "-h", "-help", "--help":
			return true
		}
	}
	return false
}

// helpFor prints the usage of the path args spell (the noun and the
// subverbs the table knows, flags and values dropped) and exits 2: the
// path's forms and examples from verbflag.Usages and the flags of the flag
// set the path's own -h reaches.
func helpFor(run runFunc, args []string, out io.Writer) int {
	path := verbflag.Resolve(args)
	if path == "" {
		path = args[0]
	}
	verbflag.WriteHelp(out, path, helpFlags(run, path))
	return 2
}

// helpFlags runs the path's verb with only its subverb words and -h, and
// returns the flag set whose Parse raised verbflag.Help; nil for a noun with
// no entry of its own and for a path that takes no flags, which are never
// run. Nothing a verb prints on the way is shown.
func helpFlags(run runFunc, path string) (fs *flag.FlagSet) {
	u, ok := verbflag.Usages[path]
	if !ok || u.NoFlags {
		return nil
	}
	defer func() {
		if r := recover(); r != nil {
			h, isHelp := r.(verbflag.Help)
			if !isHelp {
				panic(r)
			}
			fs = h.FS
		}
	}()
	argv := append(strings.Fields(path)[1:], "-h")
	run(context.Background(), argv, io.Discard, io.Discard)
	return nil
}

// verbNames is every verb, comma-separated: the top level's usage.
func verbNames() string {
	names := []string{"table", "refresh", "version", "help"}
	for n := range verbs {
		names = append(names, n)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
