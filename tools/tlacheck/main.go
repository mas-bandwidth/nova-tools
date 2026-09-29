// Command tlacheck runs the TLA+ model checks of this repository: the declared
// cases of tla/CASES.tsv with their run records, the table model's suites, the
// replay of the table model's findings against a table.lua, and the execution
// replay of table.lua receipts against EpochMemberTable.
//
// It is a bench tool: TLC needs java, the replays need redis-server, and the
// runs are bounded (docs/SPEC-CI.md, the tlc job). Run it on a bench, never on
// a working machine. The reading of TLC's results and the suites live in
// internal/tlc and internal/tablemodel; this file is the command line.
//
//	example:
//	  tlacheck groups --root .
//	  tlacheck run --root . --jar /path/to/tla2tools.jar --dir /tmp/tlc-out --group tablefirstcontact
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/tlc"
)

const tool = "tlacheck"

// env is everything the command reads from outside itself, so the tests run it
// with none of it: no java, no redis-server, no environment, no clock.
type env struct {
	stdout, stderr io.Writer
	getenv         func(string) string
	lookPath       func(string) (string, error)
	hostname       func() (string, error)
	javaVersion    func(java string) (string, error) // the version `java -version` reports
	exec           tlc.Executor
	goos           string
	tmpDir         string // where a disposable store's directory goes; the system default when empty
}

func main() {
	os.Exit(run(os.Args[1:], env{
		stdout: os.Stdout, stderr: os.Stderr,
		getenv: os.Getenv, lookPath: tlc.LookPath, hostname: os.Hostname, javaVersion: javaVersion,
		exec: tlc.Execute, goos: hostOS,
	}))
}

// javaVersion runs the java it is given with -version, bounded, and returns
// the version it reports. It runs java, so it belongs on a bench.
func javaVersion(java string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return tlc.ReadJavaVersion(ctx, java)
}

type verb struct {
	name    string
	summary string
	help    string
	do      func(e env, args []string) int
}

func verbs() []verb {
	return []verb{
		{"run", "run the declared cases of tla/CASES.tsv and write the run records", helpRun, cmdRun},
		{"groups", "print the required case groups as JSON", helpGroups, cmdGroups},
		{"merge", "join the records of several runs into one records file", helpMerge, cmdMerge},
		{"inputs", "print the files a case's TLC run reads, with their hashes", helpInputs, cmdInputs},
		{"table", "check the table model: contracts, findings, controls", helpTable, cmdTable},
		{"member", "check the member and epoch protocol and its mutation controls", helpMember, cmdMember},
		{"replay", "replay table.lua receipts and check them against EpochMemberTable", helpReplay, cmdReplay},
		{"witnesses", "replay the table model's findings against a table.lua", helpWitnesses, cmdWitnesses},
	}
}

func run(args []string, e env) int {
	if len(args) == 0 {
		fmt.Fprint(e.stderr, banner())
		return 2
	}
	switch args[0] {
	case "help", "-h", "--help":
		if len(args) > 1 && args[0] == "help" {
			return helpVerb(e, args[1])
		}
		fmt.Fprint(e.stdout, banner())
		return 0
	}
	for _, v := range verbs() {
		if v.name == args[0] {
			return v.do(e, args[1:])
		}
	}
	names := make([]string, 0, len(verbs()))
	for _, v := range verbs() {
		names = append(names, v.name)
	}
	fmt.Fprintf(e.stderr, "%s: unknown verb %s; the verbs are %s; run: %s help\n", tool, oneline.Quote(args[0]), strings.Join(names, ", "), tool)
	return 2
}

func helpVerb(e env, name string) int {
	for _, v := range verbs() {
		if v.name == name {
			fmt.Fprint(e.stdout, v.help)
			return 0
		}
	}
	fmt.Fprintf(e.stderr, "%s help: unknown verb %s; run: %s help\n", tool, oneline.Quote(name), tool)
	return 2
}

