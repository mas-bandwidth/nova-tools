package main

import (
	"flag"
	"fmt"
	"io"
	"path/filepath"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/scaffold"
)

func cmdNewRule(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("new-rule", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", ".", "nova-tools checkout directory")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		return refuse(stderr, " new-rule", "usage: nova-ci new-rule [--root <checkout>] <rule-name>")
	}
	abs, err := filepath.Abs(*root)
	if err != nil {
		return refuse(stderr, " new-rule", oneline.Err(err))
	}
	written, err := scaffold.Rule(abs, fs.Arg(0))
	if err != nil {
		fmt.Fprintf(stderr, "nova-ci new-rule: %v\n", err)
		return 1
	}
	for _, p := range written {
		fmt.Fprintln(stdout, "wrote "+p)
	}
	return 0
}
