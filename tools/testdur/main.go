// Command testdur turns `go test -json` into the two-minute rule's assertion,
// not a memory. The caller pipes the test step through it:
//
//	set -o pipefail
//	go test -json -count=1 ./... | go run ./tools/testdur
//
// After the packages have run, testdur fails the step when any package took
// over 60 s, or when the step's total (the sum of the packages' elapsed times)
// exceeded 120 s, printing one "TESTDUR FAIL pkg=<p> s=<n> bar=<b>" line per
// offender. A passing step prints one "TESTDUR OK" line naming the slowest
// package. pipefail keeps the real `go test` exit status, so a failing test and
// a slow leg are both red for the reason they actually are.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// packageBar is Glenn's working number: a package answers in a minute.
const packageBar = 60.0

// totalBar is the two-minute ceiling: the step's packages summed must not cross it.
const totalBar = 120.0

// run reads one `go test -json` stream, writes the assertion's verdict to out,
// and reports whether the step crossed the bar (the caller turns that into exit 1).
func run(in io.Reader, out io.Writer) (failed bool) {
	type pkgTime struct {
		pkg  string
		secs float64
	}
	var pkgs []pkgTime
	var total float64

	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 1<<20), 1<<24)
	for sc.Scan() {
		var e struct {
			Action, Package, Test string
			Elapsed               float64
		}
		if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
			continue // a test that prints non-JSON to stdout is not a duration
		}
		if e.Action != "pass" && e.Action != "fail" {
			continue
		}
		if e.Test != "" {
			continue
		}
		pkgs = append(pkgs, pkgTime{pkg: e.Package, secs: e.Elapsed})
		total += e.Elapsed
	}
	if err := sc.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "testdur: reading go test -json: %v\n", err)
		os.Exit(1)
	}

	for _, p := range pkgs {
		if p.secs > packageBar {
			fmt.Fprintf(out, "TESTDUR FAIL pkg=%s s=%d bar=%d\n", p.pkg, int(p.secs), int(packageBar))
			failed = true
		}
	}
	if total > totalBar {
		fmt.Fprintf(out, "TESTDUR FAIL pkg=<total> s=%d bar=%d\n", int(total), int(totalBar))
		failed = true
	}

	if !failed {
		var slowest pkgTime
		for _, p := range pkgs {
			if p.secs > slowest.secs {
				slowest = p
			}
		}
		fmt.Fprintf(out, "TESTDUR OK pkg=%s s=%d\n", slowest.pkg, int(slowest.secs))
	}
	return failed
}

func main() {
	if run(os.Stdin, os.Stdout) {
		os.Exit(1)
	}
}
