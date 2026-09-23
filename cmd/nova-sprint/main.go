package main

import (
	"context"
	"os"
)

// A release may stamp this with -ldflags; buildinfo resolves other builds.
var version string

func main() { os.Exit(run(context.Background(), os.Args[1:], os.Stdout, os.Stderr)) }
