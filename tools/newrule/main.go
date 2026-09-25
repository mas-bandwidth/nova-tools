package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mas-bandwidth/nova-tools/internal/scaffold"
)

func main() {
	fs := flag.NewFlagSet("newrule", flag.ExitOnError)
	root := fs.String("root", ".", "nova-tools checkout directory")
	_ = fs.Parse(os.Args[1:])

	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "usage: newrule [--root <checkout>] <rule-name>")
		os.Exit(2)
	}

	abs, err := filepath.Abs(*root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "newrule: %v\n", err)
		os.Exit(1)
	}

	written, err := scaffold.Rule(abs, fs.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "newrule: %v\n", err)
		os.Exit(1)
	}

	for _, p := range written {
		fmt.Println("wrote " + p)
	}
}
