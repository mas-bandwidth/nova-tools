// A fake nova-bus, for the tests of nova-wake's bus source.
//
// It is a FAKE rather than the real binary because what nova-wake's bus source
// has to be proved about is its CLASSIFICATION -- every line suppressed,
// relayed or standing, every line counted, and the default case printed -- and
// the transcripts that prove it (an INBOX REFUSED, a line from a future version
// of nova-bus) are ones the real binary cannot be made to print on demand. The
// version string it answers is pinned by the caller, so a test can also prove
// the refusal a wrong version earns.
//
// It is driven by one directory, named in NOVA_WAKE_FAKE_BUS:
//
//	version      what `nova-bus version` prints; default the pinned line
//	out          what an inbox or wait poll prints, for every poll
//	out.<n>      what the nth inbox or wait poll prints, if it exists
//	exit         the exit code for a poll; default 0
//	exit.<n>     the exit code for the nth poll
//	calls        appended to, one line per invocation: the arguments
//	polls        the poll counter, so out.<n> can be chosen
//	delay        milliseconds to block before answering, the way a `wait` does
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func main() {
	dir := os.Getenv("NOVA_WAKE_FAKE_BUS")
	if dir == "" {
		fmt.Fprintln(os.Stderr, "the fake nova-bus needs NOVA_WAKE_FAKE_BUS")
		os.Exit(2)
	}
	args := os.Args[1:]
	appendLine(filepath.Join(dir, "calls"), strings.Join(args, " "))
	if len(args) > 0 && args[0] == "version" {
		v := read(filepath.Join(dir, "version"))
		if v == "" {
			v = "nova-bus v0.10.3 darwin/arm64 go1.27.1"
		}
		fmt.Println(strings.TrimRight(v, "\n"))
		return
	}
	if ms, err := strconv.Atoi(strings.TrimSpace(read(filepath.Join(dir, "delay")))); err == nil && ms > 0 {
		time.Sleep(time.Duration(ms) * time.Millisecond)
	}
	n := bump(filepath.Join(dir, "polls"))
	out := read(filepath.Join(dir, "out."+strconv.Itoa(n)))
	if out == "" {
		out = read(filepath.Join(dir, "out"))
	}
	fmt.Print(out)
	code := read(filepath.Join(dir, "exit."+strconv.Itoa(n)))
	if code == "" {
		code = read(filepath.Join(dir, "exit"))
	}
	if c, err := strconv.Atoi(strings.TrimSpace(code)); err == nil && c != 0 {
		os.Exit(c)
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

func bump(path string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(read(path)))
	n++
	os.WriteFile(path, []byte(strconv.Itoa(n)), 0o644)
	return n
}
