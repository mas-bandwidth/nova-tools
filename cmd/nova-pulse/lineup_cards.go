package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/pulse/lineup"
)

// lineupCardLintCmd is the R4 lint (#2636): `nova-swarm lint --base-check`, handed
// `--card <path>` per card. Until #2636 lands the flag is unknown, the lint exits 2 on
// every card, and every card is RED -- the lineup cannot go green on a lint that has not
// shipped, which is the point.
const lineupCardLintCmd = "nova-swarm lint --base-check"

// lineupCardChecks is the card half of `nova-pulse lineup --profile coding` (#2944):
//
//	--queue <dir>     the sprint queue (its *.md, front/*.md and pending/*.md are the cards)
//	--routes <file>   the allowed list, one route per line (default <queue>/ROUTES-code)
//	--lint-cmd <cmd>  the R4 lint command (default "nova-swarm lint --base-check")
//
// It prints one `LINEUP RED card=<name> check=<check> detail=...` line per failure and
// exits 1, or one `LINEUP OK cards=<n> checks=...` line and exits 0; 2 on bad flags.
// The bench half (#2562, cmd/nova-pulse/lineup.go) calls this with the same flags under
// `--profile coding`; the two are separate files so either lands first.
func lineupCardChecks(args []string, stdout, stderr io.Writer) int {
	f := newFlags("lineup")
	queue := f.fs.String("queue", "", "")
	routes := f.fs.String("routes", "", "")
	lintCmd := f.fs.String("lint-cmd", lineupCardLintCmd, "")
	if !f.parse(args, stderr) {
		return 2
	}
	f.want(*queue, "queue", "the sprint queue directory whose cards are checked before the first is dealt")
	if f.refused(stderr) {
		return 2
	}
	cards, err := lineup.ReadQueue(*queue)
	if err != nil {
		fmt.Fprintf(stderr, "nova-pulse lineup: --queue wants a readable queue directory: %s\n", oneline.Err(err))
		return 2
	}
	routesFile := *routes
	if routesFile == "" {
		routesFile = filepath.Join(*queue, "ROUTES-code")
	}
	// A missing list is not refused here: it is a queue RED, printed with the others.
	allowed, _ := lineup.ReadRoutes(routesFile)
	cc := lineup.CardChecks{Lint: cardLinter(*lintCmd), AllowedRoutes: allowed}
	findings := cc.Check(cards)
	for _, fd := range findings {
		fmt.Fprintf(stdout, "LINEUP RED card=%s check=%s detail=%s\n",
			oneline.Field(fd.Card), oneline.Field(fd.Check), oneline.Escape(oneline.Cap(fd.Detail, oneline.TailBytes)))
	}
	if len(findings) > 0 {
		return 1
	}
	fmt.Fprintf(stdout, "LINEUP OK cards=%d checks=%s\n", len(cards), strings.Join(lineup.CardCheckNames, ","))
	return 0
}

// cardLinter runs the lint command on one card. Exit 0 is clean; any other exit is a
// drift, named by the first LINT DRIFT line it printed (or its first line of output);
// a command that cannot start is an error.
func cardLinter(cmd string) func(lineup.Card) (string, error) {
	argv := strings.Fields(cmd)
	return func(c lineup.Card) (string, error) {
		if len(argv) == 0 {
			return "", errors.New("empty --lint-cmd")
		}
		var out bytes.Buffer
		x := exec.Command(argv[0], append(append([]string{}, argv[1:]...), "--card", c.Path)...)
		x.Stdout, x.Stderr = &out, &out
		err := x.Run()
		if err == nil {
			return "", nil
		}
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return "", err
		}
		first := ""
		for _, l := range strings.Split(out.String(), "\n") {
			l = strings.TrimSpace(l)
			if strings.HasPrefix(l, "LINT DRIFT") {
				return l, nil
			}
			if first == "" && l != "" {
				first = l
			}
		}
		if first == "" {
			first = "no output"
		}
		return fmt.Sprintf("exit %d: %s", exit.ExitCode(), first), nil
	}
}
