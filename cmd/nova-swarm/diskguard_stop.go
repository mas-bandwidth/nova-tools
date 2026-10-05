package main

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/oneline"
	"github.com/mas-bandwidth/nova-tools/internal/subproc"
)

// THE STOP FLOOR (docs/COORDINATOR-TOOLS.md, the disk-guard row).
//
// Under --disk-floor the guard warns and the members start no card; the loops that are
// not members (land, mirrors, runners, servers) go on writing. Under --stop-floor, lower,
// the guard stops every loop unit of this login (launchd's com.nova.loop.<name> on a Mac,
// systemd's nova-loop-<name> service and timer elsewhere), the disk-guard loops apart, so
// the guard that would free the disk keeps running and nothing else fills it. A stopped
// loop is started again by hand (or by fleet/loops.yml) once there is room: the guard
// never starts anything. This is the bash disk-guard's 100 GiB stop, one coordinator ran
// from its own tool repository, as a flag of the installed verb.

// loopUnitKept is the word in a unit's name that keeps it running under the stop floor:
// the disk guard's own loops.
const loopUnitKept = "disk-guard"

// stopUnder stops every loop unit but the guard's own when the free disk on any volume it
// looked at is under the stop floor.
func (g *guard) stopUnder(free uint64, where string) {
	if g.stopFloor <= 0 || free >= uint64(g.stopFloor) || g.stopped {
		return
	}
	g.stopped = true
	units, err := g.loopUnits()
	if err != nil {
		g.fail(fmt.Sprintf("free=%d is under the stop floor %d on the volume of %s and the loop units could not be listed (%s); stop them by hand", free, g.stopFloor, oneline.Field(where), oneline.Err(err)))
		return
	}
	for _, u := range units {
		if strings.Contains(u, loopUnitKept) {
			continue
		}
		if err := g.unless(func() error { return g.stopUnit(u) }); err != nil {
			g.fail(fmt.Sprintf("the loop unit %s could not be stopped (%s)", oneline.Field(u), oneline.Err(err)))
			continue
		}
		g.say(fmt.Sprintf("DISK-GUARD STOPPED unit=%s free=%d stop-floor=%d: start it again once there is room", oneline.Field(u), free, g.stopFloor))
	}
}

// loopUnitsHost lists this login's loop units: launchctl's labels on darwin, systemd's
// user units elsewhere, services and timers both (a timer left running starts its
// oneshot again).
func loopUnitsHost() ([]string, error) {
	if runtime.GOOS == "darwin" {
		b, err := unitCommand("launchctl", "list")
		if err != nil {
			return nil, err
		}
		return launchctlLoops(string(b)), nil
	}
	b, err := unitCommand("systemctl", "--user", "list-units", "--all", "--plain", "--no-legend", "--type=service,timer", "nova-loop-*")
	if err != nil {
		return nil, err
	}
	return systemctlLoops(string(b)), nil
}

// stopUnitHost stops one loop unit: bootout from the login's launchd domain, or
// systemctl --user stop.
func stopUnitHost(unit string) error {
	if runtime.GOOS == "darwin" {
		_, err := unitCommand("launchctl", "bootout", "gui/"+strconv.Itoa(os.Getuid())+"/"+unit)
		return err
	}
	_, err := unitCommand("systemctl", "--user", "stop", unit)
	return err
}

func unitCommand(name string, args ...string) ([]byte, error) {
	cmd, cancel := subproc.CommandFor(context.Background(), 30*time.Second, name, args...)
	defer cancel()
	return cmd.Output()
}

// launchctlLoops is the com.nova.loop.* labels of `launchctl list` (PID, Status, Label).
func launchctlLoops(list string) []string {
	var out []string
	for _, line := range strings.Split(list, "\n") {
		f := strings.Fields(line)
		if len(f) == 3 && strings.HasPrefix(f[2], "com.nova.loop.") {
			out = append(out, f[2])
		}
	}
	return out
}

// systemctlLoops is the nova-loop-* units of `systemctl list-units --plain --no-legend`.
func systemctlLoops(list string) []string {
	var out []string
	for _, line := range strings.Split(list, "\n") {
		f := strings.Fields(line)
		if len(f) > 0 && strings.HasPrefix(f[0], "nova-loop-") {
			out = append(out, f[0])
		}
	}
	return out
}
