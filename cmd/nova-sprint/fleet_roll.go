// fleet roll [--to <sha>] (#4332) is fleet-roll.sh as one verb: fleet release
// of the sha (dev's tip when --to is absent), then the fleet play through
// ansible with the machines registry as its inventory (never ssh by hand),
// then a verify of every bench's beat version field in the store (no ssh):
// one VERIFY <bench> want=<v> have=<v> ok|behind line per bench and FLEET
// ROLL OK|BEHIND|FAIL last (internal/nsprint/fleetbuild/roll.go).
//
//	fleet roll [--to <sha>] [--redis <addr>] [--machines <file>] [--benches <a,b,...>]
//	           [--play-dir <dir>] [--play tools.yml] [--wait 60s] [--admin-password-env NAME]
//
// Exit 0 every bench beat names the release; 1 a refusal (FLEET ROLL
// REFUSED: <why>), a bench behind (the list on the last line), a failed play
// or fn=refused; 2 usage; 5 the store unreachable.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fleetbuild"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

func runFleetRoll(ctx context.Context, args []string, out, errOut io.Writer) int {
	return runFleetRollWith(ctx, args, out, errOut, productionReleaseDeps())
}

func rollRefused(errOut io.Writer, err error) int {
	fmt.Fprintf(errOut, "FLEET ROLL REFUSED: %s\n", oneline.Escape(strings.TrimPrefix(err.Error(), fleetbuild.ErrRefused.Error()+": ")))
	return 1
}

func runFleetRollWith(ctx context.Context, args []string, out, errOut io.Writer, deps releaseDeps) int {
	fs := verbflag.New("fleet roll")
	to := fs.String("to", "", "")
	redisAddr := fs.String("redis", "", "")
	machines := fs.String("machines", "", "")
	benches := fs.String("benches", "", "")
	playDir := fs.String("play-dir", "", "")
	play := fs.String("play", fleetbuild.DefaultPlay, "")
	wait := fs.Duration("wait", fleetbuild.DefaultVerifyWait, "")
	adminEnv := fs.String("admin-password-env", fleetbuild.DefaultAdminEnv, "")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "fleet roll", err.Error())
	}
	if fs.NArg() != 0 {
		return refuse(errOut, "fleet roll", "takes no positional arguments; --to <sha> names the commit (dev's tip without it)")
	}
	if *wait < 0 {
		return refuse(errOut, "fleet roll", "--wait must be >= 0")
	}
	if strings.ContainsAny(*play, `/\`) || !strings.HasSuffix(*play, ".yml") {
		return refuse(errOut, "fleet roll", "--play names a play file in the play directory, like tools.yml")
	}
	home, err := deps.Home()
	if err != nil {
		return rollRefused(errOut, fmt.Errorf("no home directory: %v", err))
	}
	registry := *machines
	if registry == "" {
		registry = deps.Getenv(fleetbuild.MachinesEnv)
	}
	if registry == "" {
		return rollRefused(errOut, fmt.Errorf("the play's inventory reads the machines registry: --machines <file>, or %s", fleetbuild.MachinesEnv))
	}
	ms, err := fleetbuild.ReadMachinesFile(registry)
	if err != nil {
		return rollRefused(errOut, fmt.Errorf("machines registry: %v (--machines <file>, or %s)", err, fleetbuild.MachinesEnv))
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
		return unreachable(errOut, "fleet roll", err.Error())
	}
	defer st.Close()
	rel := &fleetbuild.Release{Client: st.Client(), Runner: deps.Runner, Home: home, UID: deps.UID, GOOS: deps.GOOS,
		Redis: fleetAddr(*redisAddr), AdminEnv: *adminEnv, AdminPassword: deps.Getenv(*adminEnv),
		BuildCmd: fleetBuildCmd(), Machines: ms, Benches: only, Out: out}
	roll := &fleetbuild.Roll{Release: rel, PlayDir: dir, Play: *play, Registry: registry,
		Wait: *wait, Poll: fleetbuild.DefaultVerifyPoll, Sleep: deps.Sleep}
	res, err := roll.Run(ctx, *to)
	if errors.Is(err, fleetbuild.ErrRefused) {
		return rollRefused(errOut, err)
	}
	if err != nil {
		return unreachable(errOut, "fleet roll", err.Error())
	}
	fmt.Fprintln(out, res.Line())
	if !res.OK() {
		return 1
	}
	return 0
}
