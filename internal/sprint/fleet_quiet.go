package sprint

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

// The coordinator's quiet on a machine (docs/SPEC-SPRINT.md section 5,
// fleet-quiet-machine-b.w7): fleet quiet <member> --for <duration> --reason <text> holds a
// fleet member out of the deal until a time, while the work dealt to it finishes, and every
// worker's view says so (a QUIET line: the machine, the end time and the reason; run no go
// build or test there). It ends by itself at the time: the deal reads the clock, so nothing
// has to run for it to end, and the tick's level writes the end to the log once; fleet quiet
// <member> --end ends it early. A quiet is a property of the fleet table, one per member,
// which a fleet step writes at once (a work-table property waits in the pump's queue), and
// the worker's view reads it from the table's shape, one read.

// QuietPropPrefix is the fleet table's property of a member's quiet: quiet_<member>.
const QuietPropPrefix = "quiet_"

// The happened notes of a quiet: begun by the coordinator, and ended, at its time or early.
const (
	NQuiet      = "a machine is quiet"
	NQuietEnded = "a machine's quiet ended"
)

// Quiet is one member's quiet as its property holds it: until when, why, who set it and
// when; Ended is when it ended (its time, or --end) and the log has said so.
type Quiet struct {
	Member  string    `json:"-"`
	Until   time.Time `json:"until"`
	Reason  string    `json:"reason"`
	By      string    `json:"by,omitempty"`
	At      time.Time `json:"at"`
	Ended   time.Time `json:"ended,omitzero"`
	EndedBy string    `json:"ended_by,omitempty"`
}

// Active says the quiet holds the member out of the deal at now: not ended, and before its
// time.
func (q Quiet) Active(now time.Time) bool { return q.Ended.IsZero() && now.Before(q.Until) }

// Line is the QUIET line every worker's view carries while the quiet holds.
func (q Quiet) Line() string {
	return fmt.Sprintf("QUIET %s until %s: %s; run no go build or test there", q.Member, stamp(q.Until), q.Reason)
}

func (q Quiet) value() string {
	b, _ := json.Marshal(q) // ignored: a struct of strings and times always marshals
	return string(b)
}

// Quiets is every member's quiet the fleet table's properties hold, by member name; a
// property that does not read is no quiet.
func Quiets(props map[string]string) []Quiet {
	var out []Quiet
	for name, v := range props {
		m, ok := strings.CutPrefix(name, QuietPropPrefix)
		if !ok {
			continue
		}
		var q Quiet
		if json.Unmarshal([]byte(v), &q) != nil {
			continue
		}
		q.Member = m
		out = append(out, q)
	}
	slices.SortFunc(out, func(a, b Quiet) int { return strings.Compare(a.Member, b.Member) })
	return out
}

// QuietLines is the QUIET line of every quiet in force at now: what view worker shows every
// member and friend.
func QuietLines(props map[string]string, now time.Time) []string {
	var out []string
	for _, q := range Quiets(props) {
		if q.Active(now) {
			out = append(out, q.Line())
		}
	}
	return out
}

// quietNow is the members quiet at the snapshot's clock.
func quietNow(s *Snapshot) map[string]Quiet {
	out := map[string]Quiet{}
	if s.Fleet == nil {
		return out
	}
	for _, q := range Quiets(s.Fleet.Props()) {
		if q.Active(s.Now) {
			out[q.Member] = q
		}
	}
	return out
}

// notQuiet is up without the members quiet now and the members whose fresh beat says they
// start no card (Snapshot.NoRoom: free disk under the floor): the deal and the level give
// such a member nothing (docs/SPEC-SPRINT.md section 5, fleet-quiet-machine-b.w7), so a card
// is not dealt to a machine that hands it straight back refused at staging.
func notQuiet(s *Snapshot, up []string) []string {
	quiet := quietNow(s)
	if len(quiet) == 0 && len(s.NoRoom) == 0 {
		return up
	}
	var out []string
	for _, m := range up {
		if _, ok := quiet[m]; ok {
			continue
		}
		if s.NoRoom[m] != "" {
			continue
		}
		out = append(out, m)
	}
	return out
}

