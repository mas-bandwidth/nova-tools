// fleet play <tag> [--limit a,b] [--dry-run] (#4356 item C) runs one
// rowan-tools fleet play through the verb, never by hand
// (fleet-changes-only-through-ansible): <tag>.yml in the play directory with
// the machines registry as its inventory, refused on a dirty or behind
// rowan-tools clone (the line names the git command), then one
// FLEET PLAY <bench> <role> ok|changed|failed ms=<n> receipt per bench and
// role read from ansible's own output, each bench's receipt written to
// bench:<b>:play for fleet doctor and the table, and FLEET PLAY OK|FAIL last
// (internal/nsprint/fleetbuild/play.go).
//
//	fleet play <tag> [--limit <a,b,...>] [--dry-run] [--redis <addr>]
//	           [--machines <file>] [--play-dir <dir>]
//
// Exit 0 every bench ran every role; 1 a refusal (FLEET PLAY REFUSED: <why>)
// or a bench that stopped (the failed list on the last line); 2 usage; 5
// the store unreachable.
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

func runFleetPlay(ctx context.Context, args []string, out, errOut io.Writer) int {
	return runFleetPlayWith(ctx, args, out, errOut, productionReleaseDeps())
}

func playRefused(errOut io.Writer, err error) int {
	fmt.Fprintf(errOut, "FLEET PLAY REFUSED: %s\n", oneline.Escape(strings.TrimPrefix(err.Error(), fleetbuild.ErrRefused.Error()+": ")))
	return 1
}

func runFleetPlayWith(ctx context.Context, args []string, out, errOut io.Writer, deps releaseDeps) int {
	fs := verbflag.New("fleet play")
	limit := fs.String("limit", "", "")
	dryRun := fs.Bool("dry-run", false, "")
	redisAddr := fs.String("redis", "", "")
	machines := fs.String("machines", "", "")
	playDir := fs.String("play-dir", "", "")
	pos, err := parseInterspersed(fs, args)
	if err != nil {
		return refuse(errOut, "fleet play", err.Error())
	}
	if len(pos) != 1 {
		return refuse(errOut, "fleet play", "wants one <tag>, the play's name (tools runs tools.yml in the play directory)")
	}
	tag := strings.TrimSuffix(pos[0], ".yml")
	home, err := deps.Home()
	if err != nil {
		return playRefused(errOut, fmt.Errorf("no home directory: %v", err))
	}
	registry := *machines
	if registry == "" {
		registry = deps.Getenv(fleetbuild.MachinesEnv)
	}
	if registry == "" {
		return playRefused(errOut, fmt.Errorf("the play's inventory reads the machines registry: --machines <file>, or %s", fleetbuild.MachinesEnv))
	}
	ms, err := fleetbuild.ReadMachinesFile(registry)
	if err != nil {
		return playRefused(errOut, fmt.Errorf("machines registry: %v (--machines <file>, or %s)", err, fleetbuild.MachinesEnv))
	}
	dir := *playDir
	if dir == "" {
		dir = deps.Getenv(fleetbuild.PlayDirEnv)
	}
	if dir == "" {
		dir = filepath.Join(home, filepath.FromSlash(fleetbuild.DefaultPlayDirRel))
	}
	var only []string
	for _, b := range strings.Split(*limit, ",") {
		if b = strings.TrimSpace(b); b != "" {
			only = append(only, b)
		}
	}
	p := &fleetbuild.Play{Runner: deps.Runner, PlayDir: dir, Tag: tag, Registry: registry,
		Machines: ms, Limit: only, DryRun: *dryRun, Out: out}
	if !*dryRun {
		st, err := deps.Open(ctx, *redisAddr)
		if err != nil {
			return unreachable(errOut, "fleet play", err.Error())
		}
		defer st.Close()
		p.Client = st.Client()
	}
	res, err := p.Run(ctx)
	if errors.Is(err, fleetbuild.ErrRefused) {
		return playRefused(errOut, err)
	}
	if err != nil {
		return unreachable(errOut, "fleet play", err.Error())
	}
	fmt.Fprintln(out, res.Line())
	if !res.OK() {
		return 1
	}
	return 0
}