func banner() string {
	var b strings.Builder
	b.WriteString(`tlacheck: run the TLA+ model checks of this repository.

usage: tlacheck <verb> [flags]      flags come before positionals
       tlacheck help <verb>         the help of one verb (also: tlacheck <verb> -h)

verbs:
`)
	for _, v := range verbs() {
		fmt.Fprintf(&b, "  %-10s %s\n", v.name, v.summary)
	}
	b.WriteString(`
Run it on a bench: TLC needs java and the replays need redis-server, and neither
belongs on a working machine. Every run is bounded by --timeout; a timeout is a
failure, never a green. Nothing is downloaded, and the models are never run in
place: TLC runs in a private copy under --dir.

output: one line per event, <TOKEN> OK|FAIL key=value ...; OK on stdout, FAIL on
stderr. Exit 0 passed, 1 ran and said NO, 2 could not run.
first run: tlacheck groups --root .
`)
	return b.String()
}

// flags builds the flag set of a verb. Errors are not printed by the flag
// package: parse turns them into the one-line refusals of docs/CLI-STYLE.md.
func flags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(tool+" "+name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	return fs
}

// parse reads the flags of a verb. It returns done when the invocation is
// finished (help printed, or refused) with the exit code to return.
//
// A flag after a positional is a late flag and is refused by name, except after
// a literal "--".
func parse(e env, name string, fs *flag.FlagSet, args []string, help string) (done bool, code int) {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(e.stdout, help)
			return true, 0
		}
		msg := strings.TrimPrefix(err.Error(), "flag provided but not defined: ")
		if msg != err.Error() {
			msg = "unknown flag " + msg
		}
		return true, refuse(e, name, msg, tool+" "+name+" -h")
	}
	literal := false
	for _, a := range args {
		literal = literal || a == "--"
	}
	if !literal {
		for _, a := range fs.Args() {
			if strings.HasPrefix(a, "-") && a != "-" {
				return true, refuse(e, name, "flag "+oneline.Quote(a)+" comes after a positional argument; flags come first", tool+" "+name+" -h")
			}
		}
	}
	return false, 0
}

// refuse prints a refusal: the verb, the cause, and the next command, on one
// line, and returns exit 2 (could not run).
func refuse(e env, verb, cause, next string) int {
	fmt.Fprintf(e.stderr, "%s %s: %s; run: %s\n", tool, verb, oneline.Escape(cause), next)
	return 2
}

// missing refuses a verb that lacks required flags, naming every one at once.
func missing(e env, verb string, names ...string) (int, bool) {
	var absent []string
	for i := 0; i+1 < len(names); i += 2 {
		if names[i+1] == "" {
			absent = append(absent, "--"+names[i])
		}
	}
	if len(absent) == 0 {
		return 0, false
	}
	return refuse(e, verb, "missing required "+strings.Join(absent, ", "), tool+" "+verb+" -h"), true
}

// event prints one event line. Values are escaped as fields.
func event(w io.Writer, token, status string, kv ...string) {
	var b strings.Builder
	b.WriteString(token + " " + status)
	for i := 0; i+1 < len(kv); i += 2 {
		b.WriteString(" " + kv[i] + "=" + oneline.Field(kv[i+1]))
	}
	fmt.Fprintln(w, b.String())
}

// eventWhy prints an event whose last part is a sentence: the key=value fields,
// then a colon and the reason, escaped so the line stays one line.
func eventWhy(w io.Writer, token, status, why string, kv ...string) {
	var b strings.Builder
	b.WriteString(token + " " + status)
	for i := 0; i+1 < len(kv); i += 2 {
		b.WriteString(" " + kv[i] + "=" + oneline.Field(kv[i+1]))
	}
	fmt.Fprintf(w, "%s: %s\n", b.String(), oneline.Escape(why))
}

func seconds(d time.Duration) string { return fmt.Sprintf("%.2f", d.Seconds()) }

func oneLine(s string) string { return oneline.Escape(s) }
