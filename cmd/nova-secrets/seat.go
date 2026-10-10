// seat.go holds the seat verb: its flags, its run and the helpers only it uses.

package main

import (
	"flag"
	"fmt"
	"io"

	"github.com/mas-bandwidth/nova-tools/pkg/oneline"
	"github.com/mas-bandwidth/nova-tools/pkg/secrets"
)

// runSeatCLI dispatches the seat subverbs: `add` gives a new seat its first values,
// `inject` re-seals named values into a seat that exists. Every other change to a seat
// is a pull request against .sops.yaml that the store's gate reviews.
func runSeatCLI(args []string, s streams) int {
	if len(args) == 0 {
		return s.refuse("seat", 2, fmt.Errorf("seat takes a subverb, add (a new seat's first values) or inject (values into a seat that exists); run: nova-secrets seat add -h"))
	}
	switch args[0] {
	case "add":
		return runSeatAddCLI(args[1:], s)
	case "inject":
		return runSeatInjectCLI(args[1:], s)
	case "help", "--help", "-h":
		fmt.Fprintf(s.stdout, "%s", usage)
		return 0
	default:
		return s.refuse("seat", 2, fmt.Errorf("unknown seat subverb %s; the subverbs are add and inject; run: nova-secrets seat add -h", oneline.Quote(args[0])))
	}
}

func runSeatInjectCLI(args []string, s streams) int {
	fs := flag.NewFlagSet("seat inject", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	storeFlag := fs.String("store", "", storeUse)
	asFlag := fs.String("as", "", "the existing `seat` receiving the values; its <seat>.yaml is in the store (required)")
	fromFlag := fs.String("from", "", fromUse)
	onlyFlag := fs.String("only", "", onlyUse)
	keyFlag := fs.String("key", "", seatKey)
	sopsFlag := fs.String("sops", "", sopsUse)
	ghFlag := fs.String("gh", "gh", ghUse)
	gitFlag := fs.String("git", "git", gitUse)
	noPRFlag := fs.Bool("no-pr", false, noPRUse)
	dryRunFlag := fs.Bool("dry-run", false, dryRunHelp)
	if err := parseFlags(fs, args); err != nil {
		return s.refuse("seat inject", 2, err)
	}

	line, err := secrets.RunSeatInject(secrets.SeatInjectOptions{
		StoreDir: *storeFlag,
		AsName:   *asFlag,
		From:     *fromFlag,
		Only:     *onlyFlag,
		KeyPath:  *keyFlag,
		SopsPath: *sopsFlag,
		GHPath:   *ghFlag,
		GitPath:  *gitFlag,
		NoPR:     *noPRFlag,
		DryRun:   *dryRunFlag,
		Progress: s.stderr,
	})
	if err != nil {
		return s.refuse("seat inject", 2, err)
	}
	fmt.Fprintln(s.stdout, line)
	return 0
}

func runSeatAddCLI(args []string, s streams) int {
	fs := flag.NewFlagSet("seat add", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	storeFlag := fs.String("store", "", storeUse)
	asFlag := fs.String("as", "", "the new seat's `name`; the store holds no <name>.yaml yet (required)")
	pubFlag := fs.String("pub", "", "the new seat's age public `key` (age1…), from its own keygen receipt (required)")
	fromFlag := fs.String("from", "", fromUse)
	onlyFlag := fs.String("only", "", onlyUse)
	keyFlag := fs.String("key", "", seatKey)
	sopsFlag := fs.String("sops", "", sopsUse)
	if err := parseFlags(fs, args); err != nil {
		return s.refuse("seat add", 2, err)
	}

	lines, err := secrets.RunSeatAdd(secrets.SeatAddOptions{
		StoreDir: *storeFlag,
		AsName:   *asFlag,
		Pub:      *pubFlag,
		From:     *fromFlag,
		Only:     *onlyFlag,
		KeyPath:  *keyFlag,
		SopsPath: *sopsFlag,
		Progress: s.stderr,
	})
	if err != nil {
		return s.refuse("seat add", 2, err)
	}
	for _, l := range lines {
		fmt.Fprintln(s.stdout, l)
	}
	return 0
}
