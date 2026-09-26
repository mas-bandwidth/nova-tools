// fleet build (#3310) is the deploy: the builder builds the release named in
// fleet:release, every bench in the benches set and this machine install it,
// each bench that answers the release version gets its bench:<b> build
// receipt, every target's probe result goes on its beat (bench:<b>:beat
// probe, never a consumer's cards: nova-tools#4237), and the new nova-sprint
// here runs fn deploy. It replaces the
// coordinator's deploy chain and rowan-tools fleet-install-tools.sh; the plan
// lives in Redis (internal/nsprint/fleetbuild).
//
//	fleet build [--redis <addr>] [--bench <b>[,<b>...]] [--build-cmd <path>] [--machines <file>] [--dry-run]
//	fleet build set [--redis <addr>] <key>=<value>...   (the flags before the pairs)
//	fleet build compile --version <v> --commit <sha40> [--platform <p>[,<p>...]] [--repo-url <url>] [--dry-run]
//	fleet build duty [--redis <addr>] [--machines <file>] [--dry-run]
//
// compile (#4080) is the builder half, run on the builder by fleet build over
// ssh: it builds and publishes the release with its own GOMODCACHE, GOCACHE and
// toolchain under ~/nova-bench/space-build/go/, so it never shares a Go cache
// with a CI runner on the same machine (internal/nsprint/fleetbuild/compile.go).
//
// Nothing in the plan is typed (#4050): land merge writes version and commit,
// builder, self and platform:<bench> converge from the bench registry and the
// machines registry (--machines, else $NOVA_FLEET_MACHINES) before each plan,
// and the tools are the release manifest's. --redis goes anywhere on the
// line, set's key=value pairs included. `duty` is one pass of the
// reconciler's deploy duty: WOULD INSTALL per bench whose beat names another
// version under --dry-run, else it starts `fleet build --bench <those>`.
//
// Exit 0 every target installed and fn deploy answered; 1 a refusal (the
// line names the remedy) or a failed target; 2 usage; 5 Redis unreachable.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fleetbuild"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/testguard"
)

// fleetBuildRunner is the seam tests replace; production runs the argv.
var fleetBuildRunner fleetbuild.Runner = execRunner{}

// fleetBuildHome is this machine's home; tests point it at a temp dir.
var fleetBuildHome = os.UserHomeDir

type execRunner struct{}

func (execRunner) Run(ctx context.Context, argv []string) (string, error) {
	testguard.RefuseHosts(argv[0], argv[1:]...)
	out, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
	return string(out), err
}

