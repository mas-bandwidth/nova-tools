package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/unit"
)

func init() {
	register(Verb{
		Name:    "units",
		Summary: "plan, declare, apply and check fleet units (reconciler, table, consumer, friend hello, bench beat, harvest)",
		Run:     runUnits,
	})
}

type friendFlag map[string]int

func (f friendFlag) String() string { return "" }
func (f friendFlag) Set(value string) error {
	parts := strings.SplitN(value, "=", 2)
	if len(parts) != 2 {
		return fmt.Errorf("want name=slots")
	}
	var slots int
	if _, err := fmt.Sscanf(parts[1], "%d", &slots); err != nil {
		return fmt.Errorf("invalid slots %s: %w", parts[1], err)
	}
	f[parts[0]] = slots
	return nil
}

func runUnits(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "units", "want plan, declare, apply or check")
	}
	switch args[0] {
	case "plan":
		return runUnitsPlan(ctx, args[1:], out, errOut)
	case "declare":
		return runUnitsDeclare(ctx, args[1:], out, errOut)
	case "apply":
		return runUnitsApply(ctx, args[1:], out, errOut)
	case "check":
		return runUnitsCheck(ctx, args[1:], out, errOut)
	default:
		return refuse(errOut, "units", fmt.Sprintf("unknown subverb %s; want plan, declare, apply or check", args[0]))
	}
}

func parseUnitOpts(args []string) (unit.PlanOptions, string, bool, string, error) {
	fs := flag.NewFlagSet("units", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	host := fs.String("host", "", "")
	role := fs.String("role", "", "")
	friends := make(friendFlag)
	fs.Var(friends, "friend", "")
	storeAddr := fs.String("store", "", "")
	redisAddr := fs.String("redis", "", "")
	osName := fs.String("os", "darwin", "")
	seat := fs.String("seat", "operator", "")
	remove := fs.Bool("remove", false, "")

	if err := fs.Parse(args); err != nil {
		return unit.PlanOptions{}, "", false, "", err
	}
	addr := *storeAddr
	if addr == "" {
		addr = *redisAddr
	}
	if addr == "" {
		addr = "127.0.0.1:6379"
	}
	opts := unit.PlanOptions{
		Host:    *host,
		Role:    *role,
		Friends: friends,
		Store:   addr,
		OS:      *osName,
	}
	return opts, *seat, *remove, addr, nil
}

func runUnitsPlan(ctx context.Context, args []string, out, errOut io.Writer) int {
	opts, _, _, _, err := parseUnitOpts(args)
	if err != nil {
		return refuse(errOut, "units plan", err.Error())
	}
	digests, _, planSha, err := unit.Plan(opts)
	if err != nil {
		return refuse(errOut, "units plan", err.Error())
	}
	var names []string
	for name := range digests {
		names = append(names, name)
	}
	sortStrings(names)
	for _, name := range names {
		fmt.Fprintf(out, "%s=%s\n", name, digests[name])
	}
	fmt.Fprintf(out, "plan_sha256=%s\n", planSha)
	return 0
}

func runUnitsDeclare(ctx context.Context, args []string, out, errOut io.Writer) int {
	opts, seat, remove, addr, err := parseUnitOpts(args)
	if err != nil {
		return refuse(errOut, "units declare", err.Error())
	}
	st, err := store.Open(ctx, addr)
	if err != nil {
		return refuse(errOut, "units declare", err.Error())
	}
	defer st.Close()

	msg, err := unit.Declare(ctx, st, opts, seat, remove)
	if err != nil {
		return refuse(errOut, "units declare", err.Error())
	}
	fmt.Fprintln(out, msg)
	if msg == "UNCHANGED" || strings.HasPrefix(msg, "DECLARED") || strings.HasPrefix(msg, "REMOVED") {
		return 0
	}
	return 2
}

func runUnitsApply(ctx context.Context, args []string, out, errOut io.Writer) int {
	opts, _, _, addr, err := parseUnitOpts(args)
	fs := flag.NewFlagSet("units apply", flag.ContinueOnError)
	dir := fs.String("dir", "", "")
	// parse remaining flags
	_ = fs.Parse(args)

	st, err := store.Open(ctx, addr)
	if err != nil {
		return refuse(errOut, "units apply", err.Error())
	}
	defer st.Close()

	code, err := unit.Apply(ctx, st, opts, *dir)
	if err != nil {
		return code
	}
	fmt.Fprintln(out, "APPLIED")
	return 0
}

func runUnitsCheck(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := flag.NewFlagSet("units check", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	redisAddr := fs.String("redis", "", "")
	storeAddr := fs.String("store", "", "")
	host := fs.String("host", "", "")
	all := fs.Bool("all", false, "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "units check", err.Error())
	}
	addr := *storeAddr
	if addr == "" {
		addr = *redisAddr
	}
	if addr == "" {
		addr = "127.0.0.1:6379"
	}

	st, err := store.Open(ctx, addr)
	if err != nil {
		return refuse(errOut, "units check", err.Error())
	}
	defer st.Close()

	defects, code, err := unit.Check(ctx, st, *host, *all)
	if err != nil {
		return 2
	}
	for _, d := range defects {
		fmt.Fprintln(out, d)
	}
	return code
}

func sortStrings(s []string) {
	for i := 0; i < len(s); i++ {
		for j := i + 1; j < len(s); j++ {
			if s[i] > s[j] {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
}
