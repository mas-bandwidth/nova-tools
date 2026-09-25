// friend serve and friend wake --as (nova-tools #2938): the seat loop that
// takes a friend's ready work and dispatches it to the friend's own harness at
// zero model tokens, so a friend who is not awake in a session still works
// her queue. Each subverb is one call into internal/nsprint/life.
//
//	friend serve --as <f> [--width <n>] [--dispatch "<argv>" | -- <argv...>] [--dir <root>]
//	             [--sprint <s>] [--host <h>] [--harness <h>] [--session <s>] [--login <alias>]...
//	             [--once] [--redis <addr>]
//	friend wake --as <f> [the same flags]     one serve pass: take, dispatch, watch, release
//
// The dispatch argv is the seat's own declaration: `--dispatch` or the argv
// after `--`, else the `dispatch` field of the cfg:friend:<f> hash
// (space-separated argv, no argument may contain a space, as fleet loops
// are spelled). In every argument @brief, @out, @dir, @sprint and @id are
// replaced per task, and the child also gets them as NOVA_TASK_* in its
// environment with NOVA_TASK_TOKEN, NOVA_TASK_KIND and NOVA_TASK_HEAD.
//
// Each --login alias is bound to the seat in friends:login once on start,
// through ns_friend_hello (#3797), so typed lines signed who=<alias> resolve
// to the friend; a clashing alias refuses and releases the seat.
//
// Exit 0 served (a loop stopped by a signal, or a one-shot pass complete);
// 1 refused with the remedy named (seat held, no dispatch, unregistered);
// 2 usage.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/life"
	"github.com/redis/go-redis/v9"
)

// cfgFriendKey is the seat's declared configuration: field dispatch.
func cfgFriendKey(friend string) string { return "cfg:friend:" + friend }

