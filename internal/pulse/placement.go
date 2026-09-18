package pulse

// ONE GUARD, EVERY ROAD INTO A BENCH.
//
// A card reaches a machine by two roads: `fill`, which reads a bench's capacity and pops
// the ready directory, and `run`'s launcher (wire.go's Wiring.Launch), which fills the free
// slots of every root from queue/pending. Until 2026-09-18 only the first road asked the
// machines registry and the lanes table. So `nova-pulse fill --machines ... --bench mini`
// refused BY NAME -- `FILL REFUSED bench=mini reason=runner-host` -- and a `run` tick placed
// a card on that same host without a word, which is Glenn's registry lock of 2026-09-18
// holding on one path in and not the other (the manager dogfood, edge 10).
//
// This is that guard, once, as a value both roads take a card through:
//
//  1. THE MACHINE. A bench whose roles lack `bench` never sees a card -- runner hosts serve
//     the merge group's shards, and a card on one makes the shard slow, the gate red and the
//     queue stop.
//  2. THE LANE. At most one live card per lane; a lane the table does not name is refused.
//  3. THE GATE. A card carrying `AFTER: PR<n> merged` waits until the forge says merged
//     (cardgate.go, SPEC-PULSE rule 4).
//
// It reads the registry and the lanes table ONCE per tick and asks the forge once per
// distinct pull request, so a ready directory of nine cards behind one PR is one call.

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/fleet"
)

// placeVerdict is why a card may not be placed this tick, or the zero value when it may.
type placeVerdict struct {
	Kind   string // "", "lane-unknown", "lane-held", "gated"
	Lane   string
	Holder string // the live card holding the lane
	PR     int    // the pull request a gated card waits on
	State  string // what the forge said that PR is
	Text   string // the card's text, with the gate line taken off when the gate opened
	Opened bool   // the gate was shut and the forge has now opened it: write Text back
}

// ok says the card may be placed.
func (v placeVerdict) ok() bool { return v.Kind == "" }

// placement is the guard, built once per tick.
type placement struct {
	reg    *fleet.Registry
	lanes  map[string]bool
	strict bool // a lanes file was named, so an unknown lane is a refusal
	live   map[string]string
	forge  PRSource
	repo   string
	gates  map[int]bool
	states map[int]string
}

// newPlacement reads the registry and the lanes table and takes a snapshot of what is live.
// An empty --machines leaves the registry unasked (the caller names that as an unwired
// guard); an unreadable one is an error, because a registry that is there and cannot be
// read is not a bench list anybody may place against.
func newPlacement(machines, lanesFile, launched, repo string, forge PRSource) (*placement, error) {
	p := &placement{
		lanes:  laneTable(lanesFile),
		strict: strings.TrimSpace(lanesFile) != "",
		live:   liveLanes(launched),
		forge:  forge,
		repo:   repo,
		gates:  map[int]bool{},
		states: map[int]string{},
	}
	if strings.TrimSpace(machines) != "" {
		reg, err := fleet.ReadRegistry(machines)
		if err != nil {
			return nil, err
		}
		p.reg = reg
	}
	return p, nil
}

// bench holds one machine name against the registry. A registry nobody named holds nothing,
// which is the caller's to say out loud.
func (p *placement) bench(name string) error {
	if p.reg == nil {
		return nil
	}
	return p.reg.RequireBench(name)
}

// admit runs one card through the lane and the gate. It never moves anything: the caller
// owns the move, the marker and the line it prints.
func (p *placement) admit(card string) placeVerdict {
	lane := cardLane(card)
	if lane != "" {
		if _, known := p.lanes[lane]; !known && p.strict {
			return placeVerdict{Kind: "lane-unknown", Lane: lane}
		}
		if holder, isLive := p.live[lane]; isLive {
			return placeVerdict{Kind: "lane-held", Lane: lane, Holder: holder}
		}
	}
	text := readCard(card)
	pr := cardGateOf(text)
	if pr == 0 {
		return placeVerdict{Lane: lane, Text: text}
	}
	open, asked := p.gates[pr]
	if !asked {
		open, p.states[pr] = gateState(p.forge, p.repo, pr)
		p.gates[pr] = open
	}
	if !open {
		return placeVerdict{Kind: "gated", Lane: lane, PR: pr, State: p.states[pr]}
	}
	return placeVerdict{Lane: lane, PR: pr, State: p.states[pr], Text: releaseCardGate(text), Opened: true}
}

// take records that a card has been placed, so the next card of its lane is held behind it
// in this same tick and not only on the next one.
func (p *placement) take(lane, base string) {
	if lane != "" {
		p.live[lane] = base
	}
}

// release gives a lane back: a launcher that failed is not a card that ran, and a lane held
// by a card that never started is a lane nobody can use.
func (p *placement) release(lane, base string) {
	if lane != "" && p.live[lane] == base {
		delete(p.live, lane)
	}
}

// heldLine is the one line a card the lane rule holds is owed.
func heldLine(card string, v placeVerdict) string {
	return fmt.Sprintf("FILL HELD card=%s lane=%s live=%s",
		field(filepath.Base(card)), field(v.Lane), field(v.Holder))
}

// gatedLine is the one line a card the gate holds is owed, and it names the PR and what the
// forge said about it: "gated" with no number is a person opening every card to find out.
func gatedLine(prefix, card string, v placeVerdict) string {
	return fmt.Sprintf("%s GATED card=%s after=PR%d state=%s",
		prefix, field(filepath.Base(card)), v.PR, field(v.State))
}
