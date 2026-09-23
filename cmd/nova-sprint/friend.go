// The friend verb (nova-tools #3101; spec #2756 v6 4.4, 5.5) registers itself
// through the S0 registry, so adding it never edits main.go. Every subverb is
// zero tokens: one Redis Function call or one pipelined read.
//
//	friend report --as <f> --out-of-credits [--until <t>]   (the harness keeper)
//	friend report --as <f> --away --until <t>
//	friend report --as <f> --clear
//	friend show [<f>]
//	friend sweep --as <actor> [--may-hold a,b] [--builders a,b] [--coordinator c]
//	capacity friend <f> --wake unit:<label>@<host> | --wake human --notify <channel>
//
// <t> is RFC 3339 or a duration from now (for example 3h).
package main

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/friend"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

func init() {
	register(Verb{
		Name:    "friend",
		Summary: "report a friend state, show friends, or run one ladder sweep (friend:<f>:state)",
		Run:     runFriend,
	})
}

func runFriend(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "friend", "want report, show or sweep")
	}
	switch args[0] {
	case "report":
		return runFriendReport(ctx, args[1:], out, errOut)
	case "show":
		return runFriendShow(ctx, args[1:], out, errOut)
	case "sweep":
		return runFriendSweep(ctx, args[1:], out, errOut)
	default:
		return refuse(errOut, "friend", fmt.Sprintf("unknown subverb %s; want report, show or sweep", args[0]))
	}
}

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
	redisAddr := fs.String("redis", "", "")
	as := fs.String("as", "", "")
	outOfCredits := fs.Bool("out-of-credits", false, "")
	away := fs.Bool("away", false, "")
	clear := fs.Bool("clear", false, "")
	until := fs.String("until", "", "")
	reason := fs.String("reason", "", "")
	actor := fs.String("actor", "", "")
	idem := fs.String("idem", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if *as == "" || len(fs.Args()) != 0 {
		return refuse(errOut, verb, "want --as <friend> and one of --out-of-credits, --away or --clear")
	}
	req := friend.ReportRequest{Friend: *as, Reason: *reason, Actor: *actor, Idem: *idem}
	if req.Actor == "" {
		req.Actor = *as
	}
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
	redisAddr := fs.String("redis", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if len(fs.Args()) > 1 {
		return refuse(errOut, verb, "want at most one friend; flags precede the name")
	}
	name := ""
	if len(fs.Args()) == 1 {
		name = fs.Args()[0]
	}
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

func splitNames(s string) []string {
	var out []string
	for _, n := range strings.Split(s, ",") {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	return out
}

func runFriendSweep(ctx context.Context, args []string, out, errOut io.Writer) int {
	const verb = "friend sweep"
	fs := capacityFlags(verb)
	redisAddr := fs.String("redis", "", "")
	as := fs.String("as", "", "")
	mayHold := fs.String("may-hold", "", "")
	builders := fs.String("builders", "", "")
	coordinator := fs.String("coordinator", "", "")
	idleTicks := fs.Int("idle-ticks", friend.DefaultPolicy.IdleTicks, "")
	underfullTicks := fs.Int("underfull-ticks", friend.DefaultPolicy.UnderfullTicks, "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if *as == "" || len(fs.Args()) != 0 {
		return refuse(errOut, verb, "want --as <actor>; the roster is --may-hold, --builders, --coordinator")
	}
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
		Roster: life.Roster{MayHold: splitNames(*mayHold), Builders: splitNames(*builders), Coordinator: *coordinator},
		Actor:  *as,
	}
	res, err := l.Sweep(ctx)
	for _, s := range res.Steps {
		fmt.Fprintln(out, s.Line())
	}
	for _, m := range res.Moves {
		fmt.Fprintln(out, m.Line())
	}
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	fmt.Fprintf(out, "SWEEP idem=%s steps=%d moves=%d\n", res.Idem, len(res.Steps), len(res.Moves))
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

// runCapacityWake is `capacity friend <f> --wake unit:<label>@<host>` or
// `--wake human --notify <channel>`: it declares friend:<f>:wakepath.
func runCapacityWake(ctx context.Context, args []string, out, errOut io.Writer) int {
	const verb = "capacity friend --wake"
	fs := capacityFlags(verb)
	redisAddr := fs.String("redis", "", "")
	wake := fs.String("wake", "", "")
	notify := fs.String("notify", "", "")
	actor := new(string)
	fs.StringVar(actor, "as", "", "")
	fs.StringVar(actor, "actor", "", "")
	idem := fs.String("idem", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	if *actor == "" {
		return refuse(errOut, verb, "--as actor is required")
	}
	if len(fs.Args()) != 1 {
		return refuse(errOut, verb, "want capacity friend --wake <path> [--notify <channel>] --as <actor> <name>; flags precede the name")
	}
	name := fs.Args()[0]
	wp, err := friend.ParseWakePath(*wake, *notify)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	defer st.Close()
	if err := friend.SetWakePath(ctx, st, name, wp, *actor, *idem); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	fmt.Fprintf(out, "SET friend %s wake=%s\n", name, wp)
	return 0
}
