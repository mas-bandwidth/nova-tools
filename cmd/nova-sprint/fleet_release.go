// fleet release <sha>|dev (#4306, #4356 A) is the whole roll of a landed dev
// commit as one verb (internal/nsprint/fleetbuild/release.go), in order, one
// receipt per step and per bench: build (the source, the release's own
// nova-sprint here, fleet:release and the builder's compile), fn (fn deploy
// as the Redis admin user), fn-check, play (the bench play through ansible,
// then the beats behind restarted through ansible), self (self update of this
// machine and its loops kickstarted), and the verify from the beats. A step's
// refusal is one line with its remedy and the later steps run where they
// can; the verify always runs.
//
//	fleet release <sha>|dev [--redis <addr>] [--machines <file>] [--benches <a,b,...>]
//	                        [--play-dir <dir>] [--play tools.yml] [--wait 60s]
//	                        [--admin-password-env NAME]
//
// The admin password is the variable --admin-password-env names (default
// NS_ADMIN), else the seat's sealed NOVA_REDIS_ADMIN_PASSWORD (--seat <name>
// or NOVA_SEAT, read in this process through nova-secrets' library), never an
// ssh. `fleet roll` is retired into this verb, and so are --studio-only
// (self update) and --benches-only.
//
// `fleet release --bench <b>` with no sha is the older verb: it releases a
// held bench (fleet.Release).
//
// Exit 0 every step answered and every bench beat names the release; 1 a
// step refused, a bench behind (FLEET RELEASE FAIL|BEHIND ... behind=<list>),
// or a refusal before any step (FLEET RELEASE REFUSED: <why>); 2 usage; 5 the
// store unreachable.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/goenv"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fleet"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fleetbuild"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/seatcred"
	"github.com/mas-bandwidth/nova-tools/internal/testguard"
)

