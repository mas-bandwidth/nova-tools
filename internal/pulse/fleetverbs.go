package pulse

// The one remote step the single-bench fleet verbs share, and the one way they resolve a
// bench name (SPEC-PULSE ## Fleet). `fleet standard`, `fleet mirror`, `fleet join` and
// `fleet sleep` act on ONE named bench, so each of them reads the benches file, refuses
// `studio` and an unknown name by name before any ssh, and then runs one bounded remote
// script through this interface -- which a test replaces, or drives through a fake ssh on
// PATH, so no unit test reaches a machine or opens a socket.

import (
	"context"
	"fmt"
	"io"
	"strings"

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
// unreadable file, `studio`, or a name the file does not carry. A refusal reaches stderr
// with its remedy; a refused BENCH prints its FLEET line on stdout, where the bench lines
// are.
func fleetOneBench(benchesPath, name string, stdout, stderr io.Writer, verb string) (FleetBench, int) {
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
	if r, refused := fleetPowerRefusal(name, benches); refused {
		fmt.Fprintln(stdout, r.line)
		return FleetBench{}, 2
	}
	return benches[name], 0
}

// fleetUnreachable is the one shape a bench that did not answer prints, exit 3.
func fleetUnreachable(stdout io.Writer, name, reason string) int {
	fmt.Fprintf(stdout, "FLEET %s UNREACHABLE %s\n", oneline.Field(name), oneline.Escape(reason))
	return 3
}
