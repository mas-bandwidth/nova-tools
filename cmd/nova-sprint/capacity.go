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
	"os"
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
		return refuse(errOut, "capacity", "want friend, bench, machine, budget, take, give, renew, reap or hook")
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
	case "budget":
		return runCapacityBudget(ctx, args[1:], out, errOut)
	case "take":
		return runCapacityTake(ctx, args[1:], out, errOut)
	case "give":
		return runCapacityGive(ctx, args[1:], out, errOut)
	case "renew":
		return runCapacityRenew(ctx, args[1:], out, errOut)
	case "reap":
		return runCapacityReap(ctx, args[1:], out, errOut)
	case "hook":
		return runCapacityHook(ctx, args[1:], out, errOut)
	default:
		return refuse(errOut, "capacity", fmt.Sprintf("unknown subverb %s", args[0]))
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
	// #3206 rev 4 PR A: --paused 0|1 sets the paused flag (omitted keeps it)
	// and --register is accepted and implied (#2934: every capacity friend
	// write adds the friend to `friends`).
	paused := fs.String("paused", "", "")
	register := fs.Bool("register", false, "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "capacity "+kind, err.Error())
	}
	if *actor == "" {
		return refuse(errOut, "capacity "+kind, "--as actor is required")
	}
	if *paused != "" && *paused != "0" && *paused != "1" {
		return refuse(errOut, "capacity "+kind, "--paused wants 0 or 1")
	}
	if kind == capacity.KindBench && (*paused != "" || *register) {
		return refuse(errOut, "capacity "+kind, "--paused and --register are friend flags")
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
		result, err = capacity.SetFriendWith(ctx, st, name, resolved, slots, *actor, *idem,
			capacity.DesiredOpts{Paused: *paused, Register: *register})
	}
	if err != nil {
		return refuseCapacity(errOut, "capacity "+kind, err)
	}
	status := result.Status
	if status == "" {
		status = "SET"
	}
	_, _ = fmt.Fprintf(out, "%s %s %s machine=%s slots=%d desired=%d/%d\n",
		status, kind, name, resolved, result.Slots, result.Sum, result.Ceiling)
	return 0
}

func runCapacityMachine(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := capacityFlags("capacity machine")
	redisAddr := fs.String("redis", "", "")
	cores := fs.Int("cores", 0, "")
	memGB := fs.Int("mem-gb", 0, "")
	cpuMilli := fs.Int("cpu-milli", 0, "")
	memMB := fs.Int("mem-mb", 0, "")
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
	result, err := capacity.SetMachineBudget(ctx, st, machine, slots, *cores, *memGB, *cpuMilli, *memMB, *actor, *idem)
	if err != nil {
		return refuseCapacity(errOut, "capacity machine", err)
	}
	fmt.Fprintf(out, "SET machine %s slots=%d desired=%d/%d\n",
		machine, result.Slots, result.Sum, result.Ceiling)
	return 0
}

func runCapacityBudget(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := capacityFlags("capacity budget")
	redisAddr := fs.String("redis", "", "")
	actor := new(string)
	fs.StringVar(actor, "as", "", "")
	fs.StringVar(actor, "actor", "", "")
	idem := fs.String("idem", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "capacity budget", err.Error())
	}
	if *actor == "" {
		return refuse(errOut, "capacity budget", "--as actor is required")
	}
	rest := fs.Args()
	if len(rest) != 3 {
		return refuse(errOut, "capacity budget", "want machine <cpu_milli> <mem_mb>")
	}
	machine := rest[0]
	cpuMilli, err := strconv.Atoi(rest[1])
	if err != nil || cpuMilli < 0 {
		return refuse(errOut, "capacity budget", "cpu_milli must be a nonnegative integer")
	}
	memMB, err := strconv.Atoi(rest[2])
	if err != nil || memMB < 0 {
		return refuse(errOut, "capacity budget", "mem_mb must be a nonnegative integer")
	}
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "capacity budget", err.Error())
	}
	defer st.Close()
	if err := capacity.SetBudget(ctx, st, machine, cpuMilli, memMB, *actor, *idem); err != nil {
		return refuse(errOut, "capacity budget", err.Error())
	}
	fmt.Fprintf(out, "SET budget %s cpu=%d mem=%d\n", machine, cpuMilli, memMB)
	return 0
}

