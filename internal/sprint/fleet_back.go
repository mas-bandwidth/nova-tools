package sprint

import (
	"fmt"
	"maps"
	"time"
)

// A fleet member back from down adopts the latest before it is dealt
// (docs/SPEC-SPRINT.md section 5, "Back from down: adopt the latest"). The
// owner, 2026-10-05 ~10:00 AM ET: "when fleet machines come back after a long
// time down, we need to remember to bring them back up and have them adopt
// latest. Just like friends." A member down whose beat returns is not brought
// up at once: the tick holds it adopting, starts the adoption of the release
// the coordinator's own machine runs on that machine alone (the release adopt
// path, one machine), reads its installed version back, and brings it up at
// its row's width with one note ("<m> is back: <old> -> <new>") when the
// version read back is that release. A failed adoption keeps it held, the
// failure as the hold's reason, with one judgment (NAdoptFailed) while it
// stays so. One adoption is in flight per machine at a time.

// Adopting is the status of a member back from down, held while it adopts the
// release the coordinator's machine runs.
const Adopting = "adopting"

// HeldByAdopt is the hold mark (FieldHeldBy) of a member held adopting: the
// tick alone releases it, and a coordinator's hold or release takes it over.
const HeldByAdopt = "adopt"

// The control card's fields of an adoption: the episode (when the member was
// held adopting, the key of its one adoption), the release it adopts, and the
// failure that keeps it held.
const (
	FieldAdoptSince  = "adopt_since"
	FieldAdoptTo     = "adopt_to"
	FieldAdoptFailed = "adopt_failed"
)

// NMemberBack is the happened note of a member back from down at the latest;
// NAdoptFailed the judgment of one whose adoption failed.
const (
	NMemberBack  = "fleet member back"
	NAdoptFailed = "a member's adoption failed"
)

// The states of one machine's adoption.
const (
	AdoptNone     = ""
	AdoptRunning  = "running"
	AdoptFinished = "done"
	AdoptFailed   = "failed"
)

// MachineAdoption is one machine's adoption of an episode: its state, the
// version it had installed before, the version read back after, and why it
// failed.
type MachineAdoption struct {
	State string
	From  string
	To    string
	Err   string
}

// Adopter adopts a release onto one machine at a time. Target is the release
// the coordinator's own machine runs ("" is none known, and nothing is
// adopted); Start begins the adoption of version on the machine for the
// episode, and is false while one is in flight there (never two at once);
// Adoption is the episode's adoption, AdoptNone when it was never started.
// The tick calls it from inside a plan, so a call must be quick: the work runs
// beside the tick (cmd/nova-sprint/fleet_back.go, pkg/release/adopt_one.go).
type Adopter interface {
	Target() string
	Start(machine, version, episode string) bool
	Adoption(machine, episode string) MachineAdoption
}

// FleetBackPresence is the presence part with members back from down held to
// adopt the latest through a. With a nil a or no target it is TickPresence.
func FleetBackPresence(a Adopter) TickPartFn {
	return func(s *Snapshot, r TickReq) (Plan, int) {
		if a == nil || r.Beats == nil {
			return presence(s, r)
		}
		target := a.Target()
		if target == "" {
			return presence(s, r)
		}
		var back, adopting []string
		for _, m := range s.Members() {
			ctl := s.MemberCtl(m)
			switch {
			case ctl == nil:
			case AdoptingHeld(ctl):
				adopting = append(adopting, m)
			case ctl.F("held") == "" && ctl.F("status") == Down && r.Beats[m].Alive(s.Now):
				back = append(back, m)
			}
		}
		if len(back) == 0 && len(adopting) == 0 {
			return presence(s, r)
		}
		// presence sees no beat of a member back from down: it stays down
		// there, and is held adopting here, before any deal reaches it
		beats := maps.Clone(r.Beats)
		for _, m := range back {
			delete(beats, m)
		}
		rp := r
		rp.Beats = beats
		p, due := presence(s, rp)
		for _, m := range back {
			p.Units = append(p.Units, holdAdopting(s, m, target))
		}
		for _, m := range adopting {
			if u, ok := adoptStep(s, a, m, r.who()); ok {
				p.Units = append(p.Units, u)
			}
		}
		return p, due
	}
}

// AdoptingHeld says the member is held by the tick while it adopts.
func AdoptingHeld(ctl *Card) bool {
	return ctl.F("held") != "" && ctl.F(FieldHeldBy) == HeldByAdopt
}

