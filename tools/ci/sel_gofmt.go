package main

import (
	"bytes"
	"flag"
	"fmt"
	"strings"
)

// gofmtOutOfScope is the directory the gofmt check leaves alone: deprecated code
// is out of scope of the testing drive (deprecated/README.md).
const gofmtOutOfScope = "deprecated/"

func init() {
	register(verb{
		name:    "gofmt",
		summary: "list the files that are not gofmt-clean",
		help: `ci gofmt

Runs gofmt -l over the tree and prints the files it lists, less anything under
deprecated/ (out of scope of the testing drive). gofmt -l prints the unformatted files
and exits 0 either way, so the exit code is not the product: the output is. Exit 1 after
"not gofmt-clean:" and the files, one per line; exit 0 and no output when the tree is
clean. Formatting is a property of the source, not of the platform or the shard, so a
workflow checks it on one leg.

Exit 0 clean, 1 files are not gofmt-clean, 2 gofmt could not run or bad usage.

example:
  go run ./tools/ci gofmt
`,
		do: func(e env, args []string) int { return gofmtVerb(e, args, selRealHost()) },
	})
}

func gofmtVerb(e env, args []string, h selHost) int {
	const name = "gofmt"
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	if code, done := selFlags(e, name, fs, args); done {
		return code
	}
	if fs.NArg() > 0 {
		return selRefuse(e, name, fmt.Sprintf("unexpected argument %q", fs.Arg(0)))
	}
	var out bytes.Buffer
	// gofmt's own exit status is not read, and its stderr (a file that does not
	// parse) is shown.
	if _, err := h.stream(selRoot(e), nil, &out, e.stderr, "gofmt", "-l", "."); err != nil {
		fmt.Fprintf(e.stderr, "gofmt: cannot run gofmt: %v\n", err)
		return 2
	}
	var unformatted []string
	for _, l := range strings.Split(out.String(), "\n") {
		if l != "" && !strings.HasPrefix(l, gofmtOutOfScope) {
			unformatted = append(unformatted, l)
		}
	}
	if len(unformatted) > 0 {
		fmt.Fprintln(e.stdout, "not gofmt-clean:")
		fmt.Fprintln(e.stdout, strings.Join(unformatted, "\n"))
		return 1
	}
	return 0
}
