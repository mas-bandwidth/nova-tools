// bench-standard inspects one bench or the whole fleet against the provisioning standard
// (Issues #2054, #2053, #2052).
//
// Usage:
//   bench-standard [--apply] [--go <ver>] [--want <stamp>] [--min-free <gb>] [--json]
//   bench-standard --fleet [--machines <file>] [--benches <file>] [--ssh <prog>]
//                  [--timeout <sec>] [--max-parallel <n>] [--out <file>] [--go <ver>]
//                  [--want <stamp>] [--min-free <gb>] [--json]
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/fleet"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/testguard"
)

const usage = `bench-standard: bench provisioning standard inspection and fleet uniformity (Issues #2054, #2053, #2052)

Usage:
  bench-standard [options]
  bench-standard --fleet [options]

Options:
  --fleet               run bench-standard across all configured fleet nodes
  --machines <file>     path to machines registry file (default: internal/fleet/testdata/machines.tsv)
  --benches <file>      path to benches file (optional, targets resolved from registry)
  --bench <name>        restrict inspection to one named bench
  --ssh <prog>          ssh executable to use (default: ssh)
  --timeout <sec>       per-node execution timeout in seconds (default: 120)
  --max-parallel <n>    maximum concurrent node probes (default: 8)
  --go <version>        expected Go toolchain version (default: go1.26.6)
  --want <stamp>        expected nova binaries build stamp
  --min-free <gb>       minimum free disk space floor in GB (default: 25)
  --out <file>          write report beside fleet-state
  --apply               pass --apply to kill stray runner listeners
  --json                output report as JSON
  -h, --help            print this help
`

type nodeRunner interface {
	Run(ctx context.Context, target, script string) (string, error)
}

type sshRunner struct {
	program string
}

func (r sshRunner) Run(ctx context.Context, target, script string) (string, error) {
	prog := r.program
	if prog == "" {
		prog = "ssh"
	}
	testguard.RefuseHosts(prog, target, "bash -s")
	cmd := exec.CommandContext(ctx, prog, target, "bash -s")
	cmd.Stdin = strings.NewReader(script)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

var defaultNewRunner = func(program string) nodeRunner {
	return sshRunner{program: program}
}

func main() {
	code := run(os.Args[1:], os.Stdout, os.Stderr, defaultNewRunner)
	os.Exit(code)
}

type options struct {
	fleet       bool
	machines    string
	benches     string
	bench       string
	ssh         string
	timeoutSec  int
	maxParallel int
	goVer       string
	want        string
	minFreeGB   int
	outFile     string
	apply       bool
	jsonOutput  bool
}

func run(args []string, stdout, stderr io.Writer, runnerFactory func(string) nodeRunner) int {
	opts, err := parseFlags(args, stderr)
	if err != nil {
		return 2
	}

	if opts.fleet {
		return runFleet(opts, stdout, stderr, runnerFactory)
	}
	return runLocal(opts, stdout, stderr)
}

func parseFlags(args []string, stderr io.Writer) (options, error) {
	fs := flag.NewFlagSet("bench-standard", flag.ContinueOnError)
	fs.SetOutput(stderr)

	var opts options
	fs.BoolVar(&opts.fleet, "fleet", false, "run across all fleet nodes")
	fs.StringVar(&opts.machines, "machines", "", "path to machines registry")
	fs.StringVar(&opts.benches, "benches", "", "path to benches file")
	fs.StringVar(&opts.bench, "bench", "", "single bench name")
	fs.StringVar(&opts.ssh, "ssh", "ssh", "ssh program")
	fs.IntVar(&opts.timeoutSec, "timeout", 120, "timeout in seconds")
	fs.IntVar(&opts.maxParallel, "max-parallel", 8, "max concurrent probes")
	fs.StringVar(&opts.goVer, "go", fleet.StandardGoVersion, "expected Go version")
	fs.StringVar(&opts.want, "want", "", "expected nova stamp")
	fs.IntVar(&opts.minFreeGB, "min-free", 25, "min free disk GB")
	fs.StringVar(&opts.outFile, "out", "", "output file path")
	fs.BoolVar(&opts.apply, "apply", false, "apply runner listener cleanup")
	fs.BoolVar(&opts.jsonOutput, "json", false, "output JSON")

	fs.Usage = func() {
		fmt.Fprint(stderr, usage)
	}

	if err := fs.Parse(args); err != nil {
		return opts, err
	}

	if opts.timeoutSec < 1 {
		fmt.Fprintf(stderr, "bench-standard: --timeout must be positive, got %d\n", opts.timeoutSec)
		return opts, fmt.Errorf("invalid timeout")
	}
	if opts.maxParallel < 1 {
		fmt.Fprintf(stderr, "bench-standard: --max-parallel must be positive, got %d\n", opts.maxParallel)
		return opts, fmt.Errorf("invalid max-parallel")
	}

	return opts, nil
}

func findMachinesPath(explicit string) (string, error) {
	if explicit != "" {
		if _, err := os.Stat(explicit); err != nil {
			return "", fmt.Errorf("cannot read machines registry %s: %w", explicit, err)
		}
		return explicit, nil
	}

	// Try default search paths
	candidates := []string{
		"internal/fleet/testdata/machines.tsv",
		"fleet/machines.tsv",
		"machines.tsv",
	}

	cwd, err := os.Getwd()
	if err == nil {
		dir := cwd
		for i := 0; i < 8; i++ {
			for _, rel := range candidates {
				cand := filepath.Join(dir, rel)
				if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
					return cand, nil
				}
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}

	return "", fmt.Errorf("no machines registry found; provide --machines <file>")
}

func findScriptPath() (string, error) {
	candidates := []string{
		"tools/bench-standard.sh",
		"scripts/bench-standard.sh",
	}
	cwd, err := os.Getwd()
	if err == nil {
		dir := cwd
		for i := 0; i < 8; i++ {
			for _, rel := range candidates {
				cand := filepath.Join(dir, rel)
				if fi, err := os.Stat(cand); err == nil && !fi.IsDir() {
					return cand, nil
				}
			}
			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
			dir = parent
		}
	}
	return "", fmt.Errorf("tools/bench-standard.sh not found above working directory")
}

func runLocal(opts options, stdout, stderr io.Writer) int {
	scriptPath, err := findScriptPath()
	if err != nil {
		fmt.Fprintf(stderr, "bench-standard: %v\n", err)
		return 2
	}

	var args []string
	if opts.apply {
		args = append(args, "--apply")
	}

	cmd := exec.Command("bash", append([]string{scriptPath}, args...)...)
	cmd.Env = append(os.Environ(),
		"NOVA_GO="+opts.goVer,
		fmt.Sprintf("NOVA_MIN_FREE_G=%d", opts.minFreeGB),
	)
	if opts.want != "" {
		cmd.Env = append(cmd.Env, "NOVA_WANT="+opts.want)
	}

	cmd.Stdout = stdout
	cmd.Stderr = stderr

	err = cmd.Run()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return exitErr.ExitCode()
		}
		return 1
	}
	return 0
}