// releaseDeps is everything fleet release reaches outside the process, so a
// test hands in fakes per call and runs in parallel (no package-level seam).
type releaseDeps struct {
	Runner fleetbuild.ExecRunner
	Home   func() (string, error)
	Getenv func(string) string
	UID    int
	PID    int
	GOOS   string
	Open   func(ctx context.Context, addr string) (*store.Store, error)
	// Seat is this run's seat ("" none); AdminSecret reads the Redis admin
	// password sealed in a seat's file.
	Seat        func() string
	AdminSecret func(seat string) (string, error)
	// Sleep is the pause between verify reads; nil sleeps on a timer, a
	// test hands in a fake.
	Sleep func(ctx context.Context, d time.Duration) error
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

// seatAdminSecret opens seat's file through seatcred (the store, key and sops
// nova-secrets exec uses) and returns its admin password: the key
// NOVA_REDIS_ADMIN_PASSWORD, which `make -C fleet store-seal ROLE=admin`
// seals. The value stays in this process and the fn children's environment.
func seatAdminSecret(seat string) (string, error) {
	getenv := func(k string) string {
		if k == seatcred.UserEnv {
			return fleetbuild.AdminUser
		}
		return os.Getenv(k)
	}
	c, err := seatcred.Resolve(seat, getenv)
	if err != nil {
		return "", err
	}
	var pw string
	err = c.Password.Use(func(s string) error { pw = s; return nil })
	return pw, err
}

func productionReleaseDeps() releaseDeps {
	return releaseDeps{Runner: releaseExec{}, Home: fleetBuildHome, Getenv: os.Getenv,
		UID: os.Getuid(), PID: os.Getpid(), GOOS: runtime.GOOS, Open: openFleetStore,
		Seat: seatcred.Selected, AdminSecret: seatAdminSecret}
}

func runFleetRelease(ctx context.Context, args []string, out, errOut io.Writer) int {
	return runFleetReleaseWith(ctx, args, out, errOut, productionReleaseDeps())
}

// runFleetRoll is the retired spelling: one refusal naming the survivor.
func runFleetRoll(_ context.Context, _ []string, _, errOut io.Writer) int {
	return refuse(errOut, "fleet roll", "is retired into fleet release: nova-sprint fleet release <sha>|dev is the whole roll (build, fn, fn-check, the play, self update, the verify)")
}

func releaseRefused(errOut io.Writer, err error) int {
	fmt.Fprintf(errOut, "FLEET RELEASE REFUSED: %s\n", oneline.Escape(strings.TrimPrefix(err.Error(), fleetbuild.ErrRefused.Error()+": ")))
	return 1
}

// adminPassword is the admin password and, when there is none, why with the
// remedy: the variable env names, else the seat's sealed password.
func adminPassword(env string, deps releaseDeps) (pw, seat, why string) {
	if deps.Seat != nil {
		seat = deps.Seat()
	}
	if pw = deps.Getenv(env); pw != "" {
		return pw, seat, ""
	}
	if seat == "" || deps.AdminSecret == nil {
		return "", seat, env + " is empty and no seat is named (" + fleetbuild.AdminRemedy(env, "") + ")"
	}
	pw, err := deps.AdminSecret(seat)
	if err != nil || pw == "" {
		detail := "empty"
		if err != nil {
			detail = err.Error()
		}
		return "", seat, env + " is empty and seat " + seat + " gave none: " + detail + " (" + fleetbuild.AdminRemedy(env, seat) + ")"
	}
	return pw, seat, ""
}

func runFleetReleaseWith(ctx context.Context, args []string, out, errOut io.Writer, deps releaseDeps) int {
	fs := verbflag.New("fleet release")
	redisAddr := fs.String("redis", "", "")
	bench := fs.String("bench", "", "")
	machines := fs.String("machines", "", "")
	benches := fs.String("benches", "", "")
	playDir := fs.String("play-dir", "", "")
	play := fs.String("play", fleetbuild.DefaultPlay, "")
	wait := fs.Duration("wait", fleetbuild.DefaultVerifyWait, "")
	adminEnv := fs.String("admin-password-env", fleetbuild.DefaultAdminEnv, "")
	studioOnly := fs.Bool("studio-only", false, "")
	benchesOnly := fs.Bool("benches-only", false, "")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return refuse(errOut, "fleet release", err.Error())
	}
	if *studioOnly {
		return refuse(errOut, "fleet release", "--studio-only is retired: nova-sprint self update does this machine alone; fleet release <sha> is the whole roll")
	}
	if *benchesOnly {
		return refuse(errOut, "fleet release", "--benches-only is retired: fleet release <sha> is the whole roll, and a rerun keeps what is done (the play skips a bench on the release)")
	}
	if len(pos) == 0 {
		return runFleetReleaseHeld(ctx, fs, *redisAddr, *bench, out, errOut, deps)
	}
	if len(pos) != 1 {
		return refuse(errOut, "fleet release", "wants one <sha> (a landed dev commit) or dev (dev's tip), or --bench <b> to release a held bench")
	}
	if *bench != "" {
		return refuse(errOut, "fleet release <sha>", "does not take --bench; --benches <a,b,...> names the benches to roll")
	}
	if *wait < 0 {
		return refuse(errOut, "fleet release <sha>", "--wait must be >= 0")
	}
	if strings.ContainsAny(*play, `/\`) || !strings.HasSuffix(*play, ".yml") {
		return refuse(errOut, "fleet release <sha>", "--play names a play file in the play directory, like tools.yml")
	}
	if err := fleetbuild.CheckArg(pos[0]); err != nil {
		return releaseRefused(errOut, err)
	}
	home, err := deps.Home()
	if err != nil {
		return releaseRefused(errOut, fmt.Errorf("no home directory: %v", err))
	}
	registry := *machines
	if registry == "" {
		registry = deps.Getenv(fleetbuild.MachinesEnv)
	}
	if registry == "" {
		return releaseRefused(errOut, fmt.Errorf("the play's inventory and the bench list read the machines registry: --machines <file>, or %s", fleetbuild.MachinesEnv))
	}
	ms, err := fleetbuild.ReadMachinesFile(registry)
	if err != nil {
		return releaseRefused(errOut, fmt.Errorf("machines registry: %v (--machines <file>, or %s)", err, fleetbuild.MachinesEnv))
	}
	dir := *playDir
	if dir == "" {
		dir = deps.Getenv(fleetbuild.PlayDirEnv)
	}
	if dir == "" {
		dir = filepath.Join(home, filepath.FromSlash(fleetbuild.DefaultPlayDirRel))
	}
	var only []string
	for _, b := range strings.Split(*benches, ",") {
		if b = strings.TrimSpace(b); b != "" {
			only = append(only, b)
		}
	}
	st, err := deps.Open(ctx, *redisAddr)
	if err != nil {
		return unreachable(errOut, "fleet release", err.Error())
	}
	defer st.Close()
	pw, seat, why := adminPassword(*adminEnv, deps)
	rel := &fleetbuild.Release{Client: st.Client(), Runner: deps.Runner, Home: home, UID: deps.UID, GOOS: deps.GOOS, PID: deps.PID,
		Redis: fleetAddr(*redisAddr), AdminEnv: *adminEnv, AdminPassword: pw, AdminWhy: why, Seat: seat,
		BuildCmd: fleetBuildCmd(), Machines: ms, Benches: only, PlayDir: dir, Play: *play, Registry: registry,
		Wait: *wait, Poll: fleetbuild.DefaultVerifyPoll, Sleep: deps.Sleep, Out: out}
	res, err := rel.Run(ctx, pos[0])
	if errors.Is(err, fleetbuild.ErrRefused) {
		return releaseRefused(errOut, err)
	}
	if err != nil {
		return unreachable(errOut, "fleet release", err.Error())
	}
	fmt.Fprintln(out, res.Line())
	if !res.OK() {
		return 1
	}
	return 0
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
