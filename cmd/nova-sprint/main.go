package main

import (
	"fmt"
	"os"
)

const usage = `nova-sprint: bench guard and sprint tools

usage:
  nova-sprint version                  print this build identity (--version also accepted)
  nova-sprint bench guard [--bench <b>] [--loop <s>] [--dry-run] [--redis <addr>]
                                       reap old search processes
  nova-sprint bench guard set --bench <b> [--max-age <s>] [--names a,b] [--spare a,b] [--redis <addr>]
                                       write guard configuration hash

exit codes: 0 pass done, 1 process table unreadable or Redis error, 2 usage refused.
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	cmd := os.Args[1]
	args := os.Args[2:]

	code := 2
	switch cmd {
	case "version", "--version", "-v":
		code = cmdVersion(args, os.Stdout, os.Stderr)
	case "bench":
		if len(args) < 1 {
			fmt.Fprint(os.Stderr, usage)
			os.Exit(2)
		}
		subcmd := args[0]
		subargs := args[1:]
		switch subcmd {
		case "guard":
			code = cmdGuard(subargs, os.Stdout, os.Stderr)
		default:
			fmt.Fprintf(os.Stderr, "nova-sprint bench: unknown subcommand %q\n%s", subcmd, usage)
			code = 2
		}
	case "help", "--help", "-h":
		fmt.Fprint(os.Stdout, usage)
		code = 0
	default:
		fmt.Fprintf(os.Stderr, "nova-sprint: unknown command %q\n%s", cmd, usage)
		code = 2
	}
	os.Exit(code)
}
