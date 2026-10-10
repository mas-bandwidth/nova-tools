// Command nova-runner keeps one friend at her row's width.
// The contract is docs/SPEC-RUNNER.md.
//
//	example:
//	  nova-runner run --as ada --dir ~/ada-working --harness opencode --seat glenn
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"
)

// version is this binary's runner_version, set with -X main.version.
var version = "dev"

const helpText = `nova-runner keeps one friend running at her row's width, every second, with no coordinator in the loop.

  nova-runner run --as <friend> --dir <working-dir> --harness opencode --seat <seat>

The loop, every second:

  1. Width. Read nova-config friend show <friend> at most every 10s. Use that
     row's width, tiers, mode and runner_version. The row is the only max.
     A width change takes effect on the next lane start or finish. The runner
     never kills a lane to make the count match.

  2. Fill. While busy slots are under that width (a work lane is 1, a read lane is one half),
     take the next card from
     nova-sprint queue --as friend.<friend> in the sprint's order (reads first,
     as the queue prints them). Only a ready card is claimed: each one is taken
     with nova-sprint take --as friend.<friend> <card>@<gen> --epoch <n> before
     it is launched, so a working or reading card is never started twice. A work
     card is staged the way nova-friend stages it (jobs/<job>/JOB.md,
     inbox/<job>/BRIEF.md, the checkout) and finished through
     nova-sprint finish --as friend.<friend>; a read card is staged from its
     pinned work branch and head (inbox/<job>/BRIEF.md) and closed through
     nova-sprint read --as friend.<friend> --ok|--broken. The harness runs
     one-shot on the card's model for its tier, capped by the card's deadline.

  3. Beat. nova-sprint friend beat <friend> --working <n> --queue <m>
     --width <w> --running <ids> every second with the true lane count.
     Nothing else beats for her.

  4. Report. A lane that ends with no report finishes FAIL with
     "harness fault: <first error line>". Three alike in a row raise one
     judgment to --seat (nova-bus send), and the runner keeps going.

  5. Follow. When the row's runner_version differs from this binary's, start
     no new lane, wait until the running ones finish, run
     nova-update install nova-runner@<version>, and exec it. Each lane is a
     child with its own session leader, so a lane survives the exec.

It never keeps a width of its own, never starts a card the queue did not give
her, never starts a card that is already working or reading, and never kills a
lane because the width dropped.

--model-<tier> is the harness model when the card names none. The card's own
model wins. This binary's version is the build stamp (default dev).

The decisions are Lanes, Width and Next in width.go. docs/SPEC-RUNNER.md is
the contract.
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		fmt.Fprint(stdout, helpText)
		return 0
	}
	if args[0] != "run" {
		fmt.Fprintf(stderr, "nova-runner: unknown verb %s; run: nova-runner help\n", args[0])
		return 2
	}
	fs := flag.NewFlagSet("nova-runner run", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	as := fs.String("as", "", "the friend whose row this runner keeps")
	dir := fs.String("dir", "", "her working directory, the one nova-friend stages jobs under")
	harness := fs.String("harness", "", "the one-shot harness (opencode, claude, or a command on PATH)")
	seat := fs.String("seat", "", "who hears a harness-fault judgment, nova-bus send --to")
	frontier := fs.String("model-frontier", "", "model for a frontier card that names none")
	heavy := fs.String("model-heavy", "", "model for a heavy card that names none")
	pro := fs.String("model-pro", "", "model for a pro card that names none")
	flash := fs.String("model-flash", "", "model for a flash card that names none")
	fs.Usage = func() { fmt.Fprint(stdout, helpText) }
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprint(stdout, helpText)
			return 0
		}
		fmt.Fprintf(stderr, "nova-runner run: %s; run: nova-runner run -h\n", err)
		return 2
	}
	var missing []string
	if *as == "" {
		missing = append(missing, "--as")
	}
	if *dir == "" {
		missing = append(missing, "--dir")
	}
	if *harness == "" {
		missing = append(missing, "--harness")
	}
	if *seat == "" {
		missing = append(missing, "--seat")
	}
	if len(missing) > 0 {
		fmt.Fprintf(stderr, "nova-runner run: needs %s; run: nova-runner run -h\n", strings.Join(missing, ", "))
		return 2
	}
	info, err := os.Stat(*dir)
	if err != nil || !info.IsDir() {
		fmt.Fprintf(stderr, "nova-runner run: --dir %s is not a directory\n", *dir)
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "nova-runner run: takes no positional arguments; run: nova-runner run -h\n")
		return 2
	}
	r := &Runner{
		Friend:  *as,
		Dir:     *dir,
		Harness: *harness,
		Seat:    *seat,
		Version: version,
		Models: map[string]string{
			"frontier": *frontier,
			"heavy":    *heavy,
			"pro":      *pro,
			"flash":    *flash,
		},
		Out: stdout,
		Err: stderr,
	}
	r.Edges = realEdges(*as, *dir, *harness, *seat, r.Models)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := r.Run(ctx); err != nil {
		fmt.Fprintf(stderr, "nova-runner run: %s\n", err)
		return 1
	}
	return 0
}
