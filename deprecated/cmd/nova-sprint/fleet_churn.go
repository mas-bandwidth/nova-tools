// fleet churn (#4310) is the process-age sample as a verb: one ps over ssh
// on every registered machine, or from registered benches via bench beat
// telemetry (PSSample), and per machine/bench the commands younger than the
// window with counts, every process older than a day whose parent is init,
// and one CHURN <machine> young=<n> old=<n> line (internal/nsprint/fleet/churn.go).
//
//	fleet churn [--seconds 12] [--bench <name>] [--redis <addr>] [--machines <file>] [--only <a,b,...>]
//
// In telemetry mode, it reads registered benches from Redis (SMEMBERS benches),
// analyzes process start times and PPIDs from each beat's PSSample, and ends
// with FLEET CHURN OK benches=<n> churn=<k> orphans=<m>.
//
// Exit 0 every machine answered / clean; 1 a machine failed; 2 usage or no registry; 5 store unreachable.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fleet"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fleetbuild"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

func runFleetChurn(ctx context.Context, args []string, out, errOut io.Writer) (code int) {
	defer verbflag.Recover(out, "nova-sprint fleet", "", &code)
	return runFleetChurnWith(ctx, args, out, errOut, fleetBuildRunner, os.Getenv)
}

func runFleetChurnWith(ctx context.Context, args []string, out, errOut io.Writer, runner fleetbuild.Runner, getenv func(string) string) int {
	return runFleetChurnWithSampler(ctx, args, out, errOut, runner, nil, getenv)
}

func isFlagPassed(args []string, name string) bool {
	for _, a := range args {
		if a == name || strings.HasPrefix(a, name+"=") {
			return true
		}
	}
	return false
}

func runFleetChurnWithSampler(ctx context.Context, args []string, out, errOut io.Writer, runner fleetbuild.Runner, sampler fleet.ChurnSampler, getenv func(string) string) (code int) {
	defer verbflag.Recover(out, "nova-sprint fleet", "", &code)
	fs := verbflag.New("fleet churn")
	seconds := fs.Int("seconds", fleet.DefaultWindow, "")
	bench := fs.String("bench", "", "")
	redisAddr := fs.String("redis", redisDefault(), "")
	machines := fs.String("machines", "", "")
	onlyList := fs.String("only", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "fleet churn", err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, "fleet churn", "takes no positional arguments")
	}
	if *seconds < 1 {
		return refuse(errOut, "fleet churn", "--seconds must be >= 1")
	}

	// Machines mode: when --machines is given, or when NOVA_FLEET_MACHINES is set
	// and neither --redis, --bench, nor a sampler was requested.
	useRedis := isFlagPassed(args, "--redis") || isFlagPassed(args, "--bench") || sampler != nil
	if !useRedis {
		machinesPath := *machines
		if machinesPath == "" {
			machinesPath = getenv(fleetbuild.MachinesEnv)
		}
		if machinesPath == "" {
			return refuse(errOut, "fleet churn", "wants the machines registry: --machines <file>, or "+fleetbuild.MachinesEnv)
		}
		return runFleetChurnMachines(ctx, machinesPath, *onlyList, *seconds, out, errOut, runner)
	}

	// Redis / PSSample telemetry mode:
	st, err := openFleetStore(ctx, *redisAddr)
	if err != nil {
		return unreachable(errOut, "fleet churn", err.Error())
	}
	defer st.Close()

	targetBench := *bench
	if targetBench == "" && *onlyList != "" {
		targetBench = *onlyList
	}
	benches, err := readPSBenches(ctx, st.Client(), targetBench)
	if err != nil {
		if errors.Is(err, fleet.ErrUnregistered) {
			return refuse(errOut, "fleet churn", "unregistered bench "+targetBench)
		}
		return fleetRefuse(errOut, "fleet churn", err)
	}
	if len(benches) == 0 {
		return refuse(errOut, "fleet churn", "no bench is registered (SMEMBERS benches is empty)")
	}

	var totalChurn, totalOrphans, okBenches int
	code = 0
	for _, b := range benches {
		var sample fleet.PSSample
		var failMsg string
		if sampler != nil {
			s, err := sampler(ctx, b.Bench)
			if err != nil {
				failMsg = fmt.Sprintf("CHURN %s FAIL %s", b.Bench, oneline.Escape(err.Error()))
			} else {
				sample = s
			}
		} else {
			sample, failMsg = b.Sample("CHURN")
		}
		if failMsg != "" {
			fmt.Fprintln(out, failMsg)
			code = 1
			continue
		}
		churn := fleet.SampleFromPS(sample, *seconds)
		for _, line := range churn.Lines(b.Bench) {
			fmt.Fprintln(out, line)
		}
		okBenches++
		totalChurn += churn.YoungTotal
		totalOrphans += len(churn.Old)
	}
	if code == 0 {
		fmt.Fprintf(out, "FLEET CHURN OK benches=%d churn=%d orphans=%d\n", okBenches, totalChurn, totalOrphans)
	}
	return code
}

