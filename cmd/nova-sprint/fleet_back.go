package main

import (
	"os"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/release"
	"github.com/mas-bandwidth/nova-tools/internal/sprint"
)

// adoptFlagsEnv names adopt's flags for a fleet member back from down
// (docs/SPEC-SPRINT.md section 5, "Back from down: adopt the latest"): set, on
// the coordinator's machine, the tick holds a member whose beat returns after
// it was down, adopts the release this nova-sprint runs onto that machine
// alone (nova-update release adopt, one machine) and brings it up when its
// installed version reads back as that release. Missing flags or a release
// stamp keep the member held with an actionable failure judgment.
const adoptFlagsEnv = "NOVA_SPRINT_ADOPT_FLAGS"

func init() {
	sprint.InstallFleetBack(fleetBackAdopter(os.Getenv, version))
}

// fleetBackAdopter is the release adopt path for one machine at the release
// stamp, with the flags getenv's adoptFlagsEnv names. It remains installed
// when configuration is missing so the tick fails closed.
func fleetBackAdopter(getenv func(string) string, stamp string) sprint.Adopter {
	flags := strings.Fields(getenv(adoptFlagsEnv))
	if len(flags) == 0 || release.ValidVersion(stamp) != nil {
		return &releaseAdopter{}
	}
	return &releaseAdopter{one: &release.OneMachine{Version: stamp, Flags: flags,
		Deps: release.Deps{Self: func() string { return stamp }}}}
}

// releaseAdopter is a release.OneMachine as the tick's Adopter.
type releaseAdopter struct{ one *release.OneMachine }

func (r *releaseAdopter) Target() string {
	if r.one == nil {
		return ""
	}
	return r.one.Version
}

func (r *releaseAdopter) Start(machine, version, episode string) bool {
	return r.one.Start(machine, version, episode)
}

func (r *releaseAdopter) Adoption(machine, episode string) sprint.Adoption {
	run, ok := r.one.Run(machine, episode)
	switch {
	case !ok:
		return sprint.Adoption{}
	case run.Running:
		return sprint.Adoption{State: sprint.AdoptRunning, From: run.From}
	case run.Err != "":
		return sprint.Adoption{State: sprint.AdoptFailed, From: run.From, To: run.To, Err: run.Err}
	}
	return sprint.Adoption{State: sprint.AdoptDone, From: run.From, To: run.To}
}
