package sprint

import (
	"fmt"
	"strconv"
	"strings"
)

// A member is a fleet machine, and its width is the machine's child cap: the
// most work cards it holds at once, ready and working together (errata 3,
// amendment 9: "each fleet machine to have say, max width 64"). The width is a
// field of the member's control card, set by init --members <m>:<n> and by
// fleet up <m> --width <n>, and shown in the fleet table's width column. The
// deal fills every member up to its width in one step, round the fleet (T3,
// R6), and the level moves a card only to a member below its width (T4, R7).
// The model is tla/SprintEvents.tla: PlanDeal's room (its Cap), which counts
// the ready cards of a member; the width counts ready and working, and the
// bench run of the model at the width is owed (errata 3, amendment 9).
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

// memberWidths is each up member's width.
func memberWidths(s *Snapshot, up []string) map[string]int {
	w := map[string]int{}
	for _, m := range up {
		w[m] = s.Width(m)
	}
	return w
}

// widthRoom is the sum over the up members of the cards each can still take
// before it is at its width.
func widthRoom(s *Snapshot, up []string) int {
	room := 0
	for _, m := range up {
		room += max(0, s.Width(m)-s.Fleet.Count(m, Ready)-s.Fleet.Count(m, Working))
	}
	return room
}
