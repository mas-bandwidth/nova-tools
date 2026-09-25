package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fleet"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

func init() {
	register(Verb{
		Name:    "fleet",
		Summary: "fleet state, is-up, hold, release, and config",
		Run:     runFleet,
	})
}

func fleetAddr(addr string) string {
	if addr != "" {
		return addr
	}
	if v := os.Getenv("NOVA_SPRINT_REDIS"); v != "" {
		return v
	}
	if v := os.Getenv("NOVA_REDIS_ADDR"); v != "" {
		return v
	}
	return "127.0.0.1:6379"
}

func openFleetStore(ctx context.Context, addr string) (*store.Store, error) {
	return store.Open(ctx, fleetAddr(addr))
}

func unreachable(stderr io.Writer, verb, what string) int {
	where := ""
	if verb != "" {
		where = " " + verb
	}
	fmt.Fprintf(stderr, "nova-sprint%s: %s\n", where, oneline.Escape(what))
	return 5
}

func runFleet(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "fleet", "wants state, is-up, hold, release, or config")
	}
	switch args[0] {
	case "state":
		return runFleetState(ctx, args[1:], out, errOut)
	case "is-up":
		return runFleetIsUp(ctx, args[1:], out, errOut)
	case "hold":
		return runFleetHold(ctx, args[1:], out, errOut)
	case "release":
		return runFleetRelease(ctx, args[1:], out, errOut)
	case "config":
		return runFleetConfig(ctx, args[1:], out, errOut)
	default:
		return refuse(errOut, "fleet", fmt.Sprintf("unknown subverb %s; want state, is-up, hold, release, or config", args[0]))
	}
}