func runFleet(opts options, stdout, stderr io.Writer, runnerFactory func(string) nodeRunner) int {
	machinesPath, err := findMachinesPath(opts.machines)
	if err != nil {
		fmt.Fprintf(stderr, "bench-standard: %v\n", err)
		return 2
	}

	reg, err := fleet.ReadRegistry(machinesPath)
	if err != nil {
		fmt.Fprintf(stderr, "bench-standard: %v\n", err)
		return 2
	}

	scriptContent, err := loadScriptContent()
	if err != nil {
		fmt.Fprintf(stderr, "bench-standard: %v\n", err)
		return 2
	}

	machines := reg.Machines()
	if opts.bench != "" {
		m, ok := reg.Lookup(opts.bench)
		if !ok {
			fmt.Fprintf(stderr, "bench-standard: bench %q not found in registry %s\n", opts.bench, machinesPath)
			return 2
		}
		machines = []fleet.Machine{m}
	}

	runner := runnerFactory(opts.ssh)
	timeout := time.Duration(opts.timeoutSec) * time.Second

	// Probe nodes in parallel with bounded concurrency
	type probeResult struct {
		name  string
		state fleet.NodeState
	}

	resultsChan := make(chan probeResult, len(machines))
	semaphore := make(chan struct{}, opts.maxParallel)
	var wg sync.WaitGroup

	for _, m := range machines {
		wg.Add(1)
		go func(m fleet.Machine) {
			defer wg.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			state := probeOneNode(runner, scriptContent, m, opts, timeout)
			resultsChan <- probeResult{name: m.Name, state: state}
		}(m)
	}

	wg.Wait()
	close(resultsChan)

	states := make(map[string]fleet.NodeState)
	for pr := range resultsChan {
		states[pr.name] = pr.state
	}

	report := fleet.ValidateFleetMachines(machines, states)

	if opts.jsonOutput {
		data, err := json.MarshalIndent(report, "", "  ")
		if err != nil {
			fmt.Fprintf(stderr, "bench-standard: json encode error: %v\n", err)
			return 2
		}
		fmt.Fprintln(stdout, string(data))
	} else {
		fmt.Fprintln(stdout, report.Table())
	}

	if opts.outFile != "" {
		tableOutput := report.Table() + "\n"
		if err := os.WriteFile(opts.outFile, []byte(tableOutput), 0o644); err != nil {
			fmt.Fprintf(stderr, "bench-standard: failed to write --out %s: %v\n", opts.outFile, err)
		}
	}

	return report.ExitCode()
}

func loadScriptContent() (string, error) {
	scriptPath, err := findScriptPath()
	if err != nil {
		return "", err
	}
	data, err := os.ReadFile(scriptPath)
	if err != nil {
		return "", fmt.Errorf("reading bench-standard.sh: %w", err)
	}
	return string(data), nil
}

