// nova-card manages nova-sprint card push, wrap, and end operations.
package main

import (
	"fmt"
	"io"
	"os"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

const usage = `nova-card: nova-sprint card manager (see docs/nova-sprint/CARD.md)

usage:
  nova-card help      print this banner
  nova-card version   print build identity

environment:
  NOVA_CARD_REPO      the clone path where the harness executes
  NOVA_CARD_BRANCH    the branch where the harness leaves its commit

example:
  nova-card help
  nova-card version
`

func refuse(stderr io.Writer, where, what string) int {
	fmt.Fprintf(stderr, "nova-card%s: %s; run: nova-card help\n", oneline.Escape(where), oneline.Escape(what))
	return 2
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return refuse(stderr, "", "no verb given; help is the verb this tool exists for")
	}
	switch args[0] {
	case "version", "--version":
		fmt.Fprintln(stdout, "nova-card 0.12.0")
		return 0
	case "help", "-h", "--help":
		fmt.Fprint(stdout, usage)
		return 0
	default:
		return refuse(stderr, "", fmt.Sprintf("unknown subcommand %q", args[0]))
	}
}
