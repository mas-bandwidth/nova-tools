package main

import (
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/sprint"
	"github.com/mas-bandwidth/nova-tools/pkg/release"
)

// adoptFlagsEnv names adopt's flags for a fleet member back from down
// (docs/SPEC-SPRINT.md section 5, "Back from down: adopt the latest"): set, on
// the coordinator's machine, the tick holds a member whose beat returns after
// it was down, adopts the release this nova-sprint runs onto that machine
// alone (nova-update release adopt, one machine) and brings it up when its
// installed version reads back as that release. Unset, or a binary with no
// release stamped, a member back is up at once, as it was.
const adoptFlagsEnv = "NOVA_SPRINT_ADOPT_FLAGS"

func init() {
	if a := fleetBackAdopter(os.Getenv, version); a != nil {
		sprint.InstallFleetBack(a)
	}
}

// fleetBackAdopter is the release adopt path for one machine at the release
// stamp, with the flags getenv's adoptFlagsEnv names; nil when either is
// empty.
func fleetBackAdopter(getenv func(string) string, stamp string) sprint.Adopter {
	flags := strings.Fields(getenv(adoptFlagsEnv))
	if len(flags) == 0 || strings.TrimSpace(stamp) == "" {
		return nil
	}
	return &releaseAdopter{one: &release.OneMachine{Version: stamp, Flags: flags,
		Deps: release.Deps{Self: func() string { return stamp }}}}
}

// releaseAdopter is a release.OneMachine as the tick's Adopter.
type releaseAdopter struct{ one *release.OneMachine }

func (r *releaseAdopter) Target() string { return r.one.Version }

func (r *releaseAdopter) Start(machine, version, episode string) bool {
	return r.one.Start(machine, version, episode)
}

func (r *releaseAdopter) Adoption(machine, episode string) sprint.MachineAdoption {
	run, ok := r.one.Run(machine, episode)
	switch {
	case !ok:
		return sprint.MachineAdoption{}
	case run.Running:
		return sprint.MachineAdoption{State: sprint.AdoptRunning, From: run.From}
	case run.Err != "":
		return sprint.MachineAdoption{State: sprint.AdoptFailed, From: run.From, To: run.To, Err: run.Err}
	}
	return sprint.MachineAdoption{State: sprint.AdoptFinished, From: run.From, To: run.To}
}
