package main

import "bytes"

// runWith runs one nova-config invocation against deps and returns its code
// and both streams. It is not in looprun_test.go: that file is
// //go:build !windows, and backup_test.go calls this on every OS.
func runWith(d deps, args ...string) (int, string, string) {
	var out, errb bytes.Buffer
	code := run(args, &out, &errb, d)
	return code, out.String(), errb.String()
}
