// nova-loop is one copy of a named loop: a lock the kernel drops when the
// holder dies, a restart counter for node_exporter, then the command.
// It does not install a daemon, a launch agent, or a fleet unit.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/filelock"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// version is the release stamp. A release build sets it with -X. Empty reads
// the binary's own build info.
var version = ""

const usage = `nova-loop: one copy of a named loop, then the command it wraps

how it works: nova-loop takes a lock the kernel drops when the holder dies,
writes a restart counter, and becomes the command. A second live copy is refused.
A lock whose holder is already dead is taken. Nothing here installs a daemon.
first run: nova-loop run --name demo --dir <a directory you can write> -- true

usage:
  nova-loop run --name <name> [--dir <dir>] [--metrics <file>] -- <command> [args...]
  nova-loop version
  nova-loop help

exit codes: 0 the command was started (on unix this process becomes the command); 1 the lock directory or the metrics file could not be written, or the command exited non-zero where the process is not replaced; 2 could not run; 3 a live copy holds the lock

example:
  nova-loop run --name demo --dir ./run -- true
  nova-loop run --name demo --dir ./run --metrics ./run/demo.prom -- true
  nova-loop help
`

func main() {
	code, _ := run(os.Args[1:], os.Stdout, os.Stderr, realExec)
	os.Exit(code)
}

func run(args []string, stdout, stderr io.Writer, exec commandExec) (code int, held *filelock.FileLock) {
	defer recoverHelp(stdout, &code)
	if len(args) == 0 {
		return refuse(stderr, "no verb given; the verbs are run, version, help"), nil
	}
	switch args[0] {
	case "help", "-h", "--help":
		if _, err := io.WriteString(stdout, usage); err != nil {
			return 1, nil
		}
		return 0, nil
	case "version", "--version":
		if _, err := fmt.Fprintln(stdout, buildinfo.Line("nova-loop", version)); err != nil {
			return 1, nil
		}
		return 0, nil
	case "run":
		return runLoop(args[1:], stdout, stderr, exec)
	default:
		return refuse(stderr, "unknown verb "+oneline.Escape(args[0])+"; the verbs are run, version, help"), nil
	}
}

// recoverHelp prints the usage on -h. Nothing has run but flag parsing.
func recoverHelp(stdout io.Writer, code *int) {
	r := recover()
	if r == nil {
		return
	}
	if _, ok := r.(verbflag.Help); !ok {
		panic(r)
	}
	if _, err := io.WriteString(stdout, usage); err != nil {
		*code = 1
		return
	}
	*code = 0
}

func refuse(stderr io.Writer, what string) int {
	fmt.Fprintf(stderr, "nova-loop REFUSED: %s; run: nova-loop help\n", oneline.Escape(what))
	return 2
}
