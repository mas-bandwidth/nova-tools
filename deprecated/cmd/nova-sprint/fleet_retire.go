// fleet retire <bench> <unit> (#4356 item H) stops and removes a stray unit
// on a bench through ansible, never by hand, recording the retirement receipt
// in Redis under bench:<b>:retire.
//
//	fleet retire <bench> <unit> [--dry-run] [--redis <addr>]
//	             [--machines <file>] [--play-dir <dir>]
//
// Exit 0 clean; 1 refusal or retirement failed; 2 usage; 5 store unreachable.
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fleet"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/verbflag"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

func runFleetRetire(ctx context.Context, args []string, out, errOut io.Writer) int {
	return runFleetRetireWith(ctx, args, out, errOut, productionReleaseDeps())
}

func retireRefused(errOut io.Writer, err error) int {
	fmt.Fprintf(errOut, "FLEET RETIRE REFUSED: %s\n", oneline.Escape(strings.TrimPrefix(err.Error(), fleet.ErrRetireRefused.Error()+": ")))
	return 1
}

func runFleetRetireWith(ctx context.Context, args []string, out, errOut io.Writer, deps releaseDeps) int {
	fs := verbflag.New("fleet retire")
	dryRun := fs.Bool("dry-run", false, "")
	redisAddr := fs.String("redis", redisDefault(), "")
	machines := fs.String("machines", "", "")
	playDir := fs.String("play-dir", "", "")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return refuse(errOut, "fleet retire", err.Error())
	}
	if len(pos) != 2 {
		return refuse(errOut, "fleet retire", "wants <bench> <unit> (e.g. nova-sprint fleet retire alpha stray.service)")
	}
	bench := pos[0]
	unit := pos[1]

	registry := *machines
	if registry == "" {
		registry = deps.Getenv(fleet.MachinesEnv)
	}
	dir := *playDir
	if dir == "" {
		dir = deps.Getenv(fleet.PlayDirEnv)
	}
	if dir == "" {
		if home, err := deps.Home(); err == nil {
			def := filepath.Join(home, filepath.FromSlash(fleet.DefaultPlayDirRel))
			if _, err := os.Stat(filepath.Join(def, fleet.PlayInventory)); err == nil {
				dir = def
			}
		}
	}

	r := &fleet.Retire{
		Runner:   deps.Runner,
		Bench:    bench,
		Unit:     unit,
		PlayDir:  dir,
		Registry: registry,
		DryRun:   *dryRun,
		Out:      out,
	}

	if !*dryRun {
		st, err := deps.Open(ctx, *redisAddr)
		if err != nil {
			return unreachable(errOut, "fleet retire", err.Error())
		}
		defer st.Close()
		r.Client = st.Client()
	}

	res, err := r.Run(ctx)
	if errors.Is(err, fleet.ErrRetireRefused) {
		return retireRefused(errOut, err)
	}
	if err != nil {
		return unreachable(errOut, "fleet retire", err.Error())
	}
	if !res.OK() {
		return 1
	}
	return 0
}
