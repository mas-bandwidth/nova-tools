package config

import (
	"context"
	"fmt"
	"sort"
	"strconv"
)

// A machine's width is the most work cards the sprint's member on the machine
// runs at once: the machine row's width field, set directly (nova-config
// machine set <m> --width <n>) and nothing else. No other row and no live
// state takes part in it: no friend row, no beat, no Redis
// (docs/SPEC-CONFIG.md, "The sprint's width").
// nova-sprint fleet sync moves it to the fleet table as it reads.
//
// It is the static share, the same on every read of the same row. It is not
// the room left now: the CI legs running on the machine and every other child
// are taken off at the take, by a lease from the machine's one slot store
// (nova-swarm slots take, internal/swarm.TakeSlotLeases), never here.
//
// A machine with width 1 or more is a member of the sprint's fleet; width 0
// is no member: the sprint's width is a whole number from 1
// (internal/sprint/width.go). A row with no width (unset, the default, or
// cleared with --width default) is a member at the default width, half the
// machine's cores as its beat reports them, which nova-sprint fleet sync
// resolves (the owner, 2026-10-02: "default is CPUs/2"; sprint.WidthOfCores):
// the config holds no number for it.

// MachineWidth is one machine row's width.
type MachineWidth struct {
	Machine string
	// Width is the row's width field; 0 when Default.
	Width int
	// Default says the row has no width: the default, resolved from the cores.
	Default bool
}

// Member says whether the machine is a member of the sprint's fleet: its
// width is above 0, or it has the default width.
func (w MachineWidth) Member() bool { return w.Width > 0 || w.Default }

// Widths is every machine row's width, in name order, read from the machine
// rows alone.
func Widths(ctx context.Context, st Store) ([]MachineWidth, error) {
	machines, err := st.List(ctx, KindMachine)
	if err != nil {
		return nil, fmt.Errorf("widths: read machines: %w", err)
	}
	out := make([]MachineWidth, 0, len(machines))
	for _, m := range machines {
		out = append(out, MachineWidth{Machine: m.Name, Width: m.Int("width"), Default: m.Fields["width"] == ""})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Machine < out[j].Machine })
	return out, nil
}

// WidthOf is one machine's width from Widths; found is false when no machine
// row has the name.
func WidthOf(ws []MachineWidth, name string) (w MachineWidth, found bool) {
	for _, x := range ws {
		if x.Machine == name {
			return x, true
		}
	}
	return MachineWidth{}, false
}

// Line is the width's one printed line.
func (w MachineWidth) Line() string {
	width := strconv.Itoa(w.Width)
	if w.Default {
		width = "default"
	}
	return "CONFIG WIDTH machine=" + Value(w.Machine) + " width=" + width + " member=" + strconv.FormatBool(w.Member())
}
