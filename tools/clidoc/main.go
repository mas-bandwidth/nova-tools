package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"
)

func main() {
	fs := flag.NewFlagSet("clidoc", flag.ExitOnError)
	bin := fs.String("bin", "bin", "directory of built nova-* binaries")
	doc := fs.String("doc", "docs/CLI.md", "path to CLI reference documentation")
	check := fs.Bool("check", false, "check only: fail if docs/CLI.md differs from help output")
	_ = fs.Parse(os.Args[1:])

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	ok, diff, err := ProcessAll(ctx, *doc, *bin, *check)
	if err != nil {
		fmt.Fprintf(os.Stderr, "clidoc: %v\n", err)
		os.Exit(2)
	}

	if *check && !ok {
		fmt.Fprintln(os.Stderr, "docs/CLI.md differs from help output; run: make clidoc")
		if diff != "" {
			fmt.Fprintln(os.Stderr, diff)
		}
		os.Exit(1)
	}

	if !*check {
		fmt.Printf("clidoc: updated %s from binaries in %s\n", *doc, *bin)
	}
}
