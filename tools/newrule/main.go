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
	// ignored: flag.ExitOnError: Parse exits with its own message instead of returning an error
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

	written, err := rule(abs, fs.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "newrule: %v\n", err)
		os.Exit(1)
	}

	for _, p := range written {
		fmt.Println("wrote " + p)
	}
}

func rule(root, name string) ([]string, error) {
	outs, err := scaffold.RuleFiles(root, name)
	if err != nil {
		return nil, err
	}
	return scaffold.Write(root, outs)
}
