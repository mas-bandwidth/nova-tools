// nova-tokens: token spend, folded per day, keyed by (day, model, repo). SKELETON.
package main

import (
	"io"
	"os"
	"time"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr, time.Now().UTC())) }

func run(args []string, stdout, stderr io.Writer, now time.Time) int { return 2 }
