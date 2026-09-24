// The capacity verb registers itself through the S0 registry (registry.go), so
// adding it never edits main.go. capacity friend, capacity bench and
// capacity machine are each one call into internal/nsprint/capacity, which is
// one Redis Function call that guards the machine ceiling and receipts the
// write. A refusal prints CEILING <m> <sum>/<ceiling> and exits 2 (spec 2.4).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/capacity"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
)

func init() {
	register(Verb{
		Name:    "capacity",
		Summary: "set a friend, bench or machine slot budget under the machine ceiling",
		Run:     runCapacity,
	})
}

func runCapacity(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) == 0 {
		return refuse(errOut, "capacity", "want friend, bench or machine")
	}
	switch args[0] {
	case "friend":
		if hasWakeFlag(args[1:]) {
			return runCapacityWake(ctx, args[1:], out, errOut) // friend.go (#3101)
		}
		return runCapacityDesired(ctx, capacity.KindFriend, args[1:], out, errOut)
	case "bench":
		return runCapacityDesired(ctx, capacity.KindBench, args[1:], out, errOut)
	case "machine":
		return runCapacityMachine(ctx, args[1:], out, errOut)
	default:
		return refuse(errOut, "capacity", fmt.Sprintf("unknown subverb %s; want friend, bench or machine", args[0]))
	}
}

// capacityFlags is the flag set shared by the capacity subverbs, quiet on a
// parse error so the verb prints one refusal line the way main.go does.
func capacityFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	return fs
}

func runCapacityDesired(ctx context.Context, kind string, args []string, out, errOut io.Writer) int {
	fs := capacityFlags("capacity " + kind)
	redisAddr := fs.String("redis", "", "")
	machine := fs.String("machine", "", "")
	actor := new(string)
	fs.StringVar(actor, "as", "", "")
	fs.StringVar(actor, "actor", "", "")
	idem := fs.String("idem", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "capacity "+kind, err.Error())
	}
	if *actor == "" {
		return refuse(errOut, "capacity "+kind, "--as actor is required")
	}
	rest := fs.Args()
	if len(rest) != 2 {
		return refuse(errOut, "capacity "+kind, fmt.Sprintf("want %s <name> <slots>; flags precede names", kind))
	}
	name := rest[0]
	slots, err := strconv.Atoi(rest[1])
	if err != nil || slots < 0 {
		return refuse(errOut, "capacity "+kind, "slots must be a nonnegative integer")
	}
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "capacity "+kind, err.Error())
	}
	defer st.Close()

	resolved := *machine
	if resolved == "" {
		resolved = existingMachine(ctx, st, kind, name)
	}
	if resolved == "" {
		return refuse(errOut, "capacity "+kind, "machine is required; pass --machine")
	}

	var result capacity.Result
	if kind == capacity.KindBench {
		result, err = capacity.SetBench(ctx, st, name, resolved, slots, *actor, *idem)
	} else {
		result, err = capacity.SetFriend(ctx, st, name, resolved, slots, *actor, *idem)
	}
	if err != nil {
		return refuseCapacity(errOut, "capacity "+kind, err)
	}
	fmt.Fprintf(out, "SET %s %s machine=%s slots=%d desired=%d/%d\n",
		kind, name, resolved, result.Slots, result.Sum, result.Ceiling)
	return 0
}

func runCapacityMachine(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := capacityFlags("capacity machine")
	redisAddr := fs.String("redis", "", "")
	cores := fs.Int("cores", 0, "")
	memGB := fs.Int("mem-gb", 0, "")
	actor := new(string)
	fs.StringVar(actor, "as", "", "")
	fs.StringVar(actor, "actor", "", "")
	idem := fs.String("idem", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "capacity machine", err.Error())
	}
	if *actor == "" {
		return refuse(errOut, "capacity machine", "--as actor is required")
	}
	rest := fs.Args()
	if len(rest) != 2 {
		return refuse(errOut, "capacity machine", "want machine <slots>")
	}
	machine := rest[0]
	slots, err := strconv.Atoi(rest[1])
	if err != nil || slots < 0 {
		return refuse(errOut, "capacity machine", "slots must be a nonnegative integer")
	}
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "capacity machine", err.Error())
	}
	defer st.Close()
	result, err := capacity.SetMachine(ctx, st, machine, slots, *cores, *memGB, *actor, *idem)
	if err != nil {
		return refuseCapacity(errOut, "capacity machine", err)
	}
	fmt.Fprintf(out, "SET machine %s slots=%d desired=%d/%d\n",
		machine, result.Slots, result.Sum, result.Ceiling)
	return 0
}

// existingMachine reads the machine already written in a consumer's desired
// hash so a re-raise need not repeat --machine (spec 2.2: capacity writes it).
func existingMachine(ctx context.Context, st *store.Store, kind, name string) string {
	value, err := st.Client().HGet(ctx, capacity.DesiredKey(kind, name), "machine").Result()
	if err != nil {
		return ""
	}
	return value
}

// refuseCapacity turns a ceiling refusal into the exact exit 2 line and any
// other error into the ordinary refusal.
func refuseCapacity(errOut io.Writer, verb string, err error) int {
	var ceilingErr *capacity.CeilingError
	if errors.As(err, &ceilingErr) {
		fmt.Fprintln(errOut, ceilingErr.Error())
		return ceilingErr.ExitCode()
	}
	return refuse(errOut, verb, err.Error())
}
