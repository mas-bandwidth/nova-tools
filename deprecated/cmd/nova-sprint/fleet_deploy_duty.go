// The deploy duty (#4050): every reconciler pass converges fleet:release and
// compares its version with the version each beating bench's beat names;
// with drift it starts `nova-sprint fleet build --bench <drifting>` in its own
// session, once per version (fleetbuild.DeployKey), so a landing into dev
// deploys itself within one tick. `fleet build duty` runs one pass by hand;
// --dry-run prints FLEET DEPLOY WOULD INSTALL per drifting bench and starts
// nothing.
package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/nsprint/fleetbuild"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/reconcile"
	"github.com/mas-bandwidth/nova-tools/internal/nsprint/store"
	"github.com/mas-bandwidth/nova-tools/internal/sprinttable"
	"github.com/mas-bandwidth/nova-tools/internal/testguard"
)

func init() {
	registerReconcileDuty("fleet-deploy", func(st *store.Store) (reconcileDuty, error) {
		// A registry that does not read leaves builder and self as stored;
		// it never keeps the reconciler from starting.
		ms, err := fleetMachines("")
		if err != nil && reconcileOut != nil {
			fmt.Fprintf(reconcileOut, "FLEET DEPLOY MACHINES %s unread: %v\n", fleetbuild.MachinesEnv, err)
		}
		return &fleetDeployDuty{d: &fleetbuild.Duty{Client: st.Client(), Machines: ms,
			Start: fleetDeployStart(st.Client().Options().Addr), Out: reconcileOut}}, nil
	})
}

// fleetDeployDuty adapts fleetbuild.Duty to the reconciler: it writes only
// fleet:release fields and the deploy claim, idempotent on the version, so it
// needs no fence token; the deploy runs in its own session past the pass.
type fleetDeployDuty struct{ d *fleetbuild.Duty }

func (f *fleetDeployDuty) Run(ctx context.Context, _ *reconcile.Lease) (reconcile.Counts, error) {
	_, err := f.d.Pass(ctx)
	return reconcile.Counts{}, err
}

// fleetDeployStart is the seam a test replaces: production starts this binary's
// `fleet build` in its own session, its output in
// ~/nova-bench/logs/fleet-build-<version>.log, and does not wait for it.
var fleetDeployStart = func(addr string) func(ctx context.Context, version string, benches []string) error {
	return func(ctx context.Context, version string, benches []string) error {
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		if strings.HasSuffix(exe, ".test") {
			// A test binary given `fleet build` would run its tests again.
			return fmt.Errorf("refusing to start the test binary %s as fleet build", filepath.Base(exe))
		}
		home, err := fleetBuildHome()
		if err != nil {
			return err
		}
		dir := filepath.Join(home, "nova-bench", "logs")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		logf, err := os.OpenFile(filepath.Join(dir, "fleet-build-"+version+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err != nil {
			return err
		}
		defer logf.Close()
		argv := []string{"fleet", "build", "--redis", addr, "--bench", strings.Join(benches, ",")}
		testguard.RefuseHosts(exe, argv...)
		cmd := exec.Command(exe, argv...)
		cmd.Stdout, cmd.Stderr = logf, logf
		if err := sprinttable.ApplyOwnSession(cmd); err != nil {
			return err
		}
		if err := cmd.Start(); err != nil {
			return err
		}
		go func() { _ = cmd.Wait() }()
		return nil
	}
}

func runFleetBuildDuty(ctx context.Context, redisAddr, machines string, dry bool, out, errOut io.Writer) int {
	ms, err := fleetMachines(machines)
	if err != nil {
		return buildRefused(errOut, fmt.Errorf("machines registry: %v (--machines <file>, or unset %s)", err, fleetbuild.MachinesEnv))
	}
	st, err := openFleetStore(ctx, redisAddr)
	if err != nil {
		return unreachable(errOut, "fleet build duty", err.Error())
	}
	defer st.Close()
	d := &fleetbuild.Duty{Client: st.Client(), Machines: ms, DryRun: dry, Out: out}
	if !dry {
		d.Start = fleetDeployStart(fleetAddr(redisAddr))
	}
	r, err := d.Pass(ctx)
	if err != nil {
		return unreachable(errOut, "fleet build duty", err.Error())
	}
	switch r.Outcome {
	case "START", "WOULD", "HELD":
		// The pass printed its receipt.
	default:
		fmt.Fprintf(out, "FLEET DEPLOY IDLE version=%s why=%s drift=%d current=%d quiet=%d\n",
			orDash(r.Version), strings.ToLower(r.Outcome), len(r.Drifting), len(r.Current), len(r.Quiet))
	}
	return 0
}
