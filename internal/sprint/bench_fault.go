package sprint

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"
)

// A bench's fault on its fleet row (docs/SPEC-SPRINT.md section 7, the tree gate's fault;
// the-tree-gate-tells-a-bench-fault-from-a-red-test-bb). A gate that fails for its bench (its
// git, its disk, its temporary directory, its ssh, its toolchain) and not for its tree
// marks the bench on the fleet table for BenchFaultFor: the lander's ring skips it until
// then, and where shows it on the member's row (bench_fault, bench_fault_until). It ends by
// itself at its time: the ring and where read the clock, so nothing has to run for it to
// end. A mark is a property of the fleet table, one per member, as a quiet is
// (fleet_quiet.go), which where reads from the table's shape with no read of its own.

// BenchFaultPropPrefix is the fleet table's property of a member's bench fault:
// bench_fault_<member>.
const BenchFaultPropPrefix = "bench_fault_"

// BenchFaultFor is how long a faulted bench is skipped by the gate's ring.
const BenchFaultFor = 15 * time.Minute

// NBenchFault is the happened note of a bench marked faulted.
const NBenchFault = "a bench faulted"

// The where cells of a member's bench fault while it holds.
const (
	FieldBenchFault      = "bench_fault"
	FieldBenchFaultUntil = "bench_fault_until"
)

// BenchFault is one member's mark as its property holds it: the kind of fault, the line
// that said it, when it was marked and until when the ring skips it.
type BenchFault struct {
	Member string    `json:"-"`
	Kind   string    `json:"kind"`
	What   string    `json:"what"`
	At     time.Time `json:"at"`
	Until  time.Time `json:"until"`
}

// Active says the mark holds at now.
func (f BenchFault) Active(now time.Time) bool { return now.Before(f.Until) }

// Line is the mark as where says it under the fleet table.
func (f BenchFault) Line() string {
	return fmt.Sprintf("bench fault: %s %s until %s: %s", f.Member, f.Kind, stamp(f.Until), f.What)
}

func (f BenchFault) value() string {
	b, _ := json.Marshal(f) // ignored: a struct of strings and times always marshals
	return string(b)
}

// BenchFaults is every member's mark the fleet table's properties hold, by member name; a
// property that does not read is no mark.
func BenchFaults(props map[string]string) []BenchFault {
	var out []BenchFault
	for name, v := range props {
		m, ok := strings.CutPrefix(name, BenchFaultPropPrefix)
		if !ok {
			continue
		}
		var f BenchFault
		if json.Unmarshal([]byte(v), &f) != nil {
			continue
		}
		f.Member = m
		out = append(out, f)
	}
	slices.SortFunc(out, func(a, b BenchFault) int { return strings.Compare(a.Member, b.Member) })
	return out
}

// BenchFaultsNow is the marks that hold at now, by member.
func BenchFaultsNow(props map[string]string, now time.Time) map[string]BenchFault {
	out := map[string]BenchFault{}
	for _, f := range BenchFaults(props) {
		if f.Active(now) {
			out[f.Member] = f
		}
	}
	return out
}

// BenchFaultReq marks a member faulted: the kind and the line that said it, by Who.
type BenchFaultReq struct {
	Member string `json:"member"`
	Kind   string `json:"kind"`
	What   string `json:"what"`
	Who    string `json:"who,omitempty"`
}

// MarkBenchFault is the lander's mark of a faulted bench: its property set to the kind and
// the line, until BenchFaultFor from now, one property write guarded on the value read, and
// one happened note, the log's line of it. A name that is no fleet member is refused.
func MarkBenchFault(s *Snapshot, r BenchFaultReq) Plan {
	var p Plan
	if s.MemberCtl(r.Member) == nil || IsFriendRow(r.Member) {
		p.refuse(r.Member, "names no fleet member of this sprint: nova-sprint where --all shows them")
		return p
	}
	name := BenchFaultPropPrefix + r.Member
	was, had := s.Fleet.Prop(name)
	f := BenchFault{Member: r.Member, Kind: r.Kind, What: r.What, At: s.Now.UTC().Truncate(time.Second)}
	f.Until = f.At.Add(BenchFaultFor)
	n := happened(NBenchFault, "", s.Now)
	n.Who = r.Who
	n.What = fmt.Sprintf("%s bench fault %s until %s: %s; the gate's ring skips it until then", r.Member, r.Kind, stamp(f.Until), r.What)
	p.Props = append(p.Props, PropWrite{Table: Fleet, Name: name, Value: f.value(), Was: was, WasAbsent: !had})
	p.Units = append(p.Units, Unit{Key: CtlID(r.Member), Moved: n.What, Notes: []Note{n}})
	return p
}
