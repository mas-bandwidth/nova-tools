package main

import (
	"fmt"
	"io"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/guard"
)

func runBench(bench string, loopName string, interval time.Duration, stdout, stderr io.Writer) int {
	switch loopName {
	case "guard":
		in := guard.GuardInput{
			Bench:  bench,
			Stdout: stdout,
			Stderr: stderr,
		}
		for {
			code := guard.Pass(in)
			if code != 0 {
				fmt.Fprintf(stderr, "BENCH GUARD LOOP FAILED: exit %d\n", code)
			}
			time.Sleep(interval)
		}
	default:
		fmt.Fprintf(stderr, "RUNBENCH REFUSED: unknown loop %q (supported loops: guard)\n", loopName)
		return 2
	}
}