func runCapacityTake(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := capacityFlags("capacity take")
	redisAddr := fs.String("redis", "", "")
	machine := fs.String("machine", "", "")
	consumer := fs.String("consumer", "", "")
	cpuMilli := fs.Int("cpu-milli", 0, "")
	memMB := fs.Int("mem-mb", 0, "")
	ttlMs := fs.Int("ttl-ms", 30000, "")
	pgid := fs.Int("pgid", 0, "")
	kind := fs.String("kind", "", "")
	actor := new(string)
	fs.StringVar(actor, "as", "", "")
	fs.StringVar(actor, "actor", "", "")
	idem := fs.String("idem", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "capacity take", err.Error())
	}
	if *machine == "" {
		return refuse(errOut, "capacity take", "--machine is required")
	}
	if *consumer == "" {
		return refuse(errOut, "capacity take", "--consumer is required")
	}
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "capacity take", err.Error())
	}
	defer st.Close()
	res, err := capacity.Take(ctx, st, capacity.TakeRequest{
		Machine:  *machine,
		Consumer: *consumer,
		CPUMilli: *cpuMilli,
		MemMB:    *memMB,
		TTLMs:    *ttlMs,
		PGID:     *pgid,
		Kind:     *kind,
		Actor:    *actor,
		Idem:     *idem,
	})
	if err != nil {
		var bErr *capacity.BudgetError
		if errors.As(err, &bErr) {
			fmt.Fprintln(errOut, bErr.Error())
			return bErr.ExitCode()
		}
		return refuse(errOut, "capacity take", err.Error())
	}
	fmt.Fprintf(out, "TAKE %s machine=%s cpu=%d/%d mem=%d/%d\n",
		*consumer, *machine, res.UsedCPU, res.TotalCPU, res.UsedMem, res.TotalMem)
	return 0
}

func runCapacityGive(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := capacityFlags("capacity give")
	redisAddr := fs.String("redis", "", "")
	machine := fs.String("machine", "", "")
	consumer := fs.String("consumer", "", "")
	pgid := fs.Int("pgid", 0, "")
	confirmed := fs.Bool("confirmed", false, "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "capacity give", err.Error())
	}
	if *consumer == "" && len(fs.Args()) > 0 {
		*consumer = fs.Args()[0]
	}
	if *consumer == "" {
		return refuse(errOut, "capacity give", "--consumer is required")
	}
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "capacity give", err.Error())
	}
	defer st.Close()
	err = capacity.Give(ctx, st, capacity.GiveRequest{
		Machine:   *machine,
		Consumer:  *consumer,
		PGID:      *pgid,
		Confirmed: *confirmed,
	})
	if err != nil {
		var saErr *capacity.StillAliveError
		if errors.As(err, &saErr) {
			fmt.Fprintln(errOut, saErr.Error())
			return saErr.ExitCode()
		}
		return refuse(errOut, "capacity give", err.Error())
	}
	fmt.Fprintf(out, "GIVE %s\n", *consumer)
	return 0
}

func runCapacityRenew(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := capacityFlags("capacity renew")
	redisAddr := fs.String("redis", "", "")
	machine := fs.String("machine", "", "")
	consumer := fs.String("consumer", "", "")
	pgid := fs.Int("pgid", 0, "")
	ttlMs := fs.Int("ttl-ms", 30000, "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "capacity renew", err.Error())
	}
	if *consumer == "" && len(fs.Args()) > 0 {
		*consumer = fs.Args()[0]
	}
	if *consumer == "" {
		return refuse(errOut, "capacity renew", "--consumer is required")
	}
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "capacity renew", err.Error())
	}
	defer st.Close()
	if err := capacity.Renew(ctx, st, *machine, *consumer, *pgid, *ttlMs); err != nil {
		return refuse(errOut, "capacity renew", err.Error())
	}
	fmt.Fprintf(out, "RENEW %s\n", *consumer)
	return 0
}

func runCapacityReap(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := capacityFlags("capacity reap")
	redisAddr := fs.String("redis", "", "")
	machine := fs.String("machine", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "capacity reap", err.Error())
	}
	if *machine == "" && len(fs.Args()) > 0 {
		*machine = fs.Args()[0]
	}
	if *machine == "" {
		return refuse(errOut, "capacity reap", "machine is required; pass --machine")
	}
	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "capacity reap", err.Error())
	}
	defer st.Close()
	reaped, err := capacity.Reap(ctx, st, *machine)
	if err != nil {
		return refuse(errOut, "capacity reap", err.Error())
	}
	for _, d := range reaped {
		fmt.Fprintf(out, "REAPED %s machine=%s cpu=%d mem=%d\n", d.Consumer, d.Machine, d.CPUMilli, d.MemMB)
	}
	return 0
}

