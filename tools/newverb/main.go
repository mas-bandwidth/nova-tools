package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mas-bandwidth/nova-tools/internal/scaffold"
)

func main() {
	fs := flag.NewFlagSet("newverb", flag.ExitOnError)
	root := fs.String("root", ".", "nova-tools checkout directory")
	// ignored: flag.ExitOnError: Parse exits with its own message instead of returning an error
	_ = fs.Parse(os.Args[1:])

	if fs.NArg() != 2 {
		fmt.Fprintln(os.Stderr, "usage: newverb [--root <checkout>] <tool> <verb>")
		os.Exit(2)
	}

	abs, err := filepath.Abs(*root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "newverb: %v\n", err)
		os.Exit(1)
	}

	written, err := verb(abs, fs.Arg(0), fs.Arg(1))
	if err != nil {
		fmt.Fprintf(os.Stderr, "newverb: %v\n", err)
		os.Exit(1)
	}

	for _, p := range written {
		fmt.Println("wrote " + p)
	}
	fmt.Print(scaffold.DispatchNote(fs.Arg(0), fs.Arg(1)))
}

func verb(root, tool, verb string) ([]string, error) {
	outs, err := scaffold.VerbFiles(root, tool, verb)
	if err != nil {
		return nil, err
	}
	return scaffold.Write(root, outs)
}