func runFleetChurnMachines(ctx context.Context, path, onlyList string, seconds int, out, errOut io.Writer, runner fleetbuild.Runner) int {
	ms, err := fleetbuild.ReadMachinesFile(path)
	if err != nil {
		return refuse(errOut, "fleet churn", "machines registry: "+err.Error())
	}
	only := map[string]bool{}
	for _, n := range strings.Split(onlyList, ",") {
		if n = strings.TrimSpace(n); n != "" {
			only[n] = true
		}
	}
	var picked []fleetbuild.Machine
	found := map[string]bool{}
	for _, m := range ms {
		if len(only) == 0 || only[m.Name] {
			picked = append(picked, m)
			found[m.Name] = true
		}
	}
	var missing []string
	for n := range only {
		if !found[n] {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return refuse(errOut, "fleet churn", "--only names machines the registry does not: "+strings.Join(missing, ","))
	}
	if len(picked) == 0 {
		return refuse(errOut, "fleet churn", "the registry names no machine")
	}
	lines := make([][]string, len(picked))
	var wg sync.WaitGroup
	for i, m := range picked {
		wg.Add(1)
		go func(i int, m fleetbuild.Machine) {
			defer wg.Done()
			lines[i] = churnOne(ctx, m, seconds, runner)
		}(i, m)
	}
	wg.Wait()
	code := 0
	for _, ls := range lines {
		for _, l := range ls {
			fmt.Fprintln(out, l)
			if strings.HasPrefix(l, "CHURN ") && strings.Contains(l, " FAIL ") {
				code = 1
			}
		}
	}
	return code
}

// churnArgv is the sample for m: ps over ssh, or ps here when m is localhost.
func churnArgv(m fleetbuild.Machine, goos string) ([]string, error) {
	ps, err := fleet.PSCommand(goos)
	if err != nil {
		return nil, err
	}
	if m.IsLocal() {
		return strings.Fields(ps), nil
	}
	return []string{"ssh", "-n", "-o", "BatchMode=yes", "-o", "ConnectTimeout=6", m.Host(), ps}, nil
}

func churnOne(ctx context.Context, m fleetbuild.Machine, window int, runner fleetbuild.Runner) []string {
	goos, _, _ := strings.Cut(m.Platform, "-")
	if goos == "" {
		return []string{"CHURN " + m.Name + " FAIL no ps sample for " + oneline.Field(m.OSArch) + " (linux and darwin)"}
	}
	argv, err := churnArgv(m, goos)
	if err != nil {
		return []string{"CHURN " + m.Name + " FAIL " + oneline.Escape(err.Error())}
	}
	outText, err := runner.Run(ctx, argv)
	if err != nil {
		last := strings.TrimSpace(outText)
		if i := strings.LastIndex(last, "\n"); i >= 0 {
			last = last[i+1:]
		}
		return []string{"CHURN " + m.Name + " FAIL " + oneline.Escape(err.Error()+": "+last)}
	}
	procs, err := fleet.ParsePS(goos, outText)
	if err != nil {
		return []string{"CHURN " + m.Name + " FAIL " + oneline.Escape(err.Error())}
	}
	return fleet.Sample(procs, window).Lines(m.Name)
}
