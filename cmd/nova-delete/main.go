// Package main provides the nova-delete tool: moves files to quarantine instead of deleting them.
package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "nova-delete: missing command; run: nova-delete help")
		return 2
	}

	switch args[0] {
	case "help", "-h", "--help":
		printBanner()
		return 0
	case "version":
		fmt.Println("nova-delete 0.1.0")
		return 0
	case "delete":
		return runDelete(args[1:])
	case "sweep":
		return runSweep(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "nova-delete: unknown verb %q; run: nova-delete help\n", args[0])
		return 2
	}
}

func printBanner() {
	fmt.Println(`nova-delete: Move a literal path to quarantine instead of deleting it

how it works:
  nova-delete takes exactly one literal absolute path and moves it to a dated quarantine folder
  <root>/.quarantine-YYYYMMDD/<basename>.<HHMMSS>.<pid> under the allowed root that holds it.
  Allowed roots are: system temp directory and paths named by NOVA_DELETE_ROOTS (colon-separated).
  The sweep verb removes quarantine entries older than a specified duration.

first run:
  Set NOVA_DELETE_ROOTS to a colon-separated list of allowed absolute paths, e.g.:
  NOVA_DELETE_ROOTS=/tmp NOVA_DELETE_ROOTS=/Users/user/data nova-delete /tmp/file.txt

usage:
  nova-delete <path>
  nova-delete sweep --older-than <duration>
  nova-delete help [<verb>]

exit codes:
  0  success (path moved or skipped because it doesn't exist)
  1  refusal (the verb ran and said no)
  2  could not run (bad invocation, e.g., wrong number of args)

example:
  nova-delete /tmp/to_delete.txt
  nova-delete sweep --older-than 7d`)
}

func runDelete(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "nova-delete: missing path; run: nova-delete help")
		return 2
	}
	if len(args) > 1 {
		fmt.Fprintln(os.Stderr, "nova-delete: too many arguments; run: nova-delete help")
		return 2
	}
	path := args[0]
	if path == "" {
		fmt.Fprintln(os.Stderr, "nova-delete: empty argument; run: nova-delete help")
		return 2
	}
	msg, exit, err := Delete(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nova-delete: %s; run: nova-delete help\n", err)
		return 2
	}
	fmt.Println(msg)
	return exit
}

func runSweep(args []string) int {
	fs := flag.NewFlagSet("nova-delete sweep", flag.ExitOnError)
	olderThan := fs.Duration("older-than", 0, "remove entries older than this duration")
	if err := fs.Parse(args); err != nil {
		fmt.Fprintln(os.Stderr, "nova-delete sweep: invalid flags; run: nova-delete sweep -h")
		return 2
	}
	if *olderThan == 0 {
		fmt.Fprintln(os.Stderr, "nova-delete sweep: --older-than is required; run: nova-delete sweep -h")
		return 2
	}
	swept, err := Sweep(*olderThan)
	if err != nil {
		fmt.Fprintf(os.Stderr, "nova-delete sweep: %s; run: nova-delete sweep -h\n", err)
		return 2
	}
	for _, p := range swept {
		fmt.Println("SWEPT " + p)
	}
	return 0
}

func printHelp() {
	printBanner()
}
