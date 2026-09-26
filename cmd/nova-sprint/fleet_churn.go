// fleet churn (#4310) is the process-age sample as a verb: one ps over ssh
// on every registered machine, and per machine the commands younger than the
// window with counts, every process older than a day whose parent is init,
// and one CHURN <machine> young=<n> old=<n> line (internal/nsprint/fleet/churn.go).
//
//	fleet churn [--seconds 12] [--machines <file>] [--only <a,b,...>]
//
// The machines are the registry's (--machines <file>, else
// $NOVA_FLEET_MACHINES): the ssh column is the host, localhost is sampled
// without ssh, and the os/arch column picks the ps line. A machine whose
// sample fails prints CHURN <machine> FAIL <why> and the verb goes on.
//
// Exit 0 every machine answered; 1 a machine failed; 2 usage or no registry.
package main

import (
	"context"
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

func runFleetChurn(ctx context.Context, args []string, out, errOut io.Writer) int {
	return runFleetChurnWith(ctx, args, out, errOut, fleetBuildRunner, os.Getenv)
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

func runFleetChurnWith(ctx context.Context, args []string, out, errOut io.Writer, runner fleetbuild.Runner, getenv func(string) string) int {
	fs := verbflag.New("fleet churn")
	seconds := fs.Int("seconds", fleet.DefaultWindow, "")
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
	path := *machines
	if path == "" {
		path = getenv(fleetbuild.MachinesEnv)
	}
	if path == "" {
		return refuse(errOut, "fleet churn", "wants the machines registry: --machines <file>, or "+fleetbuild.MachinesEnv)
	}
	ms, err := fleetbuild.ReadMachinesFile(path)
	if err != nil {
		return refuse(errOut, "fleet churn", "machines registry: "+err.Error())
	}
	only := map[string]bool{}
	for _, n := range strings.Split(*onlyList, ",") {
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
			lines[i] = churnOne(ctx, m, *seconds, runner)
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