func runCapacityHook(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := capacityFlags("capacity hook")
	redisAddr := fs.String("redis", "", "")
	machine := fs.String("machine", "", "")
	consumer := fs.String("consumer", "", "")
	cpuMilli := fs.Int("cpu-milli", 0, "")
	memMB := fs.Int("mem-mb", 0, "")
	pgid := fs.Int("pgid", 0, "")
	actor := new(string)
	fs.StringVar(actor, "as", "ci-runner", "")
	fs.StringVar(actor, "actor", "ci-runner", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "capacity hook", err.Error())
	}
	action := "job-started"
	rest := fs.Args()
	if len(rest) > 0 {
		action = rest[0]
	}

	resolvedMachine := *machine
	if resolvedMachine == "" {
		resolvedMachine = os.Getenv("NOVA_MACHINE")
	}
	if resolvedMachine == "" {
		h, _ := os.Hostname()
		resolvedMachine = h
	}
	if resolvedMachine == "" {
		resolvedMachine = "local"
	}

	resolvedConsumer := *consumer
	if resolvedConsumer == "" {
		resolvedConsumer = os.Getenv("NOVA_CONSUMER")
	}
	if resolvedConsumer == "" {
		if runID := os.Getenv("GITHUB_RUN_ID"); runID != "" {
			job := os.Getenv("GITHUB_JOB")
			if job == "" {
				job = "job"
			}
			resolvedConsumer = fmt.Sprintf("ci:%s:%s", runID, job)
		} else if runner := os.Getenv("RUNNER_NAME"); runner != "" {
			resolvedConsumer = fmt.Sprintf("ci:%s:%d", runner, os.Getpid())
		} else {
			resolvedConsumer = fmt.Sprintf("ci:job:%d", os.Getpid())
		}
	}

	st, err := store.Open(ctx, *redisAddr)
	if err != nil {
		return refuse(errOut, "capacity hook", err.Error())
	}
	defer st.Close()

	switch action {
	case "job-started", "started", "start":
		reqCPU := *cpuMilli
		if reqCPU <= 0 {
			if envCPU := os.Getenv("NOVA_CI_CPU_MILLI"); envCPU != "" {
				reqCPU, _ = strconv.Atoi(envCPU)
			}
		}
		if reqCPU <= 0 {
			reqCPU = 2000
		}
		reqMem := *memMB
		if reqMem <= 0 {
			if envMem := os.Getenv("NOVA_CI_MEM_MB"); envMem != "" {
				reqMem, _ = strconv.Atoi(envMem)
			}
		}
		if reqMem <= 0 {
			reqMem = 4096
		}
		resolvedPgid := *pgid
		if resolvedPgid == 0 {
			resolvedPgid = currentPgrp()
		}
		if resolvedPgid == 0 {
			resolvedPgid = os.Getpid()
		}

		res, err := capacity.Take(ctx, st, capacity.TakeRequest{
			Machine:  resolvedMachine,
			Consumer: resolvedConsumer,
			CPUMilli: reqCPU,
			MemMB:    reqMem,
			TTLMs:    30000,
			PGID:     resolvedPgid,
			Kind:     capacity.KindCI,
			Actor:    *actor,
		})
		if err != nil {
			var bErr *capacity.BudgetError
			if errors.As(err, &bErr) {
				fmt.Fprintln(errOut, bErr.Error())
				return bErr.ExitCode()
			}
			return refuse(errOut, "capacity hook", err.Error())
		}
		fmt.Fprintf(out, "HOOK started %s machine=%s cpu=%d/%d mem=%d/%d\n",
			resolvedConsumer, resolvedMachine, res.UsedCPU, res.TotalCPU, res.UsedMem, res.TotalMem)
		return 0

	case "job-completed", "completed", "complete":
		err := capacity.Give(ctx, st, capacity.GiveRequest{
			Machine:   resolvedMachine,
			Consumer:  resolvedConsumer,
			Confirmed: true,
		})
		if err != nil {
			return refuse(errOut, "capacity hook", err.Error())
		}
		fmt.Fprintf(out, "HOOK completed %s machine=%s\n", resolvedConsumer, resolvedMachine)
		return 0

	default:
		return refuse(errOut, "capacity hook", fmt.Sprintf("unknown action %s; want job-started or job-completed", action))
	}
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
