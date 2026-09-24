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

	written, err := scaffold.Verb(abs, fs.Arg(0), fs.Arg(1))
	if err != nil {
		fmt.Fprintf(os.Stderr, "newverb: %v\n", err)
		os.Exit(1)
	}

	for _, p := range written {
		fmt.Println("wrote " + p)
	}
	fmt.Print(scaffold.DispatchNote(fs.Arg(0), fs.Arg(1)))
}