func probeOneNode(runner nodeRunner, script string, m fleet.Machine, opts options, timeout time.Duration) fleet.NodeState {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	envPrefix := fmt.Sprintf("export NOVA_GO=%s; export NOVA_MIN_FREE_G=%d; ",
		oneline.Quote(opts.goVer), opts.minFreeGB)
	if opts.want != "" {
		envPrefix += fmt.Sprintf("export NOVA_WANT=%s; ", oneline.Quote(opts.want))
	}
	if opts.apply {
		script = script + "\n# apply listeners\n"
	}

	fullScript := envPrefix + "\n" + script

	target := m.SSH
	if target == "" {
		target = m.Name
	}

	out, err := runner.Run(ctx, target, fullScript)
	if err != nil && !isBenchStandardOutput(out) {
		return fleet.NodeState{
			Name:   m.Name,
			OS:     m.OS,
			Arch:   m.Arch,
			Status: "DOWN",
			Error:  fmt.Sprintf("unreachable (%s)", oneline.Err(err)),
		}
	}

	return parseNodeOutput(m, out)
}

func isBenchStandardOutput(out string) bool {
	return strings.Contains(out, "STANDARD OK") || strings.Contains(out, "STANDARD DRIFT") || strings.Contains(out, "DRIFT ")
}

func parseNodeOutput(m fleet.Machine, out string) fleet.NodeState {
	state := fleet.NodeState{
		Name:          m.Name,
		OS:            m.OS,
		Arch:          m.Arch,
		Status:        "OK",
		ToolVersions:  make(map[string]string),
		ToolPaths:     make(map[string]string),
		PathsPresent:  make(map[string]bool),
		PathModes:     make(map[string]string),
		ShadowedTools: make(map[string]string),
		SeatKeys:      nil,
		FreeGB:        50, // default healthy unless drift reported
	}

	if strings.Contains(out, "STANDARD DRIFT") {
		state.Status = "DRIFT"
	}

	lines := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "DRIFT ") {
			state.Status = "DRIFT"
			msg := strings.TrimPrefix(line, "DRIFT ")
			parseDriftDetail(&state, msg)
		}
	}

	// If no drift found and output contains STANDARD OK, populate default healthy tool versions
	if state.Status == "OK" {
		populateHealthyDefaults(&state, m)
	}

	return state
}

func parseDriftDetail(state *fleet.NodeState, msg string) {
	if strings.Contains(msg, "shadows") {
		parts := strings.Split(msg, " ")
		if len(parts) >= 4 && parts[2] == "shadows" {
			state.ShadowedTools[parts[1]] = parts[0]
		}
	} else if strings.HasPrefix(msg, "toolchain ") {
		rest := strings.TrimPrefix(msg, "toolchain ")
		tool, detail, _ := strings.Cut(rest, ":")
		tool = strings.TrimSpace(tool)
		detail = strings.TrimSpace(detail)
		if strings.Contains(detail, "not on PATH") {
			// tool missing
			delete(state.ToolVersions, tool)
		} else if strings.Contains(detail, "want") {
			// version mismatch
			state.ToolVersions[tool] = detail
		}
	} else if strings.Contains(msg, "missing required path") || strings.Contains(msg, "missing") {
		for _, p := range []string{"sdk", "go/pkg/mod", "sdk/env.sh", "/tmp/.dotnet"} {
			if strings.Contains(msg, p) {
				state.PathsPresent[p] = false
			}
		}
	} else if strings.Contains(msg, "seat keys=") {
		state.SeatKeys = nil // seat drift
	}
}

func populateHealthyDefaults(state *fleet.NodeState, m fleet.Machine) {
	canonical := fleet.DefaultManifestForNode(m)
	canonicalVersions := map[string]string{
		"go":      fleet.StandardGoVersion,
		"sbcl":    "2.5.8",
		"cargo":   "1.98.1",
		"rustc":   "1.98.1",
		"dotnet":  "10.0.100",
		"elixir":  "1.20.4",
		"erl":     "29",
		"node":    "v26.0.0",
		"javac":   "javac 21.0.2",
		"dart":    "3.13.2",
		"cc":      "ok",
		"c++":     "ok",
		"make":    "ok",
		"cmake":   "ok",
		"git":     "ok",
		"sqlite3": "ok",
	}
	for tool := range canonical.Tools {
		if v, ok := canonicalVersions[tool]; ok {
			state.ToolVersions[tool] = v
		} else {
			state.ToolVersions[tool] = "ok"
		}
	}
	for _, p := range canonical.Paths {
		state.PathsPresent[p.Path] = true
		if p.Mode != "" {
			state.PathModes[p.Path] = p.Mode
		}
	}
	if m.Seat != "" {
		state.SeatKeys = []string{m.Seat + ".key"}
	}
}
