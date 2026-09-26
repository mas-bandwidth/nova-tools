// friend report, show and sweep (nova-tools #3101; spec #2756 v6 4.4, 5.5)
// are subverbs of the friend verb life.go registers. Every subverb is zero
// tokens: one Redis Function call or one pipelined read.
//
//	friend report --as <f> --out-of-credits [--until <t>]   (the harness keeper)
//	friend report --as <f> --away --until <t>
//	friend report --as <f> --clear
//	friend show [--as <f>]
//	friend sweep [--idle-ticks <n>] [--underfull-ticks <n>]
//	capacity friend --as <f> --wake unit:<label>@<host> | --wake human --notify <channel>
//
// One grammar (#4352 A): --as is the friend the verb concerns; the actor of
// a receipt is the seat, never a flag.
//
// <t> is RFC 3339 or a duration from now (for example 3h).
package main

import (
	"context"
	"fmt"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/friend"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// These subverbs join dev's `friend` verb (life.go: hello, bye, wake), which
// dispatches report, show and sweep here; one verb, one registration.

// parseUntil reads RFC 3339 or a positive duration from now.
func parseUntil(s string, now time.Time) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil || d <= 0 {
		return time.Time{}, fmt.Errorf("--until %q: want RFC 3339 or a positive duration such as 3h", s)
	}
	return now.Add(d), nil
}

func runFriendReport(ctx context.Context, args []string, out, errOut io.Writer) int {
	const verb = "friend report"
	fs := capacityFlags(verb)
	redisAddr := fs.String("redis", redisDefault(), verbflag.HelpRedis)
	as := fs.String("as", "", verbflag.HelpAs)
	outOfCredits := fs.Bool("out-of-credits", false, "the friend is out of credits")
	away := fs.Bool("away", false, "the friend is away")
	clear := fs.Bool("clear", false, "the friend is back")
	until := fs.String("until", "", "until when, RFC 3339 or a duration from now (3h)")
	why := fs.String("why", "", verbflag.HelpWhy)
	idem := fs.String("idem", "", verbflag.HelpIdem)
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if *as == "" || len(fs.Args()) != 0 {
		return refuse(errOut, verb, "want --as <friend> and one of --out-of-credits, --away or --clear")
	}
	*as = strings.TrimPrefix(*as, "friend:")
	req := friend.ReportRequest{Friend: *as, Reason: *why, Actor: seatActor(), Idem: *idem}
	n := 0
	for flag, state := range map[*bool]string{outOfCredits: friend.StateOutOfCredits, away: friend.StateAway, clear: "clear"} {
		if *flag {
			req.State = state
			n++
		}
	}
	if n != 1 {
		return refuse(errOut, verb, "want exactly one of --out-of-credits, --away or --clear")
	}
	t, err := parseUntil(*until, time.Now())
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if req.State == "clear" && !t.IsZero() {
		return refuse(errOut, verb, "--clear takes no --until")
	}
	req.Until = t
	if req.Reason == "" {
		req.Reason = map[string]string{friend.StateOutOfCredits: "usage limit", friend.StateAway: "declared absence", "clear": "reported back"}[req.State]
	}
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	defer st.Close()
	if err := friend.Report(ctx, st, req); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	state := req.State
	if state == "clear" {
		state = friend.StateUp
	}
	line := fmt.Sprintf("REPORT friend=%s state=%s", req.Friend, state)
	if !req.Until.IsZero() {
		line += " until=" + req.Until.UTC().Format(time.RFC3339)
	}
	fmt.Fprintln(out, line)
	return 0
}

func runFriendShow(ctx context.Context, args []string, out, errOut io.Writer) int {
	const verb = "friend show"
	fs := capacityFlags(verb)
	redisAddr := fs.String("redis", redisDefault(), verbflag.HelpRedis)
	as := fs.String("as", "", verbflag.HelpAs)
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if len(fs.Args()) > 0 {
		return refuse(errOut, verb, "takes flags, not positional arguments; one friend is --as <f>")
	}
	name := strings.TrimPrefix(*as, "friend:")
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	defer st.Close()
	rows, err := friend.Show(ctx, st, name)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	for _, r := range rows {
		fmt.Fprintln(out, r.Line())
	}
	return 0
}

func runFriendSweep(ctx context.Context, args []string, out, errOut io.Writer) int {
	const verb = "friend sweep"
	fs := capacityFlags(verb)
	redisAddr := fs.String("redis", redisDefault(), verbflag.HelpRedis)
	idleTicks := fs.Int("idle-ticks", friend.DefaultPolicy.IdleTicks, "ticks a friend may sit idle before the sweep acts")
	underfullTicks := fs.Int("underfull-ticks", friend.DefaultPolicy.UnderfullTicks, "ticks a friend may run under its width before the sweep acts")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if len(fs.Args()) != 0 {
		return refuse(errOut, verb, "takes flags, not positional arguments")
	}
	actor := seatActor()
	as := &actor
	if *idleTicks < 1 || *underfullTicks < 1 {
		return refuse(errOut, verb, "--idle-ticks and --underfull-ticks must be at least 1")
	}
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	defer st.Close()
	l := &friend.Ladder{
		Store:  st,
		Policy: friend.Policy{IdleTicks: *idleTicks, UnderfullTicks: *underfullTicks},
		Actor:  *as,
	}
	res, err := l.Sweep(ctx)
	for _, a := range res.Life {
		fmt.Fprintln(out, a.Line())
	}
	for _, s := range res.Steps {
		fmt.Fprintln(out, s.Line())
	}
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	fmt.Fprintf(out, "SWEEP idem=%s steps=%d\n", res.Idem, len(res.Steps))
	return 0
}

// hasWakeFlag reports whether a `capacity friend` argv declares a wake path.
func hasWakeFlag(args []string) bool {
	for _, a := range args {
		if a == "--wake" || a == "-wake" || strings.HasPrefix(a, "--wake=") || strings.HasPrefix(a, "-wake=") {
			return true
		}
	}
	return false
}

// runCapacityWake is `capacity friend --as <f> --wake unit:<label>@<host>`
// or `... --wake human --notify <channel>`: it declares friend:<f>:wakepath.
func runCapacityWake(ctx context.Context, args []string, out, errOut io.Writer) int {
	const verb = "capacity friend --wake"
	fs := capacityFlags(verb)
	redisAddr := fs.String("redis", redisDefault(), verbflag.HelpRedis)
	wake := fs.String("wake", "", "the wake path: unit:<label>@<host>, or human")
	notify := fs.String("notify", "", "the channel a human wake notifies (with --wake human)")
	as := fs.String("as", "", verbflag.HelpAs)
	idem := fs.String("idem", "", verbflag.HelpIdem)
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if fs.NArg() > 0 || *as == "" {
		return refuse(errOut, verb, "want capacity friend --as <f> --wake <path> [--notify <channel>]")
	}
	actor := seatActor()
	name := strings.TrimPrefix(*as, "friend:")
	wp, err := friend.ParseWakePath(*wake, *notify)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	defer st.Close()
	if err := friend.SetWakePath(ctx, st, name, wp, actor, *idem); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	fmt.Fprintf(out, "SET friend %s wake=%s\n", name, wp)
	return 0
}
