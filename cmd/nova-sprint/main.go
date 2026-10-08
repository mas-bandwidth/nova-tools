// nova-sprint manages the bounded sprint table, the per-bench copy of which
// is the live model (nova-tools#2593).
package main

import (
	"context"
	"io"
	"os"
	"time"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, time.Now().UTC()))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, now time.Time) (code int) {
	code = Run(context.Background(), args, stdout, stderr)
	return code
}
