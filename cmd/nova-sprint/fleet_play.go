// fleet play --play <tag> [--bench a,b] [--dry-run] (#4356 item C) runs one
// rowan-tools fleet play through the verb, never by hand
// (fleet-changes-only-through-ansible): <tag>.yml in the play directory with
// the machines registry as its inventory, refused on a dirty or behind
// rowan-tools clone (the line names the git command), then one
// FLEET PLAY <bench> <role> ok|changed|failed ms=<n> receipt per bench and
// role read from ansible's own output, each bench's receipt written to
// bench:<b>:play for fleet doctor and the table, and FLEET PLAY OK|FAIL last
// (internal/nsprint/fleetbuild/play.go).
//
//	fleet play --play <tag> [--bench <a,b,...>] [--dry-run] [--redis <addr>]
//	           [--machines <file>] [--play-dir <dir>]
//
// One grammar (#4352 A): the play is --play (with or without .yml), the
// benches it is limited to are --bench, a comma list, as on fleet release.
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
	play := fs.String("play", "", "the play to run: its tag (tools runs tools.yml in the play directory)")
	limit := fs.String("bench", "", "the benches the play is limited to, comma-separated (default every machine of the registry)")
	dryRun := fs.Bool("dry-run", false, verbflag.HelpDryRun)
	redisAddr := fs.String("redis", redisDefault(), verbflag.HelpRedis)
	machines := fs.String("machines", "", "the machines registry file (default NOVA_FLEET_MACHINES)")
	playDir := fs.String("play-dir", "", "the play directory, a rowan-tools clone (default NOVA_FLEET_PLAY_DIR, else ~/"+fleetbuild.DefaultPlayDirRel+")")
	if err := fs.Parse(args); err != nil {
		return refuse(errOut, "fleet play", err.Error())
	}
	if fs.NArg() > 0 {
		return refuse(errOut, "fleet play", "takes flags, not positional arguments: the play is --play <tag>")
	}
	if *play == "" {
		return refuse(errOut, "fleet play", "wants --play <tag>, the play's name (tools runs tools.yml in the play directory)")
	}
	tag := strings.TrimSuffix(*play, ".yml")
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
	only := verbflag.List(*limit)
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
