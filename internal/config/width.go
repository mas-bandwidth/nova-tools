package config

import (
	"context"
	"fmt"
	"sort"
	"strconv"
)

// A machine's width is the share of the machine's ceiling the sprint's member
// has: the ceiling (the machine row's slots, its one capacity field, written
// to machine:<m>:ceiling) less the desired slots of the friends charged to it.
// One ceiling per machine, shared by the friends and the sprint (the machine
// budget ruling of 2026-09-27): a friend's slots are hers, the rest is the
// member's. No field of the machine row holds it and none is typed; it is
// always derived from the rows, so there is no second inventory to keep in
// step with the first (docs/SPEC-CONFIG.md, "The sprint's width").
//
// It is the static share, the same on every read of the same rows, so that
// fleet sync can write it and read it back unchanged. It is not the room left
// now: the CI legs running on the machine (the beat's ci, nova-tools#4293)
// and every other child hold slots of the ceiling moment by moment, and they
// are taken off at the take, by a lease from the machine's one slot store
// (nova-swarm slots take, internal/swarm.TakeSlotLeases; the member takes
// min(width - held, free leases)), never here. The one subtraction below is
// exempt from the CI-legs class rule for that reason (TestSlotsShrinkByCILegs,
// internal/ci/cipriority_class_test.go).
//
// A machine with slots above 0 is a sprint member when it has room left
// (width 1 or more). A machine whose friends take the whole ceiling has width
// 0 and is no member: the sprint's width is a whole number from 1
// (internal/sprint/width.go), and a member wider than its share would break
// the ceiling the one slot store holds.

// MachineWidth is one machine row's capacity and what remains of it for the
// sprint.
type MachineWidth struct {
	Machine string
	// Slots is the machine's ceiling, the row's slots field.
	Slots int
	// Charged is the sum of the desired slots of the friends charged to the
	// machine.
	Charged int
	// Width is Slots less Charged, 0 when Charged is at least Slots.
	Width int
}

// Member says whether the machine is a member of the sprint's fleet: it has
// room after its friends.
func (w MachineWidth) Member() bool { return w.Width > 0 }

// HostReader reads where each named friend runs now: the machine her own
// beat reports, "" for a friend with no beat (RedisApplier.FriendHosts).
type HostReader interface {
	FriendHosts(ctx context.Context, names []string) (map[string]string, error)
}

// Widths is every machine row's width, in name order, read from one snapshot
// of the machine and fleet rows and the friend rows. A friend's desired slots
// are charged to the machine her beat reports, else to the fleet row's
// coordinator machine, the rule RedisApplier.charge applies when apply writes
// her (docs/SPEC-CONFIG.md, "What a friend's slots are charged to"). hosts
// is read only when a friend row has slots above 0; it may be nil when none
// has. A friend charged to a name that is no machine row is charged to
// nothing the sprint lists: the ceiling guard of apply is what refuses her.
func Widths(ctx context.Context, st Store, hosts HostReader) ([]MachineWidth, error) {
	machines, fleet, err := st.MachinesAndFleet(ctx)
	if err != nil {
		return nil, fmt.Errorf("widths: read machines and fleet: %w", err)
	}
	friends, err := st.List(ctx, KindFriend)
	if err != nil {
		return nil, fmt.Errorf("widths: read friends: %w", err)
	}
	var names []string
	for _, f := range friends {
		if f.Int("slots") > 0 {
			names = append(names, f.Name)
		}
	}
	charged := map[string]int{}
	if len(names) > 0 {
		if hosts == nil {
			return nil, fmt.Errorf("widths: %d friend row(s) carry slots and their machines come from their beats; a Redis address is needed to read them", len(names))
		}
		where, err := hosts.FriendHosts(ctx, names)
		if err != nil {
			return nil, fmt.Errorf("widths: read friends' beats: %w", err)
		}
		coordinator := fleet.Fields["coordinator"]
		for _, f := range friends {
			slots := f.Int("slots")
			if slots <= 0 {
				continue
			}
			m := where[f.Name]
			if m == "" {
				m = coordinator
			}
			if m == "" {
				return nil, fmt.Errorf("widths: friend %s has no beat naming a machine and the fleet names no coordinator machine to charge her slots to; run: nova-config fleet set --coordinator <machine>", f.Name)
			}
			charged[m] += slots
		}
	}
	out := make([]MachineWidth, 0, len(machines))
	for _, m := range machines {
		w := MachineWidth{Machine: m.Name, Slots: m.Int("slots"), Charged: charged[m.Name]}
		if w.Slots > w.Charged {
			w.Width = w.Slots - w.Charged
		}
		out = append(out, w)
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
	return "CONFIG WIDTH machine=" + Value(w.Machine) + " width=" + strconv.Itoa(w.Width) + " slots=" + strconv.Itoa(w.Slots) + " charged=" + strconv.Itoa(w.Charged) + " member=" + strconv.FormatBool(w.Member())
}
