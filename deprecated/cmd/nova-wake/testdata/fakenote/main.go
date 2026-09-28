// A fake --on-note receiver, for the tests of nova-wake's serve loop.
//
// What has to be proved about serve is what it HANDS the command and how many
// times: ids and nothing else, once per note, and a second time only on a
// person's word or under a declared idempotent receiver. So this records its
// arguments and its whole environment's view of stdin, and answers with an exit
// code the test chooses.
//
// One directory, named in NOVA_WAKE_FAKE_NOTE:
//
//	calls    appended to, one line per invocation: the arguments, exactly
//	stdin    appended to: whatever arrived on stdin, which must be nothing
//	rc       the exit code to answer with; default 0
//	block    if this file exists, wait until it is REMOVED before exiting
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func main() {
	dir := os.Getenv("NOVA_WAKE_FAKE_NOTE")
	if dir == "" {
		fmt.Fprintln(os.Stderr, "the fake receiver needs NOVA_WAKE_FAKE_NOTE")
		os.Exit(2)
	}
	appendLine(filepath.Join(dir, "calls"), strings.Join(os.Args[1:], " "))
	if raw, err := io.ReadAll(os.Stdin); err == nil && len(raw) > 0 {
		appendLine(filepath.Join(dir, "stdin"), string(raw))
	}
	block := filepath.Join(dir, "block")
	for {
		if _, err := os.Stat(block); err != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if rc, err := strconv.Atoi(strings.TrimSpace(read(filepath.Join(dir, "rc")))); err == nil && rc != 0 {
		os.Exit(rc)
	}
}

func read(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(raw)
}

func appendLine(path, line string) {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	fmt.Fprintln(f, line)
}
