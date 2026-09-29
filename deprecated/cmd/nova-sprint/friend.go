// Package main: friend CLI verbs for nova-sprint.
// Issue #4356 Item F:
// 1. friend tell <f> "<text>": sends message over bus (ev:friend / friend:outbox) in one call.
// 2. friend ask <f> --card <id>: deals card <id> to friend and posts brief in one atomic call.
// 3. friend tiers <f> <tiers>: sets advertised model tiers in Redis.
// 4. friend slots <f> <n>: sets capacity slots in Redis.
// 5. friend pause <f> / friend resume <f>: sets/clears paused flag on friend's record in Redis.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strconv"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/friend"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
)

func init() {
	register(Verb{
		Name:    "friend",
		Summary: "comms, capacity and status of friends: tell, ask, tiers, slots, pause, resume",
		Run:     runFriend,
	})
}

func friendFlags(name string) *flag.FlagSet {
	return verbflag.New(name)
}

func runFriend(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "friend", "want tell, ask, tiers, slots, pause or resume")
	}
	switch args[0] {
	case "tell":
		return runFriendTell(ctx, args[1:], out, errOut)
	case "ask":
		return runFriendAsk(ctx, args[1:], out, errOut)
	case "tiers":
		return runFriendTiers(ctx, args[1:], out, errOut)
	case "slots":
		return runFriendSlots(ctx, args[1:], out, errOut)
	case "pause":
		return runFriendPause(ctx, args[1:], true, out, errOut)
	case "resume":
		return runFriendPause(ctx, args[1:], false, out, errOut)
	default:
		return refuse(errOut, "friend", fmt.Sprintf("unknown subverb %s; want tell, ask, tiers, slots, pause or resume", args[0]))
	}
}

func runFriendTell(ctx context.Context, args []string, out, errOut io.Writer) int {
	const verb = "friend tell"
	fs := friendFlags(verb)
	redisAddr := fs.String("redis", redisDefault(), "")
	from := fs.String("from", "", "")
	fs.StringVar(from, "as", "", "")
	busDir := fs.String("bus", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	pos := fs.Args()
	if len(pos) < 2 {
		return refuse(errOut, verb, "want friend tell <friend> \"<text>\"")
	}
	target, text := pos[0], pos[1]
	st, err := store.Open(ctx, taskAddr(*redisAddr))
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	defer st.Close()

	res, err := friend.Tell(ctx, st.Client(), friend.TellRequest{
		Friend: target,
		Text:   text,
		Actor:  *from,
		BusDir: *busDir,
	})
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	fmt.Fprintln(out, res.Line())
	return 0
}

func runFriendAsk(ctx context.Context, args []string, out, errOut io.Writer) int {
	const verb = "friend ask"
	fs := friendFlags(verb)
	redisAddr := fs.String("redis", redisDefault(), "")
	cardID := fs.String("card", "", "")
	actor := fs.String("as", "", "")
	fs.StringVar(actor, "from", "", "")
	busDir := fs.String("bus", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	pos := fs.Args()
	if len(pos) < 1 {
		return refuse(errOut, verb, "want friend ask <friend> --card <id>")
	}
	target := pos[0]
	if *cardID == "" {
		return refuse(errOut, verb, "--card <id> is required")
	}
	st, err := store.Open(ctx, taskAddr(*redisAddr))
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	defer st.Close()

	res, err := friend.Ask(ctx, st.Client(), friend.AskRequest{
		Friend: target,
		CardID: *cardID,
		Actor:  *actor,
		BusDir: *busDir,
	})
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	fmt.Fprintln(out, res.Line())
	return 0
}

func runFriendTiers(ctx context.Context, args []string, out, errOut io.Writer) int {
	const verb = "friend tiers"
	fs := friendFlags(verb)
	redisAddr := fs.String("redis", redisDefault(), "")
	actor := fs.String("as", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	pos := fs.Args()
	if len(pos) < 2 {
		return refuse(errOut, verb, "want friend tiers <friend> <tiers>")
	}
	target, tiers := pos[0], pos[1]
	st, err := store.Open(ctx, taskAddr(*redisAddr))
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	defer st.Close()

	res, err := friend.SetTiers(ctx, st.Client(), friend.TiersRequest{
		Friend: target,
		Tiers:  tiers,
		Actor:  *actor,
	})
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	fmt.Fprintln(out, res.Line())
	return 0
}

func runFriendSlots(ctx context.Context, args []string, out, errOut io.Writer) int {
	const verb = "friend slots"
	fs := friendFlags(verb)
	redisAddr := fs.String("redis", redisDefault(), "")
	actor := fs.String("as", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	pos := fs.Args()
	if len(pos) < 2 {
		return refuse(errOut, verb, "want friend slots <friend> <n>")
	}
	target := pos[0]
	n, err := strconv.Atoi(pos[1])
	if err != nil || n < 0 {
		return refuse(errOut, verb, fmt.Sprintf("invalid slots %q: must be non-negative integer", pos[1]))
	}
	st, err := store.Open(ctx, taskAddr(*redisAddr))
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	defer st.Close()

	res, err := friend.SetSlots(ctx, st.Client(), friend.SlotsRequest{
		Friend: target,
		Slots:  n,
		Actor:  *actor,
	})
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	fmt.Fprintln(out, res.Line())
	return 0
}

func runFriendPause(ctx context.Context, args []string, pause bool, out, errOut io.Writer) int {
	verb := "friend resume"
	if pause {
		verb = "friend pause"
	}
	fs := friendFlags(verb)
	redisAddr := fs.String("redis", redisDefault(), "")
	actor := fs.String("as", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	pos := fs.Args()
	if len(pos) < 1 {
		return refuse(errOut, verb, fmt.Sprintf("want %s <friend>", verb))
	}
	target := pos[0]
	st, err := store.Open(ctx, taskAddr(*redisAddr))
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	defer st.Close()

	var res *friend.PauseResult
	if pause {
		res, err = friend.Pause(ctx, st.Client(), target, *actor)
	} else {
		res, err = friend.Resume(ctx, st.Client(), target, *actor)
	}
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	fmt.Fprintln(out, res.Line())
	return 0
}
