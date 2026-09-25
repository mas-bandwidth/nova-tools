package pulse

// `nova-pulse fleet registry` lists the machines registry: what each machine in the fleet
// IS, and therefore what may be placed on it (internal/fleet, the lock of 2026-09-18 --
// runner hosts are CI-only).
//
// It is a reading verb and nothing else: no ssh, no machine touched, no model call. It
// exists because the registry is the answer to a question people were asking by hand
// ("can I fill batman?") and getting wrong, and because every refusal the other verbs print
// names this file -- so the file must be readable with one command from anywhere.

import (
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/bounded"
	"github.com/mas-bandwidth/nova-tools/internal/fleet"
	"github.com/mas-bandwidth/nova-tools/internal/oneline"
)

// FleetRegistryInput is everything the verb needs apart from flag parsing.
type FleetRegistryInput struct {
	Machines string // the machines file
	Role     string // list only the machines carrying this role; empty is all of them
	Max      int    // at most this many MACHINE lines; 0 is all
	Stdout   io.Writer
	Stderr   io.Writer
}

// FleetRegistry prints one MACHINE line per machine, in file order:
//
//	MACHINE hulk ssh=hulk os=linux/x64 roles=bench,runner seat=swarm-hulk cores=64 notes="..."
//
// Exit 0 when the registry read, 2 when it did not or when --role names a role no machine
// carries (a role nobody carries is far more likely a typo than a fleet fact).
func FleetRegistry(in FleetRegistryInput) int {
	if strings.TrimSpace(in.Machines) == "" {
		return refusal(in.Stderr, "MACHINE", fmt.Errorf(
			"missing --machines; refusing to guess (run: nova-pulse fleet registry --machines queue/control/machines.tsv)"))
	}
	reg, err := fleet.ReadRegistry(in.Machines)
	if err != nil {
		return refusal(in.Stderr, "MACHINE", err)
	}

	machines := reg.Machines()
	role := strings.TrimSpace(in.Role)
	if role != "" {
		machines = reg.WithRole(role)
		if len(machines) == 0 {
			return refusal(in.Stderr, "MACHINE", fmt.Errorf(
				"no machine in %s carries the role %s; the roles are %s, %s, %s, %s",
				in.Machines, oneline.Field(role),
				fleet.RoleBench, fleet.RoleRunner, fleet.RoleCoordination, fleet.RoleServices))
		}
	}

	list := bounded.Capped(in.Stdout, in.Max, "MACHINE", "machine",
		"run: nova-pulse fleet registry --machines "+in.Machines+" --max 0")
	for _, m := range machines {
		list.Line(machineLine(m))
	}
	list.More()
	return 0
}

// machineLine is one machine as one line. Every column the file carries is on it, so a
// reader never has to open the file to learn what a refusal was about.
func machineLine(m fleet.Machine) string {
	return fmt.Sprintf("MACHINE %s ssh=%s os=%s/%s roles=%s seat=%s cores=%d notes=%s",
		oneline.Field(m.Name), oneline.Field(m.SSH), oneline.Field(m.OS), oneline.Field(m.Arch),
		oneline.Field(m.RoleList()), oneline.Field(dash(m.Seat)), m.Cores,
		oneline.Quote(dash(m.Notes)))
}
