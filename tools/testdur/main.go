// Command testdur turns `go test -json` into the two-minute rule's evidence: the
// per-package total and every test over five seconds. Glenn's rule (2026-09-10,
// reaffirmed 2026-09-15) is that anything we call out to answers in a minute,
// two at most; a package nobody times drifts past that without anyone noticing.
//
// Usage: go test -json -count=1 ./... | go run ./tools/testdur
//
// Output is one line per slow test, "<pkg> <test> <seconds>", then one line per
// package, "<pkg> TOTAL <seconds>", both sorted slowest first. Nothing else: the
// caller pastes it into docs/TEST-DURATIONS.md.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
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
	for _, rs := range []([]row){tests, pkgs} {
		sort.Slice(rs, func(i, j int) bool { return rs[i].seconds > rs[j].seconds })
		for _, r := range rs {
			name := r.test
			if name == "" {
				name = "TOTAL"
			}
			fmt.Printf("%s %s %.1f\n", r.pkg, name, r.seconds)
		}
	}
}