// quietWhy is the members of up quiet now, and those whose beat says they start no card
// with their word why, for a refusal or a hold: "" when none is.
func quietWhy(s *Snapshot, up []string) string {
	quiet := quietNow(s)
	var out []string
	for _, m := range up {
		if q, ok := quiet[m]; ok {
			out = append(out, m+" until "+stamp(q.Until))
		}
	}
	var full []string
	for _, m := range up {
		if _, ok := quiet[m]; !ok && s.NoRoom[m] != "" {
			full = append(full, m+" ("+s.NoRoom[m]+")")
		}
	}
	var says []string
	if len(out) > 0 {
		says = append(says, "quiet: "+strings.Join(out, ", "))
	}
	if len(full) > 0 {
		says = append(says, "starts no card: "+strings.Join(full, ", "))
	}
	return strings.Join(says, "; ")
}

// quietPlan is fleet quiet: the member's quiet set until r.Until with r.Reason, or with
// r.End its quiet ended now; one property write guarded on the value read, and one happened
// note, the log's line of it.
func quietPlan(s *Snapshot, r FleetReq) Plan {
	var p Plan
	if s.MemberCtl(r.Member) == nil || IsFriendRow(r.Member) {
		p.refuse(r.Member, "names no fleet member of this sprint: nova-sprint where --all shows them")
		return p
	}
	name := QuietPropPrefix + r.Member
	was, had := s.Fleet.Prop(name)
	var q Quiet
	if had && json.Unmarshal([]byte(was), &q) != nil {
		q = Quiet{}
	}
	q.Member = r.Member
	var n Note
	if r.End {
		if !q.Active(s.Now) {
			p.refuse(r.Member, r.Member+" is not quiet: nothing to end; run: nova-sprint fleet quiet "+r.Member+" --for <duration> --reason <text>")
			return p
		}
		q.Ended, q.EndedBy = s.Now, r.Who
		n = happened(NQuietEnded, "", s.Now)
		n.What = fmt.Sprintf("%s quiet ended early by %s (it ran until %s): %s", r.Member, r.Who, stamp(q.Until), q.Reason)
	} else {
		var why []string
		if strings.TrimSpace(r.Reason) == "" {
			why = append(why, "--reason wants the reason every worker reads, e.g. 'load 64: the macOS CI legs time out'")
		}
		if !r.Until.After(s.Now) {
			why = append(why, "the quiet's end wants a time after now ("+stamp(s.Now)+"): --for <duration> or --until <RFC3339>")
		}
		if len(why) > 0 {
			p.refuse(r.Member, strings.Join(why, "; "))
			return p
		}
		q = Quiet{Member: r.Member, Until: r.Until.UTC().Truncate(time.Second), Reason: r.Reason, By: r.Who, At: s.Now.UTC().Truncate(time.Second)}
		n = happened(NQuiet, "", s.Now)
		n.What = fmt.Sprintf("%s quiet until %s: %s; no card is dealt to it, its dealt work finishes; run no go build or test there", r.Member, stamp(q.Until), q.Reason)
	}
	n.Who = r.Who
	p.Props = append(p.Props, PropWrite{Table: Fleet, Name: name, Value: q.value(), Was: was, WasAbsent: !had})
	p.Units = append(p.Units, Unit{Key: CtlID(r.Member), Moved: n.What, Notes: []Note{n}})
	return p
}

// quietEnds is the tick's end of every quiet past its time (the level's, once a tick): each
// marked ended at its time, and one happened note, the log's line. The deal ends a quiet at
// its time whether or not this has run; this writes that it did.
func quietEnds(s *Snapshot, p *Plan) {
	for _, q := range Quiets(s.Fleet.Props()) {
		if !q.Ended.IsZero() || s.Now.Before(q.Until) {
			continue
		}
		name := QuietPropPrefix + q.Member
		was, _ := s.Fleet.Prop(name)
		q.Ended = q.Until
		n := happened(NQuietEnded, "", s.Now)
		n.Who = MachineActor
		n.What = fmt.Sprintf("%s quiet ended at its time %s: %s; the deal gives it cards again", q.Member, stamp(q.Until), q.Reason)
		p.Props = append(p.Props, PropWrite{Table: Fleet, Name: name, Value: q.value(), Was: was})
		p.Units = append(p.Units, Unit{Key: CtlID(q.Member), Moved: n.What, Notes: []Note{n}})
	}
}
