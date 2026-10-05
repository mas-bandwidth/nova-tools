package sprint

import (
	"fmt"
	"slices"
	"strconv"
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
	// PropFriendIdle is the work table's property: how long a friend holding cards may
	// show no session activity before it is an alarm, a duration.
	PropFriendIdle = "friend_idle"
	// PropReadTier is the work table's property: the sprint's read tier.
	PropReadTier = "read_tier"
	// FieldReadTier is a stream's control card's field: the stream's read tier,
	// over the sprint's.
	FieldReadTier = "read_tier"
	// FieldRelease is a stream's control card's field: the release the stream
	// belongs to (e.g. "v1.2.0"), set by stream set --release (docs/SPEC-SPRINT.md section 11).
	FieldRelease = "release"
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

// FriendIdleDefault is how long a friend holding cards may show no file write under her
// working directory and outbox before it is an alarm (nova-sprint set --friend-idle).
const FriendIdleDefault = 20 * time.Minute

// FriendIdleAfter is that bound: the sprint's setting, else FriendIdleDefault.
func (s *Snapshot) FriendIdleAfter() time.Duration {
	if s.Work != nil {
		if v, ok := s.Work.Prop(PropFriendIdle); ok {
			if d, err := time.ParseDuration(v); err == nil && d > 0 {
				return d
			}
		}
	}
	return FriendIdleDefault
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
var readTiers = []string{cardhdr.RouteFlash, cardhdr.RoutePro, cardhdr.RouteHeavy}

// stronger is the stronger of two read tiers.
func stronger(a, b string) string {
	if slices.Index(readTiers, b) > slices.Index(readTiers, a) {
		return b
	}
	return a
}

// SetReq is the coordinator's settings: with Streams, each stream's read tier,
// attempt cap, release, or protected-branch mark; without, the sprint's dealt
// bound, read tier and attempt cap (brief_bound.go, AttemptsCap). An empty value
// leaves that setting as it is; ReadTierDefault takes one off.
type SetReq struct {
	Streams    []string `json:",omitempty"`
	ReadTier   string   `json:",omitempty"`
	DealtMax   string   `json:",omitempty"`
	Attempts   string   `json:",omitempty"`
	FriendIdle string   `json:",omitempty"`
	// Reason, with Streams and ReadTier, is why the read tier is set, recorded on the
	// stream's control card (FieldReadTierReason); Answers the judgments this answers.
	Reason  string   `json:",omitempty"`
	Answers []string `json:",omitempty"`
	// LandProtected is the streams' mark: the repositories whose protected branches
	// they land on (FieldLandProtected, docs/SPEC-SPRINT.md section 7).
	LandProtected string `json:",omitempty"`
	Release       string `json:",omitempty"`
	// The backlog alarms' thresholds (alarms.go): a count, a percent, on, or off.
	AlarmReview  string `json:",omitempty"`
	AlarmMerging string `json:",omitempty"`
	AlarmFleet   string `json:",omitempty"`
	AlarmReady   string `json:",omitempty"`
	GoLanes      string `json:",omitempty"` // the Go lanes of every machine (PropGoLanes, section 18)
	Who          string
}

// Set writes the settings: refused whole, writing nothing, for an actor who is not
// the coordinator, a read tier that is not flash, pro or default, a dealt bound that
// is not a positive duration, a mark that names no repository or is not a stream's,
// an alarm's threshold it does not take, nothing to set, or a stream that is not a stream (docs/SPEC-SPRINT.md section 11).
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
	if r.LandProtected != "" {
		if w := landProtectedWhy(r.LandProtected); w != "" {
			why = append(why, w)
		}
		if len(r.Streams) == 0 {
			why = append(why, "--land-protected is a stream's, not the sprint's: nova-sprint stream set <stream> --land-protected "+r.LandProtected)
		}
	}
	if r.GoLanes != "" && r.GoLanes != ReadTierDefault {
		if n, err := strconv.Atoi(r.GoLanes); err != nil || n < 1 {
			why = append(why, "--go-lanes wants a whole number from 1, the Go lanes of every machine, or "+ReadTierDefault+" for "+strconv.Itoa(LaneWidthDefault)+"; found "+r.GoLanes)
		}
	}
	alarms := r.alarms()
	for _, a := range alarmProps {
		if v := alarms[a.prop]; v != "" && !alarmValid(a.prop, v) {
			why = append(why, a.flag+" wants "+a.wants+", or "+AlarmOff+" to take it off; found "+v)
		}
		if v := alarms[a.prop]; v != "" && len(r.Streams) > 0 {
			why = append(why, a.flag+" is the sprint's, not a stream's: nova-sprint set "+a.flag+" "+v)
		}
	}
	if r.Attempts != "" {
		if _, err := ParseAttempts(r.Attempts); err != nil {
			why = append(why, err.Error())
		}
	}
	if r.FriendIdle != "" && r.FriendIdle != ReadTierDefault {
		if d, err := time.ParseDuration(r.FriendIdle); err != nil || d <= 0 {
			why = append(why, "--friend-idle wants a duration above zero (20m, 1h), or "+ReadTierDefault+" for "+FriendIdleDefault.String()+"; found "+r.FriendIdle)
		}
	}
	if r.ReadTier == "" && r.DealtMax == "" && r.LandProtected == "" && r.Release == "" && len(alarms) == 0 && r.GoLanes == "" && r.Attempts == "" && r.FriendIdle == "" {
		why = append(why, "nothing to set: --read-tier, --dealt-max, --go-lanes, --attempts, --friend-idle or an --alarm-... threshold")
	}
	if len(r.Streams) > 0 && r.GoLanes != "" {
		why = append(why, "--go-lanes is the sprint's, not a stream's: nova-sprint set --go-lanes "+r.GoLanes)
	}
	if len(r.Streams) > 0 && r.DealtMax != "" {
		why = append(why, "--dealt-max is the sprint's, not a stream's: nova-sprint set --dealt-max "+r.DealtMax)
	}
	if len(r.Streams) == 0 && r.Release != "" {
		why = append(why, "--release is a stream's, not the sprint's: nova-sprint stream set <s> --release "+r.Release)
	}
	if len(r.Streams) > 0 && r.FriendIdle != "" {
		why = append(why, "--friend-idle is the sprint's, not a stream's: nova-sprint set --friend-idle "+r.FriendIdle)
	}
	for _, st := range r.Streams {
		if s.StreamCtl(st) == nil {
			why = append(why, "no stream "+st)
			continue
		}
		// the floor: a stream's read tier is never below its work tier (readtier.go)
		if work := StreamWorkTier(s, st); r.ReadTier != "" && r.ReadTier != ReadTierDefault && slices.Contains(readTiers, r.ReadTier) && stronger(work, r.ReadTier) != r.ReadTier {
			why = append(why, st+"'s work tier is "+work+": its read tier is never below it; run: nova-sprint stream set "+st+" --read-tier "+work)
		}
	}
	if len(why) > 0 {
		p.refuse("set", strings.Join(why, "; "))
		return p
	}
	if len(r.Streams) > 0 {
		for _, st := range r.Streams {
			ctl := s.StreamCtl(st)
			set, unset := map[string]string{}, []string{}
			var moved []string
			var closes []Open
			if r.ReadTier != "" {
				if r.ReadTier == ReadTierDefault || r.ReadTier == "none" {
					unset = append(unset, FieldReadTier, FieldReadTierReason)
					moved = append(moved, "read-tier the sprint's")
				} else {
					set[FieldReadTier] = r.ReadTier
					moved = append(moved, "read-tier "+r.ReadTier)
					if r.Reason != "" {
						set[FieldReadTierReason] = r.Reason
					}
					// a raise answers the stream's judgment that it should rise (readtier.go)
					for _, o := range s.Open {
						if o.Note.Type == NRaiseReadTier && o.Subject() == StreamSubject(st) {
							closes = append(closes, o)
						}
					}
				}
			}
			for _, f := range []struct{ field, v, word, off string }{
				{FieldLandProtected, r.LandProtected, "land-protected", "none"},
				{FieldRelease, r.Release, "release", "none"},
			} {
				switch f.v {
				case "":
				case ReadTierDefault, "none":
					unset, moved = append(unset, f.field), append(moved, f.word+" "+f.off)
				default:
					set[f.field], moved = f.v, append(moved, f.word+" "+f.v)
				}
			}
			if r.Attempts != "" {
				if r.Attempts == ReadTierDefault {
					unset = append(unset, FieldAttempts)
					moved = append(moved, "attempts the sprint's")
				} else {
					set[FieldAttempts] = r.Attempts
					moved = append(moved, "attempts "+r.Attempts)
				}
			}
			p.Units = append(p.Units, Unit{Key: ctl.ID, Stream: st, Changes: []Change{change(Merge, setEntry(ctl, set, unset...))}, Moved: "stream " + st + " " + strings.Join(moved, ", "), Closes: closes})
		}
		answered(&p, s, r.Answers, r.Who)
		return p
	}
	// a property is written with its word, default included: the readers take
	// default for none (DealtMax, readTierSetting)
	var moved []string
	kvs := [][2]string{{PropReadTier, r.ReadTier}, {PropDealtMax, r.DealtMax}, {PropGoLanes, r.GoLanes}, {PropAttempts, r.Attempts}, {PropFriendIdle, r.FriendIdle}}
	for _, a := range alarmProps {
		kvs = append(kvs, [2]string{a.prop, alarms[a.prop]})
	}
	for _, kv := range kvs {
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
	case name == PropGoLanes:
		return fmt.Sprintf("default (%d a machine)", LaneWidthDefault)
	case name == PropAttempts:
		return fmt.Sprintf("default (%d attempts on one brief)", AttemptsDefault)
	case name == PropFriendIdle:
		return fmt.Sprintf("default (%s)", FriendIdleDefault)
	}
	return "default (each card's own tier)"
}

// alarms is the backlog alarms' thresholds the request sets, by property; none unset.
func (r SetReq) alarms() map[string]string {
	out := map[string]string{}
	for prop, v := range map[string]string{PropAlarmReview: r.AlarmReview, PropAlarmMerging: r.AlarmMerging, PropAlarmFleet: r.AlarmFleet, PropAlarmReady: r.AlarmReady} {
		if v != "" {
			out[prop] = v
		}
	}
	return out
}