func runFriendServe(ctx context.Context, args []string, out, errOut io.Writer, once bool) int {
	verb := "friend serve"
	if once {
		verb = "friend wake"
	}
	fs, addr := lifeFlags(verb)
	as := fs.String("as", "", "friend name (must equal NOVA_FRIEND)")
	width := fs.Int("width", 0, "children at once; 0 reads friend:<f>:desired")
	dispatch := fs.String("dispatch", "", "harness argv, space separated (else cfg:friend:<f> dispatch)")
	dir := fs.String("dir", "", "root for per-task brief and output dirs")
	sprint := fs.String("sprint", "", "take from one sprint only")
	host := fs.String("host", "", "host the seat runs on")
	harness := fs.String("harness", "", "harness identity for the beat")
	session := fs.String("session", "", "seat session identity")
	onceFlag := fs.Bool("once", false, "one pass, then release the seat")
	var logins loginFlags
	fs.Var(&logins, "login", "a login alias bound to this seat on start (repeatable; #3797)")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, verb, err.Error())
	}
	argv := fs.Args()
	if *as == "" {
		return refuse(errOut, verb, "--as is required")
	}
	if *dispatch != "" && len(argv) > 0 {
		return refuse(errOut, verb, "give the harness as --dispatch \"<argv>\" or after --, not both")
	}
	if *width < 0 {
		return refuse(errOut, verb, "--width must be at least 1, or 0 to read friend:<f>:desired")
	}
	// The seat contract (#2929 rev 4): serve takes for --as, so --as is the
	// seat. Both refusals come before any dial.
	initiator := os.Getenv(seatEnv)
	if initiator == "" {
		return refuse(errOut, verb, "want NOVA_FRIEND")
	}
	if *as != initiator {
		return refuse(errOut, verb, fmt.Sprintf("want --as equal to NOVA_FRIEND (NOVA_FRIEND=%s, --as %s)", initiator, *as))
	}
	if *host == "" {
		var err error
		if *host, err = os.Hostname(); err != nil {
			return refuse(errOut, verb, err.Error())
		}
	}
	if *session == "" {
		var err error
		if *session, err = newLifeSession(); err != nil {
			return refuse(errOut, verb, err.Error())
		}
	}
	if *dir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return refuse(errOut, verb, "no home for the default --dir: "+err.Error())
		}
		*dir = filepath.Join(home, life.DefaultRoot, "serve", *as)
	}
	st, err := openLifeStore(ctx, lifeAddr(*addr))
	if err != nil {
		return refuse(errOut, verb, err.Error())
	}
	defer st.Close()

	// One pipeline: the declared dispatch and the desired width.
	pipe := st.Client().Pipeline()
	dispatchCmd := pipe.HGet(ctx, cfgFriendKey(*as), "dispatch")
	desiredCmd := pipe.HGet(ctx, "friend:"+*as+":desired", "slots")
	if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
		return refuse(errOut, verb, err.Error())
	}
	switch {
	case *dispatch != "":
		argv = strings.Fields(*dispatch)
	case len(argv) > 0:
	default:
		argv = strings.Fields(dispatchCmd.Val())
	}
	if len(argv) == 0 {
		fmt.Fprintf(errOut, "FRIEND SERVE %s REFUSED no-dispatch: declare the harness with HSET %s dispatch '<argv>' (for example: claude -p @brief) or pass --dispatch\n", *as, cfgFriendKey(*as))
		return 1
	}
	if *width == 0 {
		n, err := strconv.Atoi(desiredCmd.Val())
		if err != nil || n < 1 {
			fmt.Fprintf(errOut, "FRIEND SERVE %s REFUSED no-width: friend:%s:desired has no slots; run nova-sprint capacity friend, or pass --width\n", *as, *as)
			return 1
		}
		*width = n
	}
	cfg := life.ServeConfig{
		Friend: *as, Session: *session, Host: *host, Harness: *harness, Sprint: *sprint,
		Width: *width, Dispatch: argv, Dir: *dir, Actor: initiator, Out: out,
		Logins: logins,
	}
	if once || *onceFlag {
		res, err := life.ServeOnce(ctx, st, cfg)
		if err != nil {
			return serveRefused(errOut, *as, err)
		}
		fmt.Fprintf(out, "SERVE %s once taken=%d closed=%d width=%d\n", *as, res.Taken, res.Closed, *width)
		return 0
	}
	signalCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := life.Serve(signalCtx, st, cfg); err != nil {
		return serveRefused(errOut, *as, err)
	}
	fmt.Fprintf(out, "SERVE %s stopped\n", *as)
	return 0
}

// serveRefused prints the one refusal line with its remedy: exit 1.
func serveRefused(errOut io.Writer, friend string, err error) int {
	var held *life.SeatHeldError
	var login *life.LoginError
	switch {
	case errors.As(err, &login):
		fmt.Fprintf(errOut, "FRIEND SERVE %s REFUSED login %s: drop or rename that --login (nova-sprint friend hello --as <owner> binds a login to its owner); the seat was released\n",
			friend, login.Words)
	case errors.As(err, &held):
		fmt.Fprintf(errOut, "FRIEND SERVE %s REFUSED seat-held holder=%s: stop that serve (its unit, or nova-sprint friend bye --as %s from its session) or wait %s for its lock to lapse\n",
			friend, held.Holder, friend, life.ServeLockTTL)
	case errors.Is(err, life.ErrUnregistered):
		fmt.Fprintf(errOut, "FRIEND SERVE %s REFUSED unregistered: nova-sprint capacity friend %s --as <actor> --machine <m> <slots>\n", friend, friend)
	default:
		fmt.Fprintf(errOut, "FRIEND SERVE %s REFUSED %s\n", friend, err.Error())
	}
	return 1
}

// hasAsFlag reports whether a `friend wake` argv names a seat with --as, which
// makes it a one-shot serve pass rather than the reconciler's wake routing.
func hasAsFlag(args []string) bool {
	for _, a := range args {
		if a == "--as" || a == "-as" || strings.HasPrefix(a, "--as=") || strings.HasPrefix(a, "-as=") {
			return true
		}
	}
	return false
}
