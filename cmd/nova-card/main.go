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
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

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
  NOVA_CARD_HARNESS  absolute path of a harness program; unset, the Go
                     harness runs in-process (nova-sprint card run, #3681)
                     from NOVA_CARD_HARNESS_BIN, NOVA_BENCH_SEAT,
                     NOVA_CARD_DEADLINE, NOVA_CARD_TOKENS and HOME
  NOVA_CARD_JOBS     absolute root of job dirs (<root>/<S>/<label>/<attempt>)
  NOVA_CARD_RESULTS  absolute root of results (<root>/<identity>)
  NOVA_CARD_CLOCK    the card clock, a Go duration (45m)
  NOVA_CARD_BEAT     the beat cadence, a Go duration (default 60s)
  NOVA_CARD_LAUNCH_DEADLINE_MS  launcher's absolute batch deadline

The harness runs in the job dir with NOVA_CARD, NOVA_CARD_BRANCH,
NOVA_CARD_JOB and NOVA_CARD_OUT set; what it writes under NOVA_CARD_OUT is
copied to the results dir before the end record, and the job dir is deleted.
Exit 0 is DONE/done; any other exit is FAILED crash, or FAILED refused with
the refusal line as the card's why when the harness's program printed a
NATIVE REFUSED line (#3194); the clock running out is FAILED timeout.

exit codes: 0 the end was recorded (any outcome), 1 configuration missing,
2 could not run, 3 fenced, 4 not dealt to this bench, 6 Redis unavailable.
A refusal before launched also leaves its one REFUSED line (never the token)
on the sprint log, kind "wrapper refused", or, when Redis does not answer,
appended to NOVA_CARD_RESULTS/refused/<sprint>/<label>/<attempt>.line (#3420).

example:
  nova-card version
`

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv)) }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) int {
	ack := launchAcker(getenv)
	defer ack("REFUSED wrapper exited before card launched")
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
	ctx := context.Background()
	line, err := readLaunchLine(stdin)
	if err != nil {
		if l, ok := cardFromArg(args[0]); ok {
			recordRefusal(ctx, getenv, nil, true, l, card.WrapperReport{Code: 2, Card: l.Card(), Why: err.Error()})
		}
		return refuse(stderr, err.Error())
	}
	if line.Card() != args[0] {
		// Neither side is echoed whole: the stdin line holds the token.
		why := "the launch line on stdin names " + line.Card() + ", not " + oneline.Escape(args[0])
		recordRefusal(ctx, getenv, nil, true, line, card.WrapperReport{Code: 2, Card: line.Card(), Why: why})
		return refuse(stderr, why)
	}
	cfg, err := config(line, getenv)
	if err != nil {
		ack("REFUSED " + err.Error())
		rep := card.WrapperReport{Code: card.WrapperExitUsage, Card: line.Card(), Why: err.Error()}
		recordRefusal(ctx, getenv, nil, true, line, rep)
		fmt.Fprintln(stdout, rep.Line())
		return card.WrapperExitUsage
	}
	st, err := store.Open(ctx, getenv("NOVA_CARD_REDIS"))
	if err != nil {
		ack("REFUSED redis unavailable")
		rep := card.WrapperReport{Code: card.WrapperExitRedis, Card: line.Card(), Why: "redis: " + err.Error()}
		recordRefusal(ctx, getenv, nil, false, line, rep)
		fmt.Fprintln(stdout, rep.Line())
		return card.WrapperExitRedis
	}
	defer st.Close()
	cfg.Store = st
	cfg.Started = func() { ack("LAUNCHED") }
	ledger := &card.RedisLedger{
		Store: st, Sprint: line.Sprint, Label: line.Label, Token: line.Token,
		LaunchDeadline: cfg.LaunchDeadline,
	}
	rep := card.RunWrapper(ctx, cfg, ledger)
	if rep.Code != card.WrapperExitEnded {
		ack("REFUSED " + rep.Why)
	}
	if rep.Outcome == "" && rep.Code != card.WrapperExitEnded {
		// Refused before launched: nothing else records it (#3420).
		recordRefusal(ctx, getenv, st, false, line, rep)
	}
	fmt.Fprintln(stdout, rep.Line())
	return rep.Code
}

// launchAcker writes the one child-to-launcher status on the inherited fd.
// It is a no-op when nova-card was invoked outside `card launch --stdin`.
func launchAcker(getenv func(string) string) func(string) {
	raw := getenv(launch.LaunchAckFDEnv)
	fd, err := strconv.Atoi(raw)
	if raw == "" || err != nil || fd < 3 {
		return func(string) {}
	}
	f := os.NewFile(uintptr(fd), "nova-card-launch-ack")
	var once sync.Once
	return func(status string) {
		once.Do(func() {
			status = strings.ReplaceAll(strings.ReplaceAll(status, "\r", " "), "\n", " ")
			fmt.Fprintln(f, status)
			_ = f.Close()
		})
	}
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
	if raw := getenv(launch.LaunchDeadlineEnv); raw != "" {
		ms, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || ms <= 0 {
			bad = append(bad, launch.LaunchDeadlineEnv)
		} else {
			cfg.LaunchDeadline = time.UnixMilli(ms)
		}
	}
	for _, kv := range []struct {
		name string
		dst  *time.Duration
		need bool
	}{{"NOVA_CARD_CLOCK", &cfg.Clock, true}, {"NOVA_CARD_BEAT", &cfg.BeatEvery, false}, {"NOVA_CARD_CHECK", &cfg.CheckTimeout, false}} {
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
	for name, v := range map[string]string{"NOVA_CARD_BENCH": cfg.Bench, "NOVA_CARD_JOBS": cfg.JobsRoot, "NOVA_CARD_RESULTS": cfg.ResultsRoot} {
		if v == "" {
			bad = append(bad, name)
		}
	}
	if cfg.Harness == "" {
		// No harness program: the Go harness in-process (#3681), configured
		// from the same card.env the bash one read.
		rc, missing := card.RunConfigFromEnv(getenv)
		bad = append(bad, missing...)
		cfg.InProcess = &rc
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		bad = slices.Compact(bad)
		return cfg, fmt.Errorf("missing or bad %s", strings.Join(bad, ", "))
	}
	return cfg, nil
}

// RefusedLogKind is the sprint-log kind of a wrapper refusal record (#3420).
// It wakes no reconciler pass (reconcile.Classify reads "card " and "task "
// kinds only): the card is still dealt, and the refusal is evidence only.
const RefusedLogKind = "wrapper refused"

// refusalTimeout bounds the one Redis dial and XADD of a refusal record.
const refusalTimeout = 3 * time.Second

// recordRefusal keeps one line for a refusal before launched (#3420). The
// launcher starts the wrapper detached with stdout and stderr /dev/null, so
// the REFUSED line printed there reaches no one. The line (the report's line,
// the bench and the time, never the token) goes to the sprint log
// s:<S>:log as one entry of kind RefusedLogKind when Redis answers: st when
// the wrapper has a connection, else a fresh dial of NOVA_CARD_REDIS when dial
// is set. Otherwise it is appended to <NOVA_CARD_RESULTS>/refused/<S>/<label>/
// <attempt>.line on the bench. With neither, nothing can be kept.
func recordRefusal(ctx context.Context, getenv func(string) string, st *store.Store, dial bool, l launch.Line, rep card.WrapperReport) {
	bench := getenv("NOVA_CARD_BENCH")
	text := rep.Line() + " bench=" + oneline.Escape(bench) + " at=" + time.Now().UTC().Format(time.RFC3339)
	why := rep.Why
	if _, hex, ok := strings.Cut(l.Token, "."); ok && hex != "" {
		text = strings.ReplaceAll(text, hex, "<token>")
		why = strings.ReplaceAll(why, hex, "<token>")
	}
	ctx, cancel := context.WithTimeout(ctx, refusalTimeout)
	defer cancel()
	if st == nil && dial && getenv("NOVA_CARD_REDIS") != "" {
		if s, err := store.Open(ctx, getenv("NOVA_CARD_REDIS")); err == nil {
			defer s.Close()
			st = s
		}
	}
	if st != nil && st.Client() != nil {
		err := st.Client().XAdd(ctx, &redis.XAddArgs{Stream: card.LogKey(l.Sprint), Values: []any{
			"kind", RefusedLogKind, "id", l.Label, "attempt", strconv.Itoa(l.Attempt),
			"bench", bench, "to", "refused", "code", strconv.Itoa(rep.Code),
			"reason", why, "line", text, "actor", "nova-card",
			"at", strconv.FormatInt(time.Now().Unix(), 10),
		}}).Err()
		if err == nil {
			return
		}
	}
	root := getenv("NOVA_CARD_RESULTS")
	if !filepath.IsAbs(root) {
		return
	}
	// l's sprint and label passed launch.ParseLine ([a-z0-9-]), so the path
	// stays under root.
	dir := filepath.Join(root, "refused", l.Sprint, l.Label)
	if os.MkdirAll(dir, 0o755) != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, strconv.Itoa(l.Attempt)+".line"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	_, _ = fmt.Fprintln(f, text)
	_ = f.Close()
}

// cardFromArg reads argv's <sprint>/<label>/<attempt> when the launch line
// could not be read, so that refusal is kept under the card argv names.
// launch.ParseLine is the one validator of the three; the token field it is
// given is a placeholder of the right shape and is dropped.
func cardFromArg(arg string) (launch.Line, bool) {
	f := strings.Split(arg, "/")
	if len(f) != 3 {
		return launch.Line{}, false
	}
	l, err := launch.ParseLine(strings.Join(f, " ") + " " + f[2] + "." + strings.Repeat("0", 32))
	if err != nil {
		return launch.Line{}, false
	}
	l.Token = ""
	return l, true
}

func refuse(stderr io.Writer, why string) int {
	fmt.Fprintf(stderr, "nova-card: %s; run: nova-card help\n", why)
	return 2
}
