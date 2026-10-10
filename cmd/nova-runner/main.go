// Command nova-runner keeps one friend at her row's width.
// The contract is docs/SPEC-RUNNER.md.
//
//	example:
//	  nova-runner run --as ada --dir ~/ada-working --harness opencode --seat coordinator
package main

import (
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/mas-bandwidth/nova-tools/internal/tool"
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
	return runnerTool().Run(args, os.Stdin, stdout, stderr)
}

func runnerTool() *tool.Tool {
	return &tool.Tool{
		Name:      "nova-runner",
		What:      "keep a friend at her configured width with one-shot harness lanes",
		How:       helpText,
		Stamp:     version,
		ExitTable: "0 stopped normally, 1 runner failed, 2 invalid invocation",
		Verbs: []tool.Verb{{
			Name:    "run",
			Usage:   "run --as <friend> --dir <working-dir> --harness <command> --seat <seat> [--model-<tier> <model>]",
			Example: "run --as ada --dir ~/ada-working --harness opencode --seat coordinator",
			Effect:  tool.Delivery + "; claims and closes sprint cards, sends beats and harness-fault judgments",
			Flags: func(f *tool.Flags) {
				f.Required("as", "the friend whose row this runner keeps")
				f.Required("dir", "her working directory, where nova-friend stages jobs")
				f.Required("harness", "the one-shot harness command")
				f.Required("seat", "who hears a harness-fault judgment")
				for _, tier := range []string{"frontier", "heavy", "pro", "flash"} {
					f.String("model-"+tier, "", "fallback model for "+tier+" cards")
				}
				f.Prints()
			},
			Run: runLane,
		}},
	}
}

func runLane(c *tool.Call) *tool.Out {
	if refused := c.Refused(); refused != nil {
		return refused
	}
	as, dir, harness, seat := c.Str("as"), c.Str("dir"), c.Str("harness"), c.Str("seat")
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return tool.Refuse("--dir " + dir + " is not a directory; give an existing working directory")
	}
	r := &Runner{
		Friend:  as,
		Dir:     dir,
		Harness: harness,
		Seat:    seat,
		Version: version,
		Models: map[string]string{
			"frontier": c.Str("model-frontier"),
			"heavy":    c.Str("model-heavy"),
			"pro":      c.Str("model-pro"),
			"flash":    c.Str("model-flash"),
		},
		Out: c.Stdout,
		Err: c.Stderr,
	}
	r.Edges = realEdges(as, dir, harness, seat, r.Models)
	ctx, stop := signal.NotifyContext(c.Ctx, syscall.SIGTERM)
	defer stop()
	if err := r.Run(ctx); err != nil {
		return tool.Fail("nova-runner run: " + err.Error())
	}
	return tool.Exit(0)
}
