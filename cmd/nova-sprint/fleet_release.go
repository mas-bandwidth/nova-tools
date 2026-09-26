// fleet release <sha> (#4306) is the deploy of a landed dev tip as one verb:
// the six hand steps of 2026-09-26 in order, one receipt line each, refusing
// on the first failure (internal/nsprint/fleetbuild/release.go).
//
//	fleet release <sha> [--redis <addr>] [--machines <file>] [--benches <a,b,...>]
//	                    [--studio-only | --benches-only] [--admin-password-env NAME]
//
// The admin password for fn deploy is read from the variable
// --admin-password-env names (default NS_ADMIN), never from an ssh; empty,
// the FN step alone is refused with the exact remedy and the rest runs.
//
// `fleet release --bench <b>` with no sha is the older verb: it releases a
// held bench (fleet.Release).
//
// Exit 0 every step answered; 1 a refusal (FLEET RELEASE REFUSED: <why>
// (<remedy>)) or fn=refused; 2 usage; 5 the store unreachable.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fleet"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fleetbuild"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/testguard"
)

// releaseDeps is everything fleet release reaches outside the process, so a
// test hands in fakes per call and runs in parallel (no package-level seam).
type releaseDeps struct {
	Runner fleetbuild.ExecRunner
	Home   func() (string, error)
	Getenv func(string) string
	UID    int
	GOOS   string
	Open   func(ctx context.Context, addr string) (*store.Store, error)
}

// releaseExec runs one child in dir with the sanitized environment plus env.
type releaseExec struct{}

func (releaseExec) Run(ctx context.Context, dir string, env []string, argv []string) (string, error) {
	testguard.RefuseHosts(argv[0], argv[1:]...)
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Dir = dir
	cmd.Env = append(goenv.Clean(os.Environ()), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func productionReleaseDeps() releaseDeps {
	return releaseDeps{Runner: releaseExec{}, Home: fleetBuildHome, Getenv: os.Getenv,
		UID: os.Getuid(), GOOS: runtime.GOOS, Open: openFleetStore}
}

func runFleetRelease(ctx context.Context, args []string, out, errOut io.Writer) int {
	return runFleetReleaseWith(ctx, args, out, errOut, productionReleaseDeps())
}

func releaseRefused(errOut io.Writer, err error) int {
	fmt.Fprintf(errOut, "FLEET RELEASE REFUSED: %s\n", oneline.Escape(strings.TrimPrefix(err.Error(), fleetbuild.ErrRefused.Error()+": ")))
	return 1
}

func runFleetReleaseWith(ctx context.Context, args []string, out, errOut io.Writer, deps releaseDeps) int {
	fs := verbflag.New("fleet release")
	redisAddr := fs.String("redis", "", "")
	bench := fs.String("bench", "", "")
	machines := fs.String("machines", "", "")
	benches := fs.String("benches", "", "")
	studioOnly := fs.Bool("studio-only", false, "")
	benchesOnly := fs.Bool("benches-only", false, "")
	adminEnv := fs.String("admin-password-env", fleetbuild.DefaultAdminEnv, "")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return refuse(errOut, "fleet release", err.Error())
	}
	if len(pos) == 0 {
		return runFleetReleaseHeld(ctx, fs, *redisAddr, *bench, out, errOut, deps)
	}
	if len(pos) != 1 {
		return refuse(errOut, "fleet release", "wants one <sha> (a landed dev commit), or --bench <b> to release a held bench")
	}
	if *bench != "" {
		return refuse(errOut, "fleet release <sha>", "does not take --bench; --benches <a,b,...> names the benches to roll")
	}
	if *studioOnly && *benchesOnly {
		return refuse(errOut, "fleet release <sha>", "--studio-only and --benches-only leave nothing to do")
	}
	home, err := deps.Home()
	if err != nil {
		return releaseRefused(errOut, fmt.Errorf("no home directory: %v", err))
	}
	path := *machines
	if path == "" {
		path = deps.Getenv(fleetbuild.MachinesEnv)
	}
	ms, err := fleetbuild.ReadMachinesFile(path)
	if err != nil {
		return releaseRefused(errOut, fmt.Errorf("machines registry: %v (--machines <file>, or unset %s)", err, fleetbuild.MachinesEnv))
	}
	var only []string
	for _, b := range strings.Split(*benches, ",") {
		if b = strings.TrimSpace(b); b != "" {
			only = append(only, b)
		}
	}
	rel := &fleetbuild.Release{Runner: deps.Runner, Home: home, UID: deps.UID, GOOS: deps.GOOS,
		Redis: fleetAddr(*redisAddr), AdminEnv: *adminEnv, AdminPassword: deps.Getenv(*adminEnv),
		BuildCmd: fleetBuildCmd(), Machines: ms, Benches: only, StudioOnly: *studioOnly, BenchesOnly: *benchesOnly, Out: out}
	if !*studioOnly {
		st, err := deps.Open(ctx, *redisAddr)
		if err != nil {
			return unreachable(errOut, "fleet release", err.Error())
		}
		defer st.Close()
		rel.Client = st.Client()
	}
	res, err := rel.Run(ctx, pos[0])
	if errors.Is(err, fleetbuild.ErrRefused) {
		return releaseRefused(errOut, err)
	}
	if err != nil {
		return unreachable(errOut, "fleet release", err.Error())
	}
	word := "OK"
	code := 0
	if !res.OK() {
		word, code = "FAIL", 1
	}
	fmt.Fprintf(out, "FLEET RELEASE %s version=%s commit=%s studio=%s fn=%s rolled=%d skipped=%d\n",
		word, res.Version, res.Commit[:12], strings.ToLower(res.Studio), res.Fn, len(res.Rolled), len(res.Skipped))
	return code
}

// runFleetReleaseHeld is `fleet release --bench <b>`: the held bench is
// released; every other flag belongs to the sha form.
func runFleetReleaseHeld(ctx context.Context, fs *flag.FlagSet, redisAddr, bench string, out, errOut io.Writer, deps releaseDeps) int {
	var other []string
	fs.Visit(func(f *flag.Flag) {
		if f.Name != "redis" && f.Name != "bench" {
			other = append(other, "--"+f.Name)
		}
	})
	if len(other) > 0 {
		return refuse(errOut, "fleet release", "wants a <sha> with "+strings.Join(other, ", ")+"; --bench <b> alone releases a held bench")
	}
	if bench == "" {
		return refuse(errOut, "fleet release", "wants a <sha> (the deploy) or --bench <b> (release a held bench)")
	}
	st, err := deps.Open(ctx, redisAddr)
	if err != nil {
		return unreachable(errOut, "fleet release", err.Error())
	}
	defer st.Close()

	if err := fleet.Release(ctx, st.Client(), bench); err != nil {
		if errors.Is(err, fleet.ErrNotHeld) {
			return refuse(errOut, "fleet release", "bench "+bench+" is not held")
		}
		if errors.Is(err, fleet.ErrUnregistered) {
			return refuse(errOut, "fleet release", "unregistered bench "+bench)
		}
		return fleetRefuse(errOut, "fleet release", err)
	}
	return 0
}