// FleetRowStatus is a member's status cell in the fleet table at now:
// adopting while the tick holds it to adopt, its own status beside up, held
// and down, and never up whatever it beats; else MemberStatus.
func FleetRowStatus(ctl *Card, b Beat, now time.Time) string {
	if AdoptingHeld(ctl) {
		return Adopting
	}
	return MemberStatus(ctl, b, now)
}

// holdAdopting is a member back from down held adopting target: its episode
// begins now, and nothing is dealt to it while it holds.
func holdAdopting(s *Snapshot, m, target string) Unit {
	ctl := s.MemberCtl(m)
	now := stamp(s.Now)
	set := map[string]string{"status": Adopting, "since": now, "held": now, FieldHeldBy: HeldByAdopt,
		FieldHeldReason: "adopting " + target + ": back from down", FieldAdoptSince: now, FieldAdoptTo: target}
	return Unit{Key: CtlID(m), Changes: []Change{change(Fleet, setEntry(ctl, set, FieldAdoptFailed, FieldHeldFinish))},
		Moved: fmt.Sprintf("%s adopting %s: back from down", m, target)}
}

// adoptStep is one tick of a member held adopting: its adoption started when
// none was, nothing while it runs, the member up with one note when the version
// read back is the one it adopts, and held with the failure as its reason when
// it failed (the judgment is the deal's condition, adoptConds).
func adoptStep(s *Snapshot, a Adopter, m, who string) (Unit, bool) {
	ctl := s.MemberCtl(m)
	if ctl.F(FieldAdoptFailed) != "" {
		return Unit{}, false
	}
	episode, want := ctl.F(FieldAdoptSince), ctl.F(FieldAdoptTo)
	run := a.Adoption(m, episode)
	switch run.State {
	case AdoptNone:
		// false is an adoption of this machine in flight already: never two
		a.Start(m, want, episode)
		return Unit{}, false
	case AdoptRunning:
		return Unit{}, false
	case AdoptFinished:
		if run.To == want {
			set := map[string]string{"status": Up, "since": stamp(s.Now)}
			n := happened(NMemberBack, "", s.Now)
			n.Who, n.What = who, fmt.Sprintf("%s is back: %s -> %s", m, orDash(run.From), run.To)
			return Unit{Key: CtlID(m), Changes: []Change{change(Fleet, setEntry(ctl, set,
				"held", FieldHeldBy, FieldHeldReason, FieldHeldFinish, FieldAdoptSince, FieldAdoptTo, FieldAdoptFailed))},
				Notes: []Note{n}, Moved: fmt.Sprintf("%s up at %s (was %s): back from down", m, run.To, orDash(run.From))}, true
		}
		run.Err = fmt.Sprintf("its installed version reads %s after the adoption, not %s", orDash(run.To), want)
	}
	why := run.Err
	if why == "" {
		why = "the adoption failed and said nothing"
	}
	set := map[string]string{FieldAdoptFailed: why, FieldHeldReason: "adoption of " + want + " failed: " + why}
	return Unit{Key: CtlID(m), Changes: []Change{change(Fleet, setEntry(ctl, set))},
		Moved: fmt.Sprintf("%s held: adoption of %s failed: %s", m, want, why)}, true
}

// adoptConds is the judgment of each member held with a failed adoption, held
// open while it stays so: one per member, never one per tick.
func adoptConds(s *Snapshot) []cond {
	var out []cond
	for _, m := range s.Members() {
		ctl := s.MemberCtl(m)
		if ctl == nil || !AdoptingHeld(ctl) || ctl.F(FieldAdoptFailed) == "" {
			continue
		}
		out = append(out, cond{typ: NAdoptFailed, stream: MemberSubject(m), streamLevel: true,
			what: fmt.Sprintf("%s is back from down and its adoption of %s failed: %s; it stays held (fleet up %s brings it up as it is)",
				m, ctl.F(FieldAdoptTo), ctl.F(FieldAdoptFailed), m),
			decisions: []string{"fleet up " + m, "wait"}})
	}
	return out
}

// InstallFleetBack makes FleetBackPresence(a) the presence part the machine
// runs (TickTables and TickParts), as friend_read.go installs the friend ask:
// the binary that knows the release it runs installs it at its start, before
// any tick.
func InstallFleetBack(a Adopter) {
	fn := FleetBackPresence(a)
	for i := range TickTables {
		for j := range TickTables[i].Parts {
			if TickTables[i].Parts[j].Name == "presence" {
				TickTables[i].Parts[j].Fn = fn
			}
		}
	}
	for i := range TickParts {
		if TickParts[i].Name == "presence" {
			TickParts[i].Fn = fn
		}
	}
}
