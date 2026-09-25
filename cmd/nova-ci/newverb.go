package main

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/scaffold"
)

func cmdNewVerb(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("new-verb", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "nova-tools checkout directory")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 2 {
		return refuse(stderr, " new-verb", "usage: nova-ci new-verb [--root <checkout>] <tool> <verb>")
	}
	abs, err := filepath.Abs(*root)
	if err != nil {
		return refuse(stderr, " new-verb", oneline.Err(err))
	}
	written, err := scaffold.Verb(abs, fs.Arg(0), fs.Arg(1))
	if err != nil {
		fmt.Fprintf(stderr, "nova-ci new-verb: %v\n", err)
		return 1
	}
	for _, p := range written {
		fmt.Fprintln(stdout, "wrote "+p)
	}
	fmt.Fprint(stdout, scaffold.DispatchNote(fs.Arg(0), fs.Arg(1)))
	return 0
}
