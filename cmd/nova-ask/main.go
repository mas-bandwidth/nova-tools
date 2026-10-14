// nova-ask asks a friend over the psychic link (nova-bus) and, when she does
// not answer by the deadline, wakes her at her harness window and asks again.
// Glenn, 2026-10-14: "Never wait forever. Always timeout." Every wait is
// bounded: the whole run is at most 2 x --deadline + WakeBudget.
//
//	nova-ask --as rowan --to stella --subject <s> --body <b> [--deadline 60s] [--redis 127.0.0.1:6381]
//
// Exit 0 ANSWERED; 1 NO ANSWER (notify Glenn); 2 could not run, or the wake
// needs the one-time Accessibility grant, named on the line.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"time"

	"github.com/mas-bandwidth/nova-tools/hook"
	"github.com/mas-bandwidth/nova-tools/pkg/bus"
	"github.com/mas-bandwidth/nova-tools/pkg/hookgate"
	"github.com/mas-bandwidth/nova-tools/pkg/redisconn"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }

type realClock struct{}

func (realClock) Now() time.Time        { return time.Now() }
func (realClock) Sleep(d time.Duration) { time.Sleep(d) }

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("nova-ask", flag.ContinueOnError)
	fs.SetOutput(stderr)
	as := fs.String("as", "", "your name, the sender")
	to := fs.String("to", "", "the friend asked")
	subject := fs.String("subject", "", "one line saying what the ask is")
	body := fs.String("body", "", "the ask's text")
	deadline := fs.Duration("deadline", 60*time.Second, "how long to wait for an answer before waking, and again after")
	redisAddr := fs.String("redis", os.Getenv(bus.RedisEnv), "the bus store, host:port")
	if err := fs.Parse(args); err != nil {
		return ExitRefused
	}
	if *to == "" || *subject == "" || *body == "" || *deadline <= 0 {
		fmt.Fprintln(stdout, "REFUSED --to, --subject, --body and a --deadline above zero are required")
		return ExitRefused
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2**deadline+WakeBudget+2*time.Second)
	defer cancel()

	octx, ocancel := context.WithTimeout(ctx, redisconn.OpenTimeout)
	addr, why := bus.Address(octx, *redisAddr, os.Getenv, func(ctx context.Context, a string) (string, error) {
		return bus.ReadFleetBus(ctx, a, os.Getenv)
	})
	if why == "" {
		why = bus.CheckAddr(octx, addr, func(ctx context.Context, host string) ([]netip.Addr, error) {
			return net.DefaultResolver.LookupNetIP(ctx, "ip", host)
		})
	}
	if why != "" {
		ocancel()
		fmt.Fprintln(stdout, "REFUSED "+why)
		return ExitRefused
	}
	st, login, closeStore, err := bus.OpenRedis(octx, addr, os.Getenv)
	ocancel()
	if err != nil {
		fmt.Fprintln(stdout, "REFUSED "+err.Error())
		return ExitRefused
	}
	defer closeStore()
	me, why := bus.Identity(*as, login)
	if why != "" {
		fmt.Fprintln(stdout, "REFUSED "+why)
		return ExitRefused
	}
	b := &bus.Bus{Store: st}
	// the hook gate nova-bus send meets before it writes (docs/SPEC-HOOK.md)
	gctx, gcancel := context.WithTimeout(ctx, bus.CallTimeout)
	err = hookgate.Gate(gctx, st, hook.Want{Name: me, Need: []hook.Push{hook.Bus}})
	gcancel()
	if err != nil {
		fmt.Fprintln(stdout, "REFUSED not hooked in: "+err.Error())
		return ExitRefused
	}
	a := &Ask{Bus: b, Clock: realClock{}, As: me, To: *to, Subject: *subject, Body: *body, Deadline: *deadline, Out: stdout}
	if mk, ok := Adapters[*to]; ok {
		a.Waker = mk()
	}
	return a.Run(ctx)
}
