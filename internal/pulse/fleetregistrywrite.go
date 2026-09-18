package pulse

// `nova-pulse fleet registry add` and `set`: the two verbs that WRITE the machines
// registry, so that nothing else has to.
//
// The registry is a control file: `nova-pulse fill` and every fleet verb resolve a
// `--bench` through it, and a wrong row puts a card on a CI runner host and stops the merge
// queue. On 2026-09-18 it was edited by hand three times -- `sed` twice and a python
// one-liner once -- and each of those edits was a control file changed by a tool that knows
// nothing about it. These verbs are the fix: the row is rendered, handed to the registry's
// own reader, and written only if the reader takes the WHOLE file back. A refusal leaves
// the file exactly as it was.
//
// They are the only writers. Neither touches a machine, opens a socket or calls a model:
// `add` and `set` are a read, a parse and a rename.

import (
	"fmt"
	"io"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/fleet"
)

// FleetRegistryAddInput is everything `registry add` needs apart from flag parsing.
type FleetRegistryAddInput struct {
	Machines string // the machines file, read and written
	Draft    fleet.Draft
	Stdout   io.Writer
	Stderr   io.Writer
}

// FleetRegistryAdd adds one machine to the registry and prints what it wrote:
//
//	REGISTRY ADDED name=air machines=queue/control/machines.tsv
//	MACHINE air ssh=glenn@air os=darwin/arm64 roles=bench,runner ... notes="..."
//
// Exit 0 when the row was written, 2 when the file or the row was refused.
func FleetRegistryAdd(in FleetRegistryAddInput) int {
	if strings.TrimSpace(in.Machines) == "" {
		return refusal(in.Stderr, "REGISTRY", fmt.Errorf(
			"missing --machines; refusing to guess (run: nova-pulse fleet registry add --machines queue/control/machines.tsv --name <n> --ssh <user@host> --os <goos/goarch> --roles <a,b> --seat <s|-> --cores <n> --notes <text>)"))
	}
	reg, err := fleet.ReadRegistry(in.Machines)
	if err != nil {
		return refusal(in.Stderr, "REGISTRY", err)
	}
	next, m, err := reg.Add(in.Draft)
	if err != nil {
		return refusal(in.Stderr, "REGISTRY", err)
	}
	if err := next.Save(); err != nil {
		return refusal(in.Stderr, "REGISTRY", err)
	}
	fmt.Fprintf(in.Stdout, "REGISTRY ADDED name=%s machines=%s\n", field(m.Name), field(in.Machines))
	fmt.Fprintln(in.Stdout, machineLine(m))
	return 0
}

// FleetRegistrySetInput is everything `registry set` needs apart from flag parsing.
type FleetRegistrySetInput struct {
	Machines string // the machines file, read and written
	Name     string // the machine to change
	Change   fleet.Change
	Stdout   io.Writer
	Stderr   io.Writer
}

// FleetRegistrySet changes the named machine's columns and prints what it wrote:
//
//	REGISTRY SET name=hulk fields=notes machines=queue/control/machines.tsv
//	MACHINE hulk ssh=hulk os=linux/x64 roles=bench,runner ... notes="..."
//
// A column the command line did not name is left exactly as the file has it. Exit 0 when
// the row was written, 2 when the file, the name or the new row was refused.
func FleetRegistrySet(in FleetRegistrySetInput) int {
	if strings.TrimSpace(in.Machines) == "" {
		return refusal(in.Stderr, "REGISTRY", fmt.Errorf(
			"missing --machines; refusing to guess (run: nova-pulse fleet registry set --machines queue/control/machines.tsv --name <n> --notes <text>)"))
	}
	if strings.TrimSpace(in.Name) == "" {
		return refusal(in.Stderr, "REGISTRY", fmt.Errorf(
			"missing --name; refusing to guess which machine to change (run: nova-pulse fleet registry --machines %s to list them)", in.Machines))
	}
	reg, err := fleet.ReadRegistry(in.Machines)
	if err != nil {
		return refusal(in.Stderr, "REGISTRY", err)
	}
	next, m, err := reg.Set(in.Name, in.Change)
	if err != nil {
		return refusal(in.Stderr, "REGISTRY", err)
	}
	if err := next.Save(); err != nil {
		return refusal(in.Stderr, "REGISTRY", err)
	}
	fmt.Fprintf(in.Stdout, "REGISTRY SET name=%s fields=%s machines=%s\n",
		field(m.Name), field(strings.Join(in.Change.Fields(), ",")), field(in.Machines))
	fmt.Fprintln(in.Stdout, machineLine(m))
	return 0
}
