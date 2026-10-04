package sprint

import (
	"fmt"
	"strconv"
	"strings"
)

// A member is a fleet machine, and its width is the machine's child cap: the
// most work cards it runs at once. It holds up to DealAhead times its width,
// ready and working together: its width working and as many again ready
// behind them, so the sprint feeds the fleet at width and keeps it working at
// that width until done. The width is a field of the member's control card,
// set by init --members <m>:<n> and by fleet up <m> --width <n>, and shown in
// the fleet table's width column. The deal fills every member up to DealAhead
// times its width in one step, round the fleet (T3, R6), and the level, once
// at the start of every tick, moves ready cards from a member that cannot
// start them to one with free lanes, never past DealAhead times its width
// (T4, R7). The model is tla/SprintEvents.tla: PlanDeal's room (its Cap),
// which counts the ready cards of a member; the room here counts ready and
// working.
const (
	// FieldWidth is the control card's field, and the fleet table's column,
	// holding the member's width.
	FieldWidth = "width"
	// DefaultWidth is the width of a member whose control card names none.
	DefaultWidth = 64
	// MaxWidth is the widest a member may be: a width over it is refused
	// before anything is written.
	MaxWidth = 1024
	// TickMaxDeal is the most cards one deal moves (T3, R6). The deal is one
	// plan; the store's apply cuts it into parts under the table layer's
	// entry bounds, so the deal refuses nothing by size under this.
	TickMaxDeal = 2000
)

// ParseWidth is a width as the verbs take it: a whole number from 1 to
// MaxWidth.
func ParseWidth(text string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil || n < 1 || n > MaxWidth {
		return 0, fmt.Errorf("a width wants a whole number from 1 to %d, got %q", MaxWidth, text)
	}
	return n, nil
}

// MemberWidth is the width a member's control card names: DefaultWidth when
// it names none or one that is not a width.
func MemberWidth(ctl *Card) int {
	if n, err := ParseWidth(ctl.F(FieldWidth)); err == nil {
		return n
	}
	return DefaultWidth
}

// WidthText is the fleet table's width cell of a member's control card.
func WidthText(ctl *Card) string { return strconv.Itoa(MemberWidth(ctl)) }

// Width is the member's width.
func (s *Snapshot) Width(member string) int { return MemberWidth(s.MemberCtl(member)) }

// MemberSpec is a member as init --members names it: <name>[:<width>], a
// bare name keeping the default (Width 0).
type MemberSpec struct {
	Name  string
	Width int
}

// ParseMembers is init --members: a comma list of <name>[:<width>].
func ParseMembers(list string) ([]MemberSpec, error) {
	var out []MemberSpec
	for _, x := range Split(list) {
		name, w, hasWidth := strings.Cut(x, ":")
		spec := MemberSpec{Name: name}
		if !ValidID(name) {
			return nil, fmt.Errorf("a member name wants letters, digits, _ and -: %q", x)
		}
		if hasWidth {
			n, err := ParseWidth(w)
			if err != nil {
				return nil, fmt.Errorf("%s: %v", name, err)
			}
			spec.Width = n
		}
		out = append(out, spec)
	}
	return out, nil
}

// memberLoads is each up member's work cards held: its ready and its working
// cells, the count its width bounds.
func memberLoads(s *Snapshot, up []string) map[string]int {
	q := map[string]int{}
	for _, m := range up {
		q[m] = s.Fleet.Count(m, Ready) + s.Fleet.Count(m, Working)
	}
	return q
}

// DealAhead is how many widths of cards a member may hold, ready and working
// together: its width working and as many again ready behind them, so a lane
// that frees takes its next card at once and never waits for a tick. The
// member itself runs at most its width (the fleet row's, internal/member);
// the rest wait in its ready column.
const DealAhead = 2

// memberWidths is each up member's room for every placement (the deal, a
// redeal, the level, a down member's cards): DealAhead times its width.
func memberWidths(s *Snapshot, up []string) map[string]int {
	w := map[string]int{}
	for _, m := range up {
		w[m] = DealAhead * s.Width(m)
	}
	return w
}

// widthRoom is the sum over the up members of the cards each can still take
// before it holds DealAhead times its width.
func widthRoom(s *Snapshot, up []string) int {
	room := 0
	for _, m := range up {
		room += max(0, DealAhead*s.Width(m)-s.Fleet.Count(m, Ready)-s.Fleet.Count(m, Working))
	}
	return room
}

// WidthOfCores is the default width of a machine with these logical cores, half
// of them (the owner, 2026-10-02: "default is CPUs/2"), at least 1 and at most
// MaxWidth; 0 when the cores are not known (no beat has reported them). It is
// what fleet sync writes for a machine row with no width (config.MachineWidth,
// Default).
func WidthOfCores(cores int) int {
	if cores <= 0 {
		return 0
	}
	return min(MaxWidth, max(1, cores/2))
}
