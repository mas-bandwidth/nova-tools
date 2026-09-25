package main

// nova-sprint route runs the routing rules of one sprint in one process
// under one lease (#2756 section 11 rows 2 and 5, 4.5; nova-tools #3036):
//
//	nova-sprint route --redis <addr> --sprint <S> [--actor <name>]
//
// Rules wired: ok-to-friend (#2933), report-to-read (#3036) and pr-to-read
// (#2941 heads and reads, joined with #3040 runner rows and no-card PR
// adoption from ev:github, each adoption cutting its ci card). Not wired yet, each one line here when its handler lands: the
// 3.3 classification (#3076, consume.Classifier is already a consume.Handler)
// and hold-to-fix (#3092, consume.HoldToFix).
//
// Exit 0 stopped cleanly (SIGINT or SIGTERM; the lease is released), 1
// REFUSED (another instance holds lease:route:<S>), 2 could not run or a
// rule failed or the lease was lost.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/consume"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

// routeNotWired names the rules the router does not run yet and the issue
// that brings each; the start line prints them so a reader never assumes a
// rule is running.
const routeNotWired = "classify(#3076),hold-to-fix(#3092)"

func init() {
	register(Verb{
		Name:    "route",
		Summary: "run ok-to-friend, report-to-read, pr-to-read (and, once built, classify, hold-to-fix) for one sprint in one process under lease:route:<S>; a second instance exits 1 REFUSED",
		Run:     runRoute,
	})
}

func runRoute(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := taskFlags("route")
	redisAddr := fs.String("redis", "", "")
	sprint := fs.String("sprint", "", "")
	actor := fs.String("actor", "route", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "route", err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, "route", "takes flags, not positional arguments")
	}
	if *redisAddr == "" || *sprint == "" {
		return refuse(errOut, "route", "needs --redis <addr> and --sprint <S>")
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	st, err := openTaskStore(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "route", err.Error())
	}
	defer st.Close()
	instance, err := consume.NewInstance()
	if err != nil {
		return refuse(errOut, "route", err.Error())
	}
	host, _ := os.Hostname()
	ok := &consume.OkFriend{Store: st, Sprint: *sprint, Consumer: host, Actor: *actor}
	report := &consume.Report{Store: st, Sprint: *sprint, Consumer: host, Actor: *actor}
	read := &consume.PRRead{Store: st, Sprint: *sprint, Consumer: host, Instance: instance, Actor: *actor, Remote: consumePRReadRemote}
	runner := routePRToRead(st, *sprint, host, *actor, out)
	pr := consume.JoinPRToRead(read, runner)
	router := &consume.Router{
		Store: st, Sprint: *sprint, Instance: instance, Host: host,
		Rules: consume.Rules(ok, report, nil, pr, nil),
	}
	fmt.Fprintf(out, "ROUTE sprint=%s instance=%s lease=%s rules=%s not-wired=%s\n",
		*sprint, instance, consume.LeaseKey(*sprint), strings.Join(router.Names(), ","), routeNotWired)
	err = router.Run(ctx)
	var held *consume.LeaseHeldError
	switch {
	case errors.As(err, &held):
		fmt.Fprintf(errOut, "nova-sprint route: REFUSED %s held by %s at %s; one router per sprint\n",
			consume.LeaseKey(*sprint), held.Holder, held.At)
		return 1
	case err != nil:
		fmt.Fprintf(errOut, "nova-sprint route: %v\n", err)
		return 2
	}
	fmt.Fprintf(out, "ROUTE STOPPED sprint=%s instance=%s lease released\n", *sprint, instance)
	return 0
}

// routePRToRead is the pr-to-read rule exactly as runRoute wires it (the
// route control TestRoutePRToReadCutsCI builds it here): an adoption cuts its
// ci card at the PR head (ns_ci_cut) before its reads; a base tip the lander
// has not recorded prints `WAIT <id> no base tip` and adopts nothing, never a
// stop.
func routePRToRead(st *store.Store, sprint, instance, actor string, out io.Writer) *consume.PRToReadRule {
	return &consume.PRToReadRule{Store: st, Sprint: sprint, Consumer: instance, Actor: actor, Out: out,
		CICut: consume.StoreCICut(st, actor)}
}