// fleetCompileExec is the compile seam tests replace: a local git or go child
// in dir, its environment goenv.Clean plus the build's own Go variables.
var fleetCompileExec fleetbuild.Exec = func(ctx context.Context, dir string, extra []string, argv []string) (string, error) {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = append(goenv.Clean(os.Environ()), extra...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func buildRefused(errOut io.Writer, err error) int {
	fmt.Fprintf(errOut, "FLEET BUILD REFUSED: %s\n", oneline.Escape(strings.TrimPrefix(err.Error(), fleetbuild.ErrRefused.Error()+": ")))
	return 1
}

// fleetBuildCmd is the build command: $NOVA_FLEET_BUILD_CMD, else the
// default found on PATH.
func fleetBuildCmd() string {
	if v := os.Getenv("NOVA_FLEET_BUILD_CMD"); v != "" {
		return v
	}
	return ""
}

// fleetMachines reads the machines registry at path, else at
// $NOVA_FLEET_MACHINES; neither set reads none.
func fleetMachines(path string) ([]fleetbuild.Machine, error) {
	if path == "" {
		path = os.Getenv(fleetbuild.MachinesEnv)
	}
	return fleetbuild.ReadMachinesFile(path)
}

func runFleetBuild(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) > 0 && args[0] == "compile" {
		return runFleetBuildCompile(ctx, args[1:], out, errOut)
	}
	fs := verbflag.New("fleet build")
	redisAddr := fs.String("redis", redisDefault(), verbflag.HelpRedis)
	benches := fs.String("bench", "", "the benches to build for, comma-separated (default every bench)")
	buildCmd := fs.String("build-cmd", fleetBuildCmd(), "the build command the builder runs")
	machines := fs.String("machines", "", "the machines registry file (default NOVA_FLEET_MACHINES)")
	dry := fs.Bool("dry-run", false, verbflag.HelpDryRun)
	// --redis anywhere on the line (#4050): the flag set parses up to each
	// positional (the mode word, set's key=value pairs) and goes on after it.
	var pos []string
	for rest := args; ; {
		if err := fs.Parse(rest); err != nil {
			return refuse(errOut, "fleet build", err.Error())
		}
		rest = fs.Args()
		if len(rest) == 0 {
			break
		}
		pos = append(pos, rest[0])
		rest = rest[1:]
	}
	if len(pos) > 0 && (pos[0] == "set" || pos[0] == "duty") {
		var other []string
		fs.Visit(func(f *flag.Flag) {
			if f.Name != "redis" && !(pos[0] == "duty" && (f.Name == "machines" || f.Name == "dry-run")) {
				other = append(other, "--"+f.Name)
			}
		})
		if len(other) > 0 {
			return refuse(errOut, "fleet build "+pos[0], "does not take "+strings.Join(other, ", "))
		}
		if pos[0] == "set" {
			return runFleetBuildSet(ctx, *redisAddr, pos[1:], out, errOut)
		}
		if len(pos) > 1 {
			return refuse(errOut, "fleet build duty", "takes no positional arguments")
		}
		return runFleetBuildDuty(ctx, *redisAddr, *machines, *dry, out, errOut)
	}
	if len(pos) != 0 {
		return refuse(errOut, "fleet build", "takes no positional arguments (fleet build set <key>=<value> writes the plan)")
	}
	ms, err := fleetMachines(*machines)
	if err != nil {
		return buildRefused(errOut, fmt.Errorf("machines registry: %v (--machines <file>, or unset %s)", err, fleetbuild.MachinesEnv))
	}
	var only []string
	for _, b := range strings.Split(*benches, ",") {
		if b = strings.TrimSpace(b); b != "" {
			only = append(only, b)
		}
	}
	st, err := openFleetStore(ctx, *redisAddr)
	if err != nil {
		return unreachable(errOut, "fleet build", err.Error())
	}
	defer st.Close()
	cfg, err := fleetbuild.ReadConfig(ctx, st.Client())
	if err != nil {
		return unreachable(errOut, "fleet build", err.Error())
	}
	facts, err := fleetbuild.ReadFacts(ctx, st.Client())
	if err != nil {
		return unreachable(errOut, "fleet build", err.Error())
	}
	facts.Machines = ms
	conv := fleetbuild.Converge(facts)
	cfg = fleetbuild.Apply(cfg, conv)
	if len(conv) > 0 {
		if *dry {
			fmt.Fprintf(out, "WOULD CONVERGE %s\n", fleetbuild.Fields(conv))
		} else if err := fleetbuild.WriteConverged(ctx, st.Client(), conv); err != nil {
			return unreachable(errOut, "fleet build", err.Error())
		} else {
			fmt.Fprintf(out, "CONVERGED %s\n", fleetbuild.Fields(conv))
		}
	}
	plan, err := fleetbuild.MakePlan(cfg, only)
	if err != nil {
		return buildRefused(errOut, err)
	}
	home, err := fleetBuildHome()
	if err != nil {
		return buildRefused(errOut, fmt.Errorf("no home directory: %v", err))
	}
	d := &fleetbuild.Deployer{Client: st.Client(), Runner: fleetBuildRunner, Home: home,
		BuildCmd: *buildCmd, Redis: fleetAddr(*redisAddr), Out: out}
	if *dry {
		fmt.Fprintf(out, "WOULD BUILD %s\n", strings.Join(d.BuildArgv(plan), " "))
		for _, t := range plan.Targets {
			fmt.Fprintf(out, "WOULD INSTALL %s platform=%s from=%s\n", t.Bench, t.Platform, fleetbuild.Source(plan, t))
		}
		fmt.Fprintf(out, "FLEET BUILD DRY-RUN version=%s commit=%s targets=%d\n", plan.Version, plan.Commit[:12], len(plan.Targets))
		return 0
	}
	res, err := d.Deploy(ctx, plan)
	if err != nil {
		return unreachable(errOut, "fleet build", err.Error())
	}
	ok := 0
	var failed []string
	for _, l := range res.Lines {
		if l.Status == "OK" {
			ok++
		} else {
			failed = append(failed, l.Bench)
		}
	}
	if res.OK() {
		fmt.Fprintf(out, "FLEET BUILD OK version=%s commit=%s benches=%d fn=ok\n", plan.Version, plan.Commit[:12], ok)
		return 0
	}
	why := "install"
	switch {
	case !res.Built:
		why = "build"
	case len(failed) == 0:
		why = "fn"
	}
	list := "-"
	if len(failed) > 0 {
		list = strings.Join(failed, ",")
	}
	fmt.Fprintf(out, "FLEET BUILD FAIL version=%s commit=%s at=%s ok=%d failed=%s\n",
		plan.Version, plan.Commit[:12], why, ok, list)
	return 1
}

func runFleetBuildSet(ctx context.Context, redisAddr string, pairs []string, out, errOut io.Writer) int {
	if len(pairs) == 0 {
		return refuse(errOut, "fleet build set", "wants <key>=<value>...: version, commit, builder, self, tools, platform:<bench>")
	}
	st, err := openFleetStore(ctx, redisAddr)
	if err != nil {
		return unreachable(errOut, "fleet build set", err.Error())
	}
	defer st.Close()
	n, err := fleetbuild.SetFields(ctx, st.Client(), pairs)
	if errors.Is(err, fleetbuild.ErrRefused) {
		return buildRefused(errOut, err)
	}
	if err != nil {
		return unreachable(errOut, "fleet build set", err.Error())
	}
	fmt.Fprintf(out, "FLEET RELEASE SET fields=%d\n", n)
	return 0
}

// runFleetBuildCompile is the builder half: exit 0 built (or nothing to
// build), 1 refused with the remedy named, 2 usage.
func runFleetBuildCompile(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := verbflag.New("fleet build compile")
	version := fs.String("version", "", "the release version")
	commit := fs.String("commit", "", "the commit built, 40 hex")
	platforms := fs.String("platform", fleetbuild.DefaultPlatforms, "the platforms built, comma-separated")
	repoURL := fs.String("repo-url", fleetbuild.DefaultRepoURL, "the repository url the builder clones")
	dry := fs.Bool("dry-run", false, verbflag.HelpDryRun)
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "fleet build compile", err.Error())
	}
	if fs.NArg() != 0 || *version == "" || *commit == "" {
		return refuse(errOut, "fleet build compile", "wants --version <v> --commit <sha40> [--platform <p>[,<p>...]] [--repo-url <url>] [--dry-run]")
	}
	home, err := fleetBuildHome()
	if err != nil {
		home = ""
	}
	var plats []string
	for _, p := range strings.Split(*platforms, ",") {
		if p = strings.TrimSpace(p); p != "" {
			plats = append(plats, p)
		}
	}
	c := &fleetbuild.Compile{Home: home, Version: *version, Commit: *commit, Platforms: plats,
		RepoURL: *repoURL, DryRun: *dry, Exec: fleetCompileExec, Out: out}
	if err := c.Run(ctx); err != nil {
		fmt.Fprintf(errOut, "FLEET COMPILE REFUSED: %s\n", oneline.Escape(strings.TrimPrefix(err.Error(), fleetbuild.ErrRefused.Error()+": ")))
		return 1
	}
	return 0
}
