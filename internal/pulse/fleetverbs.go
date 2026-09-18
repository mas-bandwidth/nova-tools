package pulse

// The one remote step the single-bench fleet verbs share, and the one way they resolve a
// bench name (SPEC-PULSE ## Fleet). `fleet standard`, `fleet mirror`, `fleet join` and
// `fleet sleep` act on ONE named bench, so each of them reads the benches file, refuses
// `studio` and an unknown name by name before any ssh, and then runs one bounded remote
// script through this interface -- which a test replaces, or drives through a fake ssh on
// PATH, so no unit test reaches a machine or opens a socket.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/fleet"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// FleetRunner is the one remote step: run a script on a bench and return what it said.
// The real one is `ssh <target> bash -s` with the script on stdin.
type FleetRunner interface {
	Run(ctx context.Context, target, script string) (string, error)
}

// SSHRunner is the real runner. Program is the ssh command from --ssh; empty is `ssh` on
// PATH.
type SSHRunner struct {
	Program string
}

func (r SSHRunner) Run(ctx context.Context, target, script string) (string, error) {
	return fleetSSH(ctx, r.Program, target, script)
}

// fleetOneBench resolves the one bench a single-bench verb was given. It returns the bench
// and 0, or a zero bench and the exit the verb must make: 2 for a missing flag, an
// unreadable file, `studio`, a name the file does not carry, or -- when --machines names the
// registry -- a machine whose roles lack `bench`. A refusal reaches stderr with its remedy;
// a refused BENCH prints its FLEET line on stdout, where the bench lines are.
func fleetOneBench(benchesPath, machinesPath, name string, stdout, stderr io.Writer, verb string) (FleetBench, int) {
	if strings.TrimSpace(benchesPath) == "" {
		return FleetBench{}, refusal(stderr, "FLEET", fmt.Errorf(
			"missing --benches; refusing to guess (run: nova-pulse fleet %s --benches <file> --bench <name>)", verb))
	}
	if strings.TrimSpace(name) == "" {
		return FleetBench{}, refusal(stderr, "FLEET", fmt.Errorf(
			"missing --bench; refusing to guess (run: nova-pulse fleet %s --benches %s --bench <name>)", verb, benchesPath))
	}
	benches, err := ReadFleetBenches(benchesPath)
	if err != nil {
		return FleetBench{}, refusal(stderr, "FLEET", fmt.Errorf(
			"%s (a benches file is name<TAB>ssh target<TAB>home<TAB>mac)", err))
	}
	reg, code := fleetRegistry(machinesPath, stderr)
	if code != 0 {
		return FleetBench{}, code
	}
	if r, refused := fleetPowerRefusal(name, benches, reg); refused {
		fmt.Fprintln(stdout, r.line)
		return FleetBench{}, 2
	}
	return benches[name], 0
}

// fleetRegistry reads the machines registry a verb was given. An empty path is the ONE
// narrowing in this guard: the fleet admin verbs predate the registry and are driven in
// tests (and by hand, on a bench that is not in it yet) without one, so an unnamed registry
// leaves the older refusals -- `studio` by name, a bench the benches file does not carry --
// as the whole guard. cmd/nova-pulse names the registry on every real invocation, and
// `nova-pulse fill`, which is the path a CARD takes, refuses outright without it.
func fleetRegistry(path string, stderr io.Writer) (*fleet.Registry, int) {
	if strings.TrimSpace(path) == "" {
		return nil, 0
	}
	reg, err := fleet.ReadRegistry(path)
	if err != nil {
		return nil, refusal(stderr, "FLEET", err)
	}
	return reg, 0
}

// fleetRoleRefusal holds one name against the registry: the lock's refusal line, or nothing
// when the machine is a bench (or when no registry was named).
func fleetRoleRefusal(name string, reg *fleet.Registry) (string, bool) {
	if reg == nil {
		return "", false
	}
	var r *fleet.Refusal
	if err := reg.RequireBench(name); errors.As(err, &r) {
		return r.Line("FLEET"), true
	}
	return "", false
}

// fleetUnreachable is the one shape a bench that did not answer prints, exit 3.
func fleetUnreachable(stdout io.Writer, name, reason string) int {
	fmt.Fprintf(stdout, "FLEET %s UNREACHABLE %s\n", oneline.Field(name), oneline.Escape(reason))
	return 3
}
