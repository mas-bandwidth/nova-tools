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
	"errors"
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
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/card/harvestcopy"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/launch"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
)

var version string

const usage = `nova-card: the card wrapper; owns one card attempt on a bench (#3059)

usage:
  nova-card <sprint>/<label>/<attempt>     (the launch line on stdin)
  nova-card copy <copy>                    (the line <copy> <token> on stdin)
  nova-card version
  nova-card help

nova-card is started by ` + "`nova-sprint card launch --stdin`" + `, never by hand:
its stdin is the one line <sprint> <label> <attempt> <token>, and the line
must name the card in argv. The token is read from stdin only and is never
printed, passed to the harness, or written to a receipt (token_sha only).

A consumer copy (#3998) runs the same way under ` + "`nova-card copy <copy>`" + `, started
by the bench's copy session (` + "`nova-sprint card session --as bench:<b>`" + `) after
one ` + "`card work --fill`" + `: its stdin line is <copy> <token>, its card is rendered
from task:<copy>, it beats with card beat and the wrapper ends it with card
end --id <copy>; no copy's model runs card end (#4227, #4270). Its job and
results paths use the sprint ` + "`copies`" + ` and the label <primary>-c<n>. A
WORK copy's DONE with a commit is the boundary step (#4227): the wrapper
pushes the copy's branch to the primary's repository (never force), opens
the PR against its BASE (or finds it open), records the PR and ends the
copy --ok --pr --head itself; the model never pushes and never runs card
end. A READ copy's DONE is its RESULT.md line 2 (#4270): a SCORE line
(SCORE N/10 gates=... finding=...) ends the copy with the score, ABSTAIN
<why> ends it fail reason abstain, anything else fail reason no-score. A
FIX copy's DONE with a commit on top of the PR's head pushes the PR's own
branch forward and ends the copy at the new head. That push and PR use
GH_PUSH_TOKEN, the bench's push credential in this process's
environment, through git askpass (this binary re-run with
NOVA_CARD_ASKPASS=1): never in argv, never on disk, never in the harness's or
the sandbox's environment. Without it the copy ends fail reason no-token; a
moved branch or a refused push is push-refused, a refused PR pr-refused.

The bench configures it through the environment the launcher runs in:
  NOVA_CARD_REDIS    the sprint Redis address
  NOVA_CARD_BENCH    this bench's name, as the card hash names it
  NOVA_CARD_HARNESS  absolute path of a harness program; unset, the Go
                     harness runs in-process (nova-sprint card run, #3681)
                     from NOVA_CARD_HARNESS_BIN, NOVA_CARD_DEADLINE,
                     NOVA_CARD_TOKENS and HOME. A copy (nova-card copy)
                     always runs in-process; a program named here is not
                     read for it (#4234)
  NOVA_CARD_JOBS     absolute root of job dirs (<root>/<S>/<label>/<attempt>)
  NOVA_CARD_RESULTS  absolute root of results (<root>/<identity>)
  NOVA_CARD_CLOCK    the card clock, a Go duration (45m)
  NOVA_CARD_BEAT     the beat cadence, a Go duration (default 60s)
  NOVA_CARD_LAUNCH_DEADLINE_MS  launcher's absolute batch deadline
  GH_PUSH_TOKEN  the bench's push credential for a work copy's
                     branch and PR (#4227); the wrapper's only

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

// init is the askpass mode (#4227): git, pushing a work copy's branch, runs
// this binary with NOVA_CARD_ASKPASS=1 and reads the push credential from
// its stdout, so the token is never in argv or on disk.
func init() {
	if harvestcopy.Askpass(os.Args[1:], os.Getenv, os.Stdout) {
		os.Exit(0)
	}
}

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv)) }

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) int {
	ack := launchAcker(getenv)
	defer ack("REFUSED wrapper exited before card launched")
	// --seat <name> (or NOVA_SEAT): the Redis login is read from that seat's
	// file through nova-secrets' library, in this process (#4052).
	args, err := seatcred.FromArgs(args, getenv)
	if err != nil {
		return refuse(stderr, err.Error())
	}
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
	if args[0] == launch.CopyArg {
		if len(args) != 2 {
			return refuse(stderr, "copy wants one copy id: nova-card copy <primary>~<n>")
		}
		return runCopy(args[1], stdin, stdout, stderr, getenv, ack)
	}
	if len(args) != 1 {
		return refuse(stderr, "wants one card <sprint>/<label>/<attempt>")
	}
	ctx := context.Background()
	line, err := readLaunchLine(stdin)
	if err != nil {
		if l, ok := cardFromArg(args[0]); ok {
			keepRefusal(ctx, stderr, getenv, nil, true, l, card.WrapperReport{Code: 2, Card: l.Card(), Why: err.Error()})
		}
		return refuse(stderr, err.Error())
	}
	if line.Card() != args[0] {
		// Neither side is echoed whole: the stdin line holds the token.
		why := "the launch line on stdin names " + line.Card() + ", not " + oneline.Escape(args[0])
		keepRefusal(ctx, stderr, getenv, nil, true, line, card.WrapperReport{Code: 2, Card: line.Card(), Why: why})
		return refuse(stderr, why)
	}
	cfg, err := config(line, getenv)
	if err != nil {
		ack("REFUSED " + err.Error())
		rep := card.WrapperReport{Code: card.WrapperExitUsage, Card: line.Card(), Why: err.Error()}
		keepRefusal(ctx, stderr, getenv, nil, true, line, rep)
		fmt.Fprintln(stdout, rep.Line())
		return card.WrapperExitUsage
	}
	st, err := store.Open(ctx, getenv("NOVA_CARD_REDIS"))
	if err != nil {
		ack("REFUSED redis unavailable")
		rep := card.WrapperReport{Code: card.WrapperExitRedis, Card: line.Card(), Why: "redis: " + err.Error()}
		keepRefusal(ctx, stderr, getenv, nil, false, line, rep)
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
		keepRefusal(ctx, stderr, getenv, st, false, line, rep)
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

// copyConfig is a consumer copy's wrapper configuration (#4234). The copy
// protocol (the card rendered from task:<copy>, run.go) lives in the Go
// harness alone, so a copy always runs in-process, whatever program
// NOVA_CARD_HARNESS names: that program is a launched card's, and reads
// s:<S>:card:<label>, which no copy has. batman and superman kept a stale
// card.env naming the retired bash harness (fleet converge never reached the
// darwin pair after 2026-09-24), and it ended every copy of the 100-card
// quack run in under a second: REFUSED no payload_sha on
// s:copies:card:<label>, exit 2, recorded as a bare "crash".
func copyConfig(l launch.Line, getenv func(string) string) (card.WrapperConfig, error) {
	return config(l, func(k string) string {
		if k == "NOVA_CARD_HARNESS" {
			return ""
		}
		return getenv(k)
	})
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
// <attempt>.line on the bench. With neither, nothing can be kept: the
// error says where the record could not go, and the caller prints it on
// stderr (keepRefusal), so a refusal that reached no store is at least on
// the one stream the bench operator can still read.
func recordRefusal(ctx context.Context, getenv func(string) string, st *store.Store, dial bool, l launch.Line, rep card.WrapperReport) error {
	bench := getenv("NOVA_CARD_BENCH")
	text := rep.Line() + " bench=" + oneline.Escape(bench) + " at=" + time.Now().UTC().Format(time.RFC3339)
	why := rep.Why
	if _, hex, ok := strings.Cut(l.Token, "."); ok && hex != "" {
		text = strings.ReplaceAll(text, hex, "<token>")
		why = strings.ReplaceAll(why, hex, "<token>")
	}
	ctx, cancel := context.WithTimeout(ctx, refusalTimeout)
	defer cancel()
	var missed []string
	if st == nil && dial && getenv("NOVA_CARD_REDIS") != "" {
		s, err := store.Open(ctx, getenv("NOVA_CARD_REDIS"))
		if err != nil {
			missed = append(missed, "redis: "+err.Error())
		} else {
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
			return nil
		}
		missed = append(missed, "xadd "+card.LogKey(l.Sprint)+": "+err.Error())
	}
	root := getenv("NOVA_CARD_RESULTS")
	if !filepath.IsAbs(root) {
		missed = append(missed, "NOVA_CARD_RESULTS is not an absolute path: nothing on disk either")
		return errors.New(strings.Join(missed, "; "))
	}
	// l's sprint and label passed launch.ParseLine ([a-z0-9-]), so the path
	// stays under root.
	dir := filepath.Join(root, "refused", l.Sprint, l.Label)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		missed = append(missed, err.Error())
		return errors.New(strings.Join(missed, "; "))
	}
	path := filepath.Join(dir, strconv.Itoa(l.Attempt)+".line")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		missed = append(missed, err.Error())
		return errors.New(strings.Join(missed, "; "))
	}
	if _, err := fmt.Fprintln(f, text); err != nil {
		_ = f.Close()
		missed = append(missed, "write "+path+": "+err.Error())
		return errors.New(strings.Join(missed, "; "))
	}
	if err := f.Close(); err != nil {
		missed = append(missed, "close "+path+": "+err.Error())
		return errors.New(strings.Join(missed, "; "))
	}
	return nil
}

// keepRefusal is recordRefusal with its failure on stderr: the refusal
// itself is already on stdout, so this line says only where the record
// could not be kept.
func keepRefusal(ctx context.Context, stderr io.Writer, getenv func(string) string, st *store.Store, dial bool, l launch.Line, rep card.WrapperReport) {
	if err := recordRefusal(ctx, getenv, st, dial, l, rep); err != nil {
		fmt.Fprintf(stderr, "nova-card: refusal of %s not recorded: %s\n", l.Card(), oneline.Escape(err.Error()))
	}
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

// runCopy is `nova-card copy <copy>` (#3998): the consumer copy's wrapper.
// Its one stdin line is <copy> <token>, and it must name the copy in argv.
func runCopy(id string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string, ack func(string)) int {
	sc := bufio.NewScanner(io.LimitReader(stdin, 4096))
	if !sc.Scan() {
		return refuse(stderr, "no copy line on stdin")
	}
	line, err := launch.ParseCopyLine(strings.TrimRight(sc.Text(), "\r"))
	if err != nil {
		return refuse(stderr, "stdin is not a copy line: "+err.Error())
	}
	if line.Copy != id {
		return refuse(stderr, "the copy line on stdin names "+oneline.Escape(line.Copy)+", not "+oneline.Escape(id))
	}
	n, err := card.CopyNumber(id)
	if err != nil {
		return refuse(stderr, err.Error())
	}
	l := launch.Line{Sprint: card.CopySprint, Label: card.CopyCardLabel(id), Attempt: n}
	cfg, err := copyConfig(l, getenv)
	if err != nil {
		ack("REFUSED " + err.Error())
		rep := card.WrapperReport{Code: card.WrapperExitUsage, Card: id, Why: err.Error()}
		fmt.Fprintln(stdout, rep.Line())
		return card.WrapperExitUsage
	}
	if p := getenv("NOVA_CARD_HARNESS"); p != "" {
		fmt.Fprintf(stderr, "nova-card: copy %s runs the Go harness in-process; NOVA_CARD_HARNESS=%s is a launched card's program, not a copy's (#4234)\n", id, oneline.Escape(p))
	}
	cfg.Copy = id
	ctx := context.Background()
	st, err := store.Open(ctx, getenv("NOVA_CARD_REDIS"))
	if err != nil {
		ack("REFUSED redis unavailable")
		rep := card.WrapperReport{Code: card.WrapperExitRedis, Card: id, Why: "redis: " + err.Error()}
		fmt.Fprintln(stdout, rep.Line())
		return card.WrapperExitRedis
	}
	defer st.Close()
	cfg.Store = st
	cfg.Started = func() { ack("LAUNCHED") }
	ledger := &card.CopyLedger{Client: st.Client(), Copy: id, Bench: cfg.Bench, Token: line.Token,
		PushToken: getenv(harvestcopy.TokenEnv)}
	if exe, err := os.Executable(); err == nil {
		ledger.Askpass = exe
	}
	rep := card.RunWrapper(ctx, cfg, ledger)
	if rep.Code != card.WrapperExitEnded {
		ack("REFUSED " + rep.Why)
	}
	fmt.Fprintln(stdout, rep.Line())
	return rep.Code
}
