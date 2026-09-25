// fleet build (#3310) is the deploy: the builder builds the release named in
// fleet:release, every bench in the benches set and this machine install it,
// each bench that answers the release version gets its bench:<b> build
// receipt, and the new nova-sprint here runs fn deploy. It replaces the
// coordinator's deploy chain and rowan-tools fleet-install-tools.sh; the plan
// lives in Redis (internal/nsprint/fleetbuild).
//
//	fleet build [--redis <addr>] [--bench <b>[,<b>...]] [--build-cmd <path>] [--dry-run]
//	fleet build set [--redis <addr>] <key>=<value>...
//	fleet build compile --version <v> --commit <sha40> [--platform <p>[,<p>...]] [--repo-url <url>] [--dry-run]
//
// compile (#4080) is the builder half, run on the builder by fleet build over
// ssh: it builds and publishes the release with its own GOMODCACHE, GOCACHE and
// toolchain under ~/nova-bench/space-build/go/, so it never shares a Go cache
// with a CI runner on the same machine (internal/nsprint/fleetbuild/compile.go).
//
// Exit 0 every target installed and fn deploy answered; 1 a refusal (the
// line names the remedy) or a failed target; 2 usage; 5 Redis unreachable.
package main

import (
	"context"
	"errors"
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

func runFleetBuild(ctx context.Context, args []string, out, errOut io.Writer) int {
	if len(args) > 0 && args[0] == "set" {
		return runFleetBuildSet(ctx, args[1:], out, errOut)
	}
	if len(args) > 0 && args[0] == "compile" {
		return runFleetBuildCompile(ctx, args[1:], out, errOut)
	}
	fs := verbflag.New("fleet build")
	redisAddr := fs.String("redis", "", "")
	benches := fs.String("bench", "", "")
	buildCmd := fs.String("build-cmd", "", "")
	dry := fs.Bool("dry-run", false, "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "fleet build", err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, "fleet build", "takes no positional arguments (fleet build set <key>=<value> writes the plan)")
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

func runFleetBuildSet(ctx context.Context, args []string, out, errOut io.Writer) int {
	fs := verbflag.New("fleet build set")
	redisAddr := fs.String("redis", "", "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "fleet build set", err.Error())
	}
	if fs.NArg() == 0 {
		return refuse(errOut, "fleet build set", "wants <key>=<value>...: version, commit, builder, self, tools, platform:<bench>")
	}
	st, err := openFleetStore(ctx, *redisAddr)
	if err != nil {
		return unreachable(errOut, "fleet build set", err.Error())
	}
	defer st.Close()
	n, err := fleetbuild.SetFields(ctx, st.Client(), fs.Args())
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
	version := fs.String("version", "", "")
	commit := fs.String("commit", "", "")
	platforms := fs.String("platform", fleetbuild.DefaultPlatforms, "")
	repoURL := fs.String("repo-url", fleetbuild.DefaultRepoURL, "")
	dry := fs.Bool("dry-run", false, "")
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
