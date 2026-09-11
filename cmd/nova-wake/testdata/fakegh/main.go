// A fake gh, for the tests of nova-wake's entry source. nova-wake's view of a
// forge is gh's, and a test that talked to a forge would be a test with a
// network in it.
//
// One directory, named in NOVA_WAKE_FAKE_GH:
//
//	<number>.json   what `gh pr view <number> --json ...` prints
//	<number>.exit   an exit code for that entry, so unreadable can be proved
//	calls           appended to, one line per invocation
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func main() {
	dir := os.Getenv("NOVA_WAKE_FAKE_GH")
	if dir == "" {
		fmt.Fprintln(os.Stderr, "the fake gh needs NOVA_WAKE_FAKE_GH")
		os.Exit(2)
	}
	args := os.Args[1:]
	appendLine(filepath.Join(dir, "calls"), strings.Join(args, " "))
	number := ""
	for i, a := range args {
		if a == "view" && i+1 < len(args) {
			number = args[i+1]
		}
	}
	if code := strings.TrimSpace(read(filepath.Join(dir, number+".exit"))); code != "" {
		fmt.Fprintln(os.Stderr, read(filepath.Join(dir, number+".stderr")))
		c, _ := strconv.Atoi(code)
		os.Exit(c)
	}
	out := read(filepath.Join(dir, number+".json"))
	if out == "" {
		out = `{"state":"OPEN","statusCheckRollup":[]}`
	}
	fmt.Print(out)
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