func runFleetState(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("fleet state", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	redisAddr := fs.String("redis", "", "")
	bench := fs.String("bench", "", "")
	upOnly := fs.Bool("up", false, "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "fleet state", err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, "fleet state", "takes no positional arguments")
	}
	st, err := openFleetStore(ctx, *redisAddr)
	if err != nil {
		return unreachable(errOut, "fleet state", err.Error())
	}
	defer st.Close()

	rows, err := fleet.Read(ctx, st.Client(), *bench)
	if err != nil {
		if errors.Is(err, fleet.ErrUnregistered) {
			return refuse(errOut, "fleet state", "unregistered bench "+*bench)
		}
		return refuse(errOut, "fleet state", err.Error())
	}
	if *upOnly {
		for _, r := range rows {
			if r.State == fleet.StateUp {
				fmt.Fprintln(out, r.Bench)
			}
		}
		return 0
	}
	for _, r := range rows {
		fmt.Fprintf(out, "%s\t%s\t%s\t%s\t%s\n", r.Bench, r.State, r.Since, r.Build, r.Reason)
	}
	return 0
}

func runFleetIsUp(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("fleet is-up", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	redisAddr := fs.String("redis", "", "")
	bench := fs.String("bench", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "fleet is-up", err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, "fleet is-up", "takes no positional arguments")
	}
	if *bench == "" {
		return refuse(errOut, "fleet is-up", "--bench is required")
	}
	st, err := openFleetStore(ctx, *redisAddr)
	if err != nil {
		return unreachable(errOut, "fleet is-up", err.Error())
	}
	defer st.Close()

	rows, err := fleet.Read(ctx, st.Client(), *bench)
	if err != nil {
		if errors.Is(err, fleet.ErrUnregistered) {
			return refuse(errOut, "fleet is-up", "unregistered bench "+*bench)
		}
		return refuse(errOut, "fleet is-up", err.Error())
	}
	if len(rows) == 0 {
		return refuse(errOut, "fleet is-up", "unregistered bench "+*bench)
	}
	state := rows[0].State
	fmt.Fprintln(out, state)
	switch state {
	case fleet.StateUp:
		return 0
	case fleet.StateDown:
		return 1
	case fleet.StateProbing:
		return 3
	case fleet.StateHeld:
		return 4
	default:
		return 2
	}
}

func runFleetHold(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("fleet hold", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	redisAddr := fs.String("redis", "", "")
	bench := fs.String("bench", "", "")
	why := fs.String("why", "", "")
	by := fs.String("by", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "fleet hold", err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, "fleet hold", "takes no positional arguments")
	}
	if *bench == "" || *why == "" {
		return refuse(errOut, "fleet hold", "--bench and --why are required")
	}
	st, err := openFleetStore(ctx, *redisAddr)
	if err != nil {
		return unreachable(errOut, "fleet hold", err.Error())
	}
	defer st.Close()

	if err := fleet.Hold(ctx, st.Client(), *bench, *why, *by); err != nil {
		if errors.Is(err, fleet.ErrUnregistered) {
			return refuse(errOut, "fleet hold", "unregistered bench "+*bench)
		}
		return refuse(errOut, "fleet hold", err.Error())
	}
	return 0
}

func runFleetRelease(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("fleet release", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	redisAddr := fs.String("redis", "", "")
	bench := fs.String("bench", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "fleet release", err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, "fleet release", "takes no positional arguments")
	}
	if *bench == "" {
		return refuse(errOut, "fleet release", "--bench is required")
	}
	st, err := openFleetStore(ctx, *redisAddr)
	if err != nil {
		return unreachable(errOut, "fleet release", err.Error())
	}
	defer st.Close()

	if err := fleet.Release(ctx, st.Client(), *bench); err != nil {
		if errors.Is(err, fleet.ErrNotHeld) {
			return refuse(errOut, "fleet release", "bench "+*bench+" is not held")
		}
		if errors.Is(err, fleet.ErrUnregistered) {
			return refuse(errOut, "fleet release", "unregistered bench "+*bench)
		}
		return refuse(errOut, "fleet release", err.Error())
	}
	return 0
}

func runFleetConfig(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("fleet config", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	redisAddr := fs.String("redis", "", "")
	downAfter := fs.Int("down-after", 0, "")
	upAfter := fs.Int("up-after", 0, "")
	sshFailAfter := fs.Int("ssh-fail-after", 0, "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "fleet config", err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, "fleet config", "takes no positional arguments")
	}

	downSet := false
	upSet := false
	sshSet := false
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "down-after":
			downSet = true
		case "up-after":
			upSet = true
		case "ssh-fail-after":
			sshSet = true
		}
	})
	if downSet && *downAfter < 1 {
		return refuse(errOut, "fleet config", "--down-after must be >= 1")
	}
	if upSet && *upAfter < 1 {
		return refuse(errOut, "fleet config", "--up-after must be >= 1")
	}
	if sshSet && *sshFailAfter < 1 {
		return refuse(errOut, "fleet config", "--ssh-fail-after must be >= 1")
	}

	st, err := openFleetStore(ctx, *redisAddr)
	if err != nil {
		return unreachable(errOut, "fleet config", err.Error())
	}
	defer st.Close()

	curDown, curUp, err := fleet.Config(ctx, st.Client())
	if err != nil {
		return refuse(errOut, "fleet config", err.Error())
	}

	if downSet {
		curDown = *downAfter
	}
	if upSet {
		curUp = *upAfter
	}
	if downSet || upSet {
		if err := fleet.SetConfig(ctx, st.Client(), curDown, curUp); err != nil {
			return refuse(errOut, "fleet config", err.Error())
		}
	}
	if sshSet {
		if err := fleet.SetSessionFailAfter(ctx, st.Client(), *sshFailAfter); err != nil {
			return refuse(errOut, "fleet config", err.Error())
		}
	}
	curSSH, err := fleet.SessionFailAfter(ctx, st.Client())
	if err != nil {
		return refuse(errOut, "fleet config", err.Error())
	}
	fmt.Fprintf(out, "down_after=%d up_after=%d ssh_fail_after=%d\n", curDown, curUp, curSSH)
	return 0
}
