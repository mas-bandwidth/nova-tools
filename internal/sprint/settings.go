package sprint

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// The sprint's settings, the coordinator's (`set`, `stream set`; nova-tools#5096
// items 22 and 27): the dealt bound, how long a work card may wait dealt and never
// taken before it is a judgment, and the read tier, the tier a primary's reads draw
// their route from when it is stronger than the card's own. The sprint's are the
// work table's properties, the stream's a field of its control card; a clear starts
// the next epoch with neither, as it starts every property.
const (
	// PropDealtMax is the work table's property: the dealt bound, a duration.
	PropDealtMax = "dealt_max"
	// PropReadTier is the work table's property: the sprint's read tier.
	PropReadTier = "read_tier"
	// FieldReadTier is a stream's control card's field: the stream's read tier,
	// over the sprint's.
	FieldReadTier = "read_tier"
	// ReadTierDefault is the word that takes a read tier off: a stream's back to
	// the sprint's, the sprint's back to each card's own tier.
	ReadTierDefault = "default"
)

// DealtMaxDefault is the dealt bound when the coordinator set none: three times
// the deadline a taken card is held to. A member holds at most DealAhead times its
// width, ready and working, so a card at the back of its ready queue is taken
// within two take deadlines of a member that works; the third is the margin.
const DealtMaxDefault = 3 * DeadlineUnfinished

// DealtMax is the dealt bound the tick holds a work card dealt and never taken to:
// the sprint's setting, else DealtMaxDefault.
func (s *Snapshot) DealtMax() time.Duration {
	if s.Work != nil {
		if v, ok := s.Work.Prop(PropDealtMax); ok {
			if d, err := time.ParseDuration(v); err == nil && d > 0 {
				return d
			}
		}
	}
	return DealtMaxDefault
}

// readTierSetting is the read tier set for the stream's reads: the stream's own,
// else the sprint's, else "" (each card's own tier).
func (s *Snapshot) readTierSetting(stream string) string {
	if s.Merge != nil {
		if t := s.StreamCtl(stream).F(FieldReadTier); t != "" {
			return t
		}
	}
	if s.Work != nil {
		if t, ok := s.Work.Prop(PropReadTier); ok && t != ReadTierDefault {
			return t
		}
	}
	return ""
}

// readTiers is the tiers a read tier may name, weakest first: a setting raises a
// card's reads to it and never lowers them (the coordinator, 2026-10-02: "A pro
// card's reads should run on a tier at least as strong as the writer's").
var readTiers = []string{cardhdr.RouteFlash, cardhdr.RoutePro}

// stronger is the stronger of two read tiers.
func stronger(a, b string) string {
	if slices.Index(readTiers, b) > slices.Index(readTiers, a) {
		return b
	}
	return a
}

// SetReq is the coordinator's settings: with Streams, each stream's read tier;
// without, the sprint's dealt bound and read tier. An empty value leaves that
// setting as it is; ReadTierDefault takes one off.
type SetReq struct {
	Streams  []string `json:",omitempty"`
	ReadTier string   `json:",omitempty"`
	DealtMax string   `json:",omitempty"`
	Who      string
}

// Set writes the settings: refused whole, writing nothing, for an actor who is not
// the coordinator, a read tier that is not flash, pro or default, a dealt bound that
// is not a positive duration, nothing to set, or a stream that is not a stream.
func Set(s *Snapshot, r SetReq) Plan {
	var p Plan
	var why []string
	if w := notCoordinator(s, r.Who, "set"); w != "" {
		why = append(why, strings.Replace(w, "answers a judgment, which is", "is", 1))
	}
	if r.ReadTier != "" && r.ReadTier != ReadTierDefault && !slices.Contains(readTiers, r.ReadTier) {
		why = append(why, "--read-tier wants "+strings.Join(readTiers, " or ")+", or "+ReadTierDefault+" to take it off; found "+r.ReadTier)
	}
	if r.DealtMax != "" && r.DealtMax != ReadTierDefault {
		if d, err := time.ParseDuration(r.DealtMax); err != nil || d <= 0 {
			why = append(why, "--dealt-max wants a duration above zero (6h, 90m), or "+ReadTierDefault+" for 3 times the take deadline; found "+r.DealtMax)
		}
	}
	if r.ReadTier == "" && r.DealtMax == "" {
		why = append(why, "nothing to set: --read-tier or --dealt-max")
	}
	if len(r.Streams) > 0 && r.DealtMax != "" {
		why = append(why, "--dealt-max is the sprint's, not a stream's: nova-sprint set --dealt-max "+r.DealtMax)
	}
	for _, st := range r.Streams {
		if s.StreamCtl(st) == nil {
			why = append(why, "no stream "+st)
		}
	}
	if len(why) > 0 {
		p.refuse("set", strings.Join(why, "; "))
		return p
	}
	if len(r.Streams) > 0 {
		for _, st := range r.Streams {
			ctl := s.StreamCtl(st)
			entry := setEntry(ctl, map[string]string{FieldReadTier: r.ReadTier})
			moved := "stream " + st + " read-tier " + r.ReadTier
			if r.ReadTier == ReadTierDefault {
				entry = setEntry(ctl, nil, FieldReadTier)
				moved = "stream " + st + " read-tier the sprint's"
			}
			p.Units = append(p.Units, Unit{Key: ctl.ID, Stream: st, Changes: []Change{change(Merge, entry)}, Moved: moved})
		}
		return p
	}
	// a property is written with its word, default included: the readers take
	// default for none (DealtMax, readTierSetting)
	var moved []string
	for _, kv := range [][2]string{{PropReadTier, r.ReadTier}, {PropDealtMax, r.DealtMax}} {
		if kv[1] == "" {
			continue
		}
		was, had := s.Work.Prop(kv[0])
		p.Props = append(p.Props, PropWrite{Table: Work, Name: kv[0], Value: kv[1], Was: was, WasAbsent: !had})
		moved = append(moved, strings.ReplaceAll(kv[0], "_", "-")+" "+orDefault(kv[1], kv[0]))
	}
	p.Units = append(p.Units, Unit{Key: "set", Moved: "sprint " + strings.Join(moved, ", ")})
	return p
}

// orDefault is a setting's value as the line says it: what default is.
func orDefault(v, name string) string {
	switch {
	case v != ReadTierDefault:
		return v
	case name == PropDealtMax:
		return fmt.Sprintf("default (%s, 3 times the take deadline)", DealtMaxDefault)
	}
	return "default (each card's own tier)"
}
