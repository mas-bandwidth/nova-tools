// Command testdur turns `go test -json` into the two-minute rule's evidence: the
// per-package total and every test over five seconds. Glenn's rule (2026-09-10,
// reaffirmed 2026-09-15) is that anything we call out to answers in a minute,
// two at most; a package nobody times drifts past that without anyone noticing.
//
// Usage: go test -json -count=1 ./... | go run ./tools/testdur
//
// Output is the two tables docs/TEST-DURATIONS.md holds for one bench, each
// under a heading naming the <goos>/<goarch> THIS RUN was measured on, so a
// recording carries its platform from the moment it is taken and a paste can
// never land under the wrong bench. Tests over five seconds come first, then
// the per-package totals, both sorted slowest first.
//
// The platform label is not decoration. Since the record grew a section per
// bench, the budget a package is judged against is its own platform's
// (tools/testdur's TestFastSuiteUnderOneMinute), and a table filed under the
// wrong heading is a ceiling applied to the wrong machine.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"sort"
)

// slowTestSeconds is the threshold #516 names for a test worth listing by name.
const slowTestSeconds = 5.0

type row struct {
	pkg, test string
	seconds   float64
}

func main() {
	var tests, pkgs []row
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 0, 1<<20), 1<<24)
	for in.Scan() {
		var e struct {
			Action, Package, Test string
			Elapsed               float64
		}
		if err := json.Unmarshal(in.Bytes(), &e); err != nil {
			continue // a test that prints non-JSON to stdout is not a duration
		}
		if e.Action != "pass" && e.Action != "fail" {
			continue
		}
		if e.Test == "" {
			pkgs = append(pkgs, row{pkg: e.Package, seconds: e.Elapsed})
		} else if e.Elapsed >= slowTestSeconds {
			tests = append(tests, row{pkg: e.Package, test: e.Test, seconds: e.Elapsed})
		}
	}
	if err := in.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "testdur: reading go test -json: %v\n", err)
		os.Exit(1)
	}
	platform := runtime.GOOS + "/" + runtime.GOARCH
	sort.Slice(tests, func(i, j int) bool { return tests[i].seconds > tests[j].seconds })
	sort.Slice(pkgs, func(i, j int) bool { return pkgs[i].seconds > pkgs[j].seconds })

	fmt.Printf("TESTS OVER FIVE SECONDS (%s)\n", platform)
	for _, r := range tests {
		fmt.Printf("%s %s %.1f\n", r.pkg, r.test, r.seconds)
	}
	fmt.Printf("\nTOTALS (%s)\n", platform)
	for _, r := range pkgs {
		fmt.Printf("%s TOTAL %.1f\n", r.pkg, r.seconds)
	}
}
