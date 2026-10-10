package sprint

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/cardcost"
)

// A card's deadline by machine (docs/SPEC-SPRINT.md section 5, the deadline; the owner,
// 2026-10-04). The route's deadline was one number for the fleet, and a machine whose
// median run wall was twice the others' (759 s against 380 to 435 s) timed out twice as
// often, every timeout a whole attempt's spend lost. The deadline a dealt card
// gets is the larger of the card's own (its route's, or its pin's) and DeadlineK times
// the member's median run wall over its last DeadlineSamples ok attempts, as the stats
// verb measures it (stats.go); the fleet row may pin it (fleet up <m> --deadline <d>,
// the control card's deadline), and then the pin is the deadline whatever the card's.

const (
	// DeadlineK is the multiple of the member's median run wall a card's deadline is at
	// least.
	DeadlineK = 3
	// DeadlineSamples is how many of the member's latest ok attempts the median is over.
	DeadlineSamples = 50
	// FieldMemberDeadline is the member's control card field holding its pinned deadline
	// in seconds (fleet up --deadline), absent when none is pinned.
	FieldMemberDeadline = "deadline"
	// FieldOwnDeadline is the work card's own deadline in seconds, the route's or the
	// pin's, kept beside `deadline` (the member's) so a card dealt again to another
	// member (a member down, the level) gets that member's from the same start.
	FieldOwnDeadline = "deadline_own"
)

// MemberMedianWall is the member's median run wall in seconds over its last
// DeadlineSamples ok attempts (the usage walls of its ok work cards, newest finished
// first), and how many samples it is over; 0 and 0 with none.
//
// The deal asks it for every card it deals or moves, and a plan is run more than once a
// tick (the part's probe, then each attempt), so it is measured once a member for the
// done-ok cell the fleet table holds (medianWalls), never once a card: a deal costs the
// cards it deals, not those times the member's history
// (TestTickDealMeasuresEachDoneOKCellOnce).
//
// A stats tidy carries the member's median through it (PropCarriedMedian): the carried
// median and count stand while the live sample is smaller than the carried count.
func MemberMedianWall(s *Snapshot, member string) (median float64, n int) {
	if s.Fleet == nil {
		return 0, 0
	}
	if cell := s.Fleet.Cell(member, DoneOK); len(cell) > 0 {
		median, n = medianWalls.of(member, cell)
	}
	return withCarried(s, member, median, n)
}

// medianWalls is each member's median run wall as last measured, with the done-ok cell
// it was measured over.
var medianWalls = medianMemo{byMember: map[string]medianWall{}, measured: map[string]int{}}

// medianMemo is the members' median run walls, each held with the done-ok cell it was
// measured over: the table builds a new cell when a card is put (Table.Put resets the
// index), so the same cell, its first card's slot and its length, is the same cards,
// and another is measured again. One entry a member, the latest cell's; safe for the
// store's parts on their own goroutines.
type medianMemo struct {
	mu       sync.Mutex
	byMember map[string]medianWall
	measured map[string]int // the cells measured, a member
}

// medianWall is a member's median run wall over a done-ok cell, and how many samples it
// is over.
type medianWall struct {
	first  **Card
	len    int
	median float64
	n      int
}

// of is the member's median run wall over the cell (not empty), measured when the
// memo holds another cell's.
func (m *medianMemo) of(member string, cell []*Card) (float64, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if w, ok := m.byMember[member]; ok && w.first == &cell[0] && w.len == len(cell) {
		return w.median, w.n
	}
	median, n := cellMedianWall(cell)
	m.measured[member]++
	m.byMember[member] = medianWall{first: &cell[0], len: len(cell), median: median, n: n}
	return median, n
}

// cellMedianWall is MemberMedianWall measured over the member's done-ok cell.
func cellMedianWall(cell []*Card) (median float64, n int) {
	cards := append([]*Card(nil), cell...)
	sort.SliceStable(cards, func(i, j int) bool { return cards[i].F("finished") > cards[j].F("finished") })
	var walls []float64
	for _, c := range cards {
		if w, ok := wallSeconds(cardcost.ParseUsage(c.F(FieldUsage)).Wall); ok {
			walls = append(walls, w)
		}
		if len(walls) == DeadlineSamples {
			break
		}
	}
	m := measure(walls)
	return m.Median, m.N
}

// Deadline is the unified deadline function for both members and friends (docs/SPEC-SPRINT.md
// section 5, the deadline). It returns the larger of own and DeadlineK times the median
// run wall over the last n ok attempts, when n > 0; otherwise it returns own.
func Deadline(median float64, n int, own int) int {
	if n == 0 {
		return own
	}
	return max(own, int(math.Ceil(DeadlineK*median)))
}

// memberDeadline is the deadline in seconds a card dealt to the member gets, from the
// card's own (the route's or the pin's): the member's pinned deadline when it has one,
// else the larger of the card's and DeadlineK times the member's median run wall.
func (s *Snapshot) memberDeadline(member string, own int) int {
	if ctl := s.MemberCtl(member); ctl != nil && ctl.Int(FieldMemberDeadline) > 0 {
		return ctl.Int(FieldMemberDeadline)
	}
	median, n := MemberMedianWall(s, member)
	return Deadline(median, n, own)
}

// dealDeadline sets the work card's deadline field for a deal to the member
// (memberDeadline) when the card carries one, keeping the card's own
// (FieldOwnDeadline) for a later move to another member (movedDeadline).
func (s *Snapshot) dealDeadline(member string, work map[string]string) {
	own, err := strconv.Atoi(work[FieldDeadline])
	if err != nil || own <= 0 {
		return
	}
	work[FieldOwnDeadline] = work[FieldDeadline]
	work[FieldDeadline] = strconv.Itoa(s.memberDeadline(member, own))
}

// movedDeadline sets the deadline of a work card moved to another member (a member
// down, the level: nextGen) from the card's own (memberDeadline); a card that carries
// none keeps what it has.
func (s *Snapshot) movedDeadline(member string, c *Card, set map[string]string) {
	if own := c.Int(FieldOwnDeadline); own > 0 {
		set[FieldDeadline] = strconv.Itoa(s.memberDeadline(member, own))
	}
}

// ParseDeadline is a pinned deadline as fleet up --deadline takes it: a duration above
// zero (45m, 2700s), in whole seconds, or ReadTierDefault to take the pin off (off).
func ParseDeadline(text string) (seconds int, off bool, err error) {
	if strings.TrimSpace(text) == ReadTierDefault {
		return 0, true, nil
	}
	d, err := time.ParseDuration(strings.TrimSpace(text))
	if err != nil || d <= 0 || d > 24*time.Hour {
		return 0, false, fmt.Errorf("--deadline wants a duration above zero up to 24h (45m, 2700s), or %s to take the pin off; found %q", ReadTierDefault, text)
	}
	return int(math.Ceil(d.Seconds())), false, nil
}
