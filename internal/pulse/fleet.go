package pulse

// The fleet verb: the machines named in one tab-separated file, acted on over ssh. This file
// is `fleet reboot` -- boot a bench, then wait for its runners to register again -- and the
// fleet power verbs `fleet suspend`, which sleeps the idle benches so a solar fleet does not
// burn the afternoon, and `fleet wake`, which wakes them by magic packet (SPEC-PULSE ## Fleet,
// issue #880 items 14 and 17).
//
// The fleet rule (docs/SPEC-PULSE.md, "Fleet"): every fleet verb prints one FLEET <name>
// line per bench, runs the benches in parallel under --timeout, exits 0/2/3 (0 ok, 2
// drift-or-refused, 3 unreachable), takes ssh from --ssh so a test puts a fake on PATH and
// no test makes a network call, and refuses `studio` for any admin act. The magic packet is
// built here, in Go, and sent through an injected sender: a test replaces it and no test
// opens a socket. The waiter here is a clock and a sleep handed in, so a test reaches a wall
// of minutes without waiting for one.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/testguard"
)

// fleetDefaultTimeout is the bound on every ssh child when the caller names none.
const fleetDefaultTimeout = 120 * time.Second

// FleetBench is one line of the benches file: name, ssh target, home, mac (mac is "-" when
// the bench never sleeps). The file is shared with `fleet survey`.
type FleetBench struct {
	Name string
	SSH  string
	Home string
	MAC  string
}

// fleetSSH runs one remote script on the bench with the ssh program from --ssh, through
// `ssh <target> bash -s`, bounded by --timeout. The target is the benches file's ssh
// column; the script is the remote command, on the child's stdin.
func fleetSSH(ctx context.Context, program, target, script string) (string, error) {
	if program == "" {
		program = "ssh"
	}
	testguard.RefuseHosts(program, "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", target, "bash", "-s")
	cmd := exec.CommandContext(ctx, program, "-o", "BatchMode=yes", "-o", "ConnectTimeout=5", target, "bash", "-s")
	cmd.Stdin = strings.NewReader(script)
	raw, err := cmd.CombinedOutput()
	return string(raw), err
}

// fleetQuote single-quotes a path for the remote shell.
func fleetQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// --- fleet suspend -------------------------------------------------------------------

// --- fleet wake ----------------------------------------------------------------------

// --- fleet reboot --------------------------------------------------------------------

// --- fleet secrets -------------------------------------------------------------------

// fleetBench is one row of the benches file: name<TAB>ssh-target<TAB>home<TAB>mac.
type fleetBench struct {
	Name   string
	Target string
	Home   string
	Mac    string
}

// readFleetBenches reads the shared fleet benches file. A line that is blank or starts
// with `#` is skipped; every other line needs at least name, ssh-target and home.
func readFleetBenches(path string) ([]fleetBench, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var out []fleetBench
	for i, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimRight(line, "\r")
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 3 {
			return nil, fmt.Errorf("line %d wants name, ssh-target and home, tab separated, got %d field(s)", i+1, len(f))
		}
		b := fleetBench{Name: f[0], Target: f[1], Home: f[2]}
		if len(f) > 3 {
			b.Mac = f[3]
		}
		if b.Name == "" || b.Target == "" || b.Home == "" {
			return nil, fmt.Errorf("line %d has an empty name, ssh-target or home", i+1)
		}
		out = append(out, b)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no benches in %s", path)
	}
	return out, nil
}

func lastLine(s string) string {
	s = strings.TrimRight(s, "\n")
	if i := strings.LastIndex(s, "\n"); i >= 0 {
		s = s[i+1:]
	}
	return strings.TrimSpace(s)
}
