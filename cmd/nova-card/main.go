// Command nova-card is the card wrapper (#3059): the one process that owns
// one card attempt on a bench. `nova-sprint card launch --stdin` (#2931)
// starts it detached, in its own session, as
//
//	nova-card <sprint>/<label>/<attempt>
//
// with the canonical launch line `<sprint> <label> <attempt> <token>` as its
// only stdin. It checks the line names its own card, refuses (exit 4, nothing
// written) a card not dealt to this bench, writes launched, runs the harness
// under the card clock, beats every NOVA_CARD_BEAT, writes the end record and
// calls card end, and deletes the job directory. internal/nsprint/card
// wrapper.go is the whole behaviour; this file reads argv, stdin and the
// bench's NOVA_CARD_* configuration. The token is never printed.
package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/buildinfo"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/launch"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

var version string

const usage = `nova-card: the card wrapper; owns one card attempt on a bench (#3059)

usage:
  nova-card <sprint>/<label>/<attempt>     (the launch line on stdin)
  nova-card version
  nova-card help

nova-card is started by ` + "`nova-sprint card launch --stdin`" + `, never by hand:
its stdin is the one line <sprint> <label> <attempt> <token>, and the line
must name the card in argv. The token is read from stdin only and is never
printed, passed to the harness, or written to a receipt (token_sha only).

The bench configures it through the environment the launcher runs in:
  NOVA_CARD_REDIS    the sprint Redis address
  NOVA_CARD_BENCH    this bench's name, as the card hash names it
  NOVA_CARD_HARNESS  absolute path of the harness program
  NOVA_CARD_JOBS     absolute root of job dirs (<root>/<S>/<label>/<attempt>)
  NOVA_CARD_RESULTS  absolute root of results (<root>/<identity>)
  NOVA_CARD_CLOCK    the card clock, a Go duration (45m)
  NOVA_CARD_BEAT     the beat cadence, a Go duration (default 60s)

The harness runs in the job dir with NOVA_CARD, NOVA_CARD_BRANCH,
NOVA_CARD_JOB and NOVA_CARD_OUT set; what it writes under NOVA_CARD_OUT is
copied to the results dir before the end record, and the job dir is deleted.
Exit 0 is DONE/done; any other exit is FAILED crash; the clock running out
is FAILED timeout.

exit codes: 0 the end was recorded (any outcome), 1 configuration missing,
2 could not run, 3 fenced, 4 not dealt to this bench, 6 Redis unavailable.

example:
  nova-card version
`

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv)) }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) == 0 {
		return refuse(stderr, "wants <sprint>/<label>/<attempt> with the launch line on stdin")
	}
	switch args[0] {
	case "help", "-h", "--help":
		if len(args) > 1 {
			return refuse(stderr, "help takes no arguments")
		}
		fmt.Fprint(stdout, usage)
		return 0
	case "version", "--version":
		if len(args) > 1 {
			return refuse(stderr, "version takes no arguments")
		}
		fmt.Fprintln(stdout, buildinfo.Line("nova-card", version))
		return 0
	}
	if len(args) != 1 {
		return refuse(stderr, "wants one card <sprint>/<label>/<attempt>")
	}
	line, err := readLaunchLine(stdin)
	if err != nil {
		return refuse(stderr, err.Error())
	}
	if line.Card() != args[0] {
		// Neither side is echoed whole: the stdin line holds the token.
		return refuse(stderr, "the launch line on stdin names "+line.Card()+", not "+oneline.Escape(args[0]))
	}
	cfg, err := config(line, getenv)
	if err != nil {
		fmt.Fprintln(stdout, card.WrapperReport{Code: card.WrapperExitUsage, Card: line.Card(), Why: err.Error()}.Line())
		return card.WrapperExitUsage
	}
	ctx := context.Background()
	st, err := store.Open(ctx, getenv("NOVA_CARD_REDIS"))
	if err != nil {
		fmt.Fprintln(stdout, card.WrapperReport{Code: card.WrapperExitRedis, Card: line.Card(), Why: "redis: " + err.Error()}.Line())
		return card.WrapperExitRedis
	}
	defer st.Close()
	ledger := &card.RedisLedger{Store: st, Sprint: line.Sprint, Label: line.Label, Token: line.Token}
	rep := card.RunWrapper(ctx, cfg, ledger)
	fmt.Fprintln(stdout, rep.Line())
	return rep.Code
}

// readLaunchLine reads the one canonical line; a second line is refused.
func readLaunchLine(in io.Reader) (launch.Line, error) {
	sc := bufio.NewScanner(io.LimitReader(in, 4096))
	if !sc.Scan() {
		return launch.Line{}, fmt.Errorf("no launch line on stdin")
	}
	l, err := launch.ParseLine(strings.TrimRight(sc.Text(), "\r"))
	if err != nil {
		return launch.Line{}, fmt.Errorf("stdin is not a launch line: %s", err)
	}
	if sc.Scan() && strings.TrimSpace(sc.Text()) != "" {
		return launch.Line{}, fmt.Errorf("stdin holds more than one launch line")
	}
	return l, nil
}

func config(l launch.Line, getenv func(string) string) (card.WrapperConfig, error) {
	cfg := card.WrapperConfig{
		Sprint: l.Sprint, Label: l.Label, Attempt: l.Attempt,
		Bench:       getenv("NOVA_CARD_BENCH"),
		Harness:     getenv("NOVA_CARD_HARNESS"),
		JobsRoot:    getenv("NOVA_CARD_JOBS"),
		ResultsRoot: getenv("NOVA_CARD_RESULTS"),
	}
	var bad []string
	if getenv("NOVA_CARD_REDIS") == "" {
		bad = append(bad, "NOVA_CARD_REDIS")
	}
	for _, kv := range []struct {
		name string
		dst  *time.Duration
		need bool
	}{{"NOVA_CARD_CLOCK", &cfg.Clock, true}, {"NOVA_CARD_BEAT", &cfg.BeatEvery, false}} {
		v := getenv(kv.name)
		if v == "" {
			if kv.need {
				bad = append(bad, kv.name)
			}
			continue
		}
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			bad = append(bad, kv.name)
			continue
		}
		*kv.dst = d
	}
	for name, v := range map[string]string{"NOVA_CARD_BENCH": cfg.Bench, "NOVA_CARD_HARNESS": cfg.Harness, "NOVA_CARD_JOBS": cfg.JobsRoot, "NOVA_CARD_RESULTS": cfg.ResultsRoot} {
		if v == "" {
			bad = append(bad, name)
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		return cfg, fmt.Errorf("missing or bad %s", strings.Join(bad, ", "))
	}
	return cfg, nil
}

func refuse(stderr io.Writer, why string) int {
	fmt.Fprintf(stderr, "nova-card: %s; run: nova-card help\n", why)
	return 2
}
