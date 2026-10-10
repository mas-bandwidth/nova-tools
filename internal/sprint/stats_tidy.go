package sprint

import (
	"cmp"
	"fmt"
	"math/big"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/cardcost"
)

// Stats tidy (`nova-sprint stats tidy`; docs/SPEC-SPRINT.md section 11, Statistics): the
// owner, 2026-10-06, "can you please clear the sets of done consumer cards for all friends
// and fleet" and "I would like a semi-fresh start to stats now", renamed the same day
// ("reset sounds too aggressive"). A tidy starts the statistics afresh and keeps the work:
// the finished work cards that are history only leave the done cells of the rows it names
// (their records stay, read by id), the routes' window and the streams' costs count from
// it, and what moved is kept in a dated archive. Pure: the snapshot in, the plan out.

// The kinds a tidy names (--friends, --fleet, --routes, --streams; --all is the four).
const (
	TidyFriends = "friends"
	TidyFleet   = "fleet"
	TidyRoutes  = "routes"
	TidyStreams = "streams"
)

// TidyKinds is every kind, in the order the verb names them.
var TidyKinds = []string{TidyFriends, TidyFleet, TidyRoutes, TidyStreams}

// TidyAgainAfter is how soon a tidy may follow the last: a second within it, by the
// recorded time to the nanosecond, is refused.
const TidyAgainAfter = time.Minute

// TidyViewWindow is the coordinator view's recent window (cmd/nova-sprint view.go,
// viewWindow): its finished-in-the-last-30m counts read the newest finishes.
const TidyViewWindow = 30 * time.Minute

// TidyKeepWindow is how recent a finish a tidy keeps, in running time: the largest of
// the windows that read the newest finishes, the friend-finish window (the idle rule),
// OverloadWindow and TidyViewWindow.
func TidyKeepWindow(s *Snapshot) time.Duration {
	return max(s.FriendFinishAfter(), OverloadWindow, TidyViewWindow)
}

// TidyCard is one finished card a tidy took off, or kept: its id, the cell it was on
// (ok or failed) and, for one kept, why.
type TidyCard struct {
	ID   string `json:"id"`
	Cell string `json:"cell"`
	Why  string `json:"why,omitempty"`
	// Finished is the card's finish stamp: a stats reset's mark is rebased by the moved
	// cards that finished before it alone (ResetMark.Rebase).
	Finished time.Time `json:"finished,omitzero"`
}

// TidyRow is one fleet row's done cells as a tidy found them: its ok and failed counts
// before (the counters the archive keeps), the cards it takes off, and the cards it
// leaves because the work or a rule still reads them.
type TidyRow struct {
	Row    string     `json:"row"`
	OK     int        `json:"ok"`
	Failed int        `json:"failed"`
	Moved  []TidyCard `json:"moved,omitempty"`
	Kept   []TidyCard `json:"kept,omitempty"`
}

// TidyKept is the finished cards a tidy leaves on their done cells, by id, with why; every
// other finished card is history only, which its record keeps:
//   - the work card of a primary on the work table not landed (its review, its reads, its
//     merge and its next attempt's base read every attempt of it);
//   - a card finished within TidyKeepWindow of running time (stopped, the time the machine
//     was STOPPED between two clock readings, as the idle rule measures it; nil is none):
//     the idle rule, the overload window and the coordinator view read the newest finishes;
//   - each row's newest RouteRestWindow finished cards, and the cards holding each route's
//     newest RouteRestWindow ended takes, whatever their age: the rest rule's sample;
//   - each provider's newest ok finish, whatever its age: a provider's return (providerBack)
//     reads whether an ok card of the provider finished after a failure.
func TidyKept(s *Snapshot, stopped func(from, to time.Time) time.Duration) map[string]string {
	kept := map[string]string{}
	keep := func(id, why string) {
		if _, ok := kept[id]; !ok {
			kept[id] = why
		}
	}
	if s.Fleet == nil {
		return kept
	}
	window := TidyKeepWindow(s)
	newestOK := map[string]*Card{}
	for _, row := range s.Fleet.Rows() {
		done := append(slices.Clone(s.Fleet.Cell(row, DoneOK)), s.Fleet.Cell(row, DoneFailed)...)
		for _, wc := range done {
			if pr := s.Work.Placed(wc.F(PrimaryField)); pr != nil && pr.Col != Landed {
				keep(wc.ID, "its primary "+pr.ID+" is "+pr.Col)
			}
			if t := stampAt(wc, "finished"); !t.IsZero() {
				d := s.Now.Sub(t)
				if stopped != nil {
					d -= stopped(t, s.Now)
				}
				if d < window {
					keep(wc.ID, "finished within "+window.String()+" of running time")
				}
			}
			if wc.Col == DoneOK {
				p := providerOf(wc.F(FieldModel))
				if n := newestOK[p]; n == nil || wc.F("finished") > n.F("finished") {
					newestOK[p] = wc
				}
			}
		}
		slices.SortStableFunc(done, func(a, b *Card) int { return strings.Compare(b.F("finished"), a.F("finished")) })
		for _, wc := range done[:min(len(done), RouteRestWindow)] {
			keep(wc.ID, fmt.Sprintf("one of the row's newest %d finishes (the rest rule's sample)", RouteRestWindow))
		}
	}
	for p, wc := range newestOK {
		keep(wc.ID, "the newest ok finish of provider "+cmp.Or(p, "-")+" (a provider's return reads it)")
	}
	for route, ends := range routeEnds(s.Fleet) {
		slices.SortStableFunc(ends, func(a, b routeEnd) int { return b.at.Compare(a.at) })
		for _, e := range ends[:min(len(ends), RouteRestWindow)] {
			if c := s.Fleet.Card(e.card); c != nil && (c.Col == DoneOK || c.Col == DoneFailed) {
				keep(e.card, fmt.Sprintf("holds one of route %s's newest %d ended takes (the rest rule's sample)", route, RouteRestWindow))
			}
		}
	}
	return kept
}

// TidyDone is the tidy of the done cells of the fleet rows its kinds name (friends: the
// friends' rows, FriendRow; fleet: the machines'): each row with a finished card, in row
// order, and the plan that takes the history-only cards off (TidyKept), one unit each,
// their records kept, with each tidied row's median run wall carried (CarriedMedians).
func TidyDone(s *Snapshot, kinds []string, stopped func(from, to time.Time) time.Duration) ([]TidyRow, Plan) {
	var rows []TidyRow
	var p Plan
	if s.Fleet == nil {
		return nil, p
	}
	kept := TidyKept(s, stopped)
	for _, row := range s.Fleet.Rows() {
		if IsFriendRow(row) && !slices.Contains(kinds, TidyFriends) || !IsFriendRow(row) && !slices.Contains(kinds, TidyFleet) {
			continue
		}
		ok, failed := s.Fleet.Cell(row, DoneOK), s.Fleet.Cell(row, DoneFailed)
		if len(ok)+len(failed) == 0 {
			continue
		}
		r := TidyRow{Row: row, OK: len(ok), Failed: len(failed)}
		for _, wc := range append(slices.Clone(ok), failed...) {
			cell := doneWord(wc.Col)
			if why, keep := kept[wc.ID]; keep {
				r.Kept = append(r.Kept, TidyCard{ID: wc.ID, Cell: cell, Why: why})
				continue
			}
			r.Moved = append(r.Moved, TidyCard{ID: wc.ID, Cell: cell, Finished: stampAt(wc, "finished")})
			p.Units = append(p.Units, Unit{Key: wc.ID, Changes: []Change{change(Fleet, removeEntry(wc, nil))},
				Moved: fmt.Sprintf("%s done %s -> off the table (stats tidy: its record kept)", wc.ID, cell)})
		}
		if len(r.Moved) > 0 {
			p.Props = append(p.Props, carryMedian(s, row)...)
		}
		rows = append(rows, r)
	}
	return rows, p
}

// doneWord is a done column as the log and the archive say it.
func doneWord(col string) string {
	if col == DoneFailed {
		return "failed"
	}
	return "ok"
}

// PropCarriedMedian is the fleet table's property holding a row's median run wall as a
// tidy found it: "<seconds> <samples>". The deadline rules (MemberMedianWall,
// FriendMedianWall) use it while the row's live ok sample is smaller than its count, and
// drop it once the live sample is at least as large, so a tidy never changes a deadline.
func PropCarriedMedian(row string) string { return "carried_median_" + row }

// carryMedian is the property write that carries the row's median run wall through a tidy,
// none when the row has no sample or carries the same already.
func carryMedian(s *Snapshot, row string) []PropWrite {
	var median float64
	var n int
	if f, ok := FriendOfRow(row); ok {
		median, n = FriendMedianWall(s, f)
	} else {
		median, n = MemberMedianWall(s, row)
	}
	if n == 0 {
		return nil
	}
	v := strconv.FormatFloat(median, 'f', -1, 64) + " " + strconv.Itoa(n)
	was, had := s.Fleet.Prop(PropCarriedMedian(row))
	if had && was == v {
		return nil
	}
	return []PropWrite{{Table: Fleet, Name: PropCarriedMedian(row), Value: v, Was: was, WasAbsent: !had}}
}

// carriedMedian is the row's carried median run wall and its sample count; ok false when
// none is carried or it does not read.
func carriedMedian(s *Snapshot, row string) (median float64, n int, ok bool) {
	if s.Fleet == nil {
		return 0, 0, false
	}
	v, had := s.Fleet.Prop(PropCarriedMedian(row))
	if !had {
		return 0, 0, false
	}
	m, c, found := strings.Cut(v, " ")
	if !found {
		return 0, 0, false
	}
	median, err := strconv.ParseFloat(m, 64)
	n, err2 := strconv.Atoi(c)
	if err != nil || err2 != nil || n <= 0 {
		return 0, 0, false
	}
	return median, n, true
}

// withCarried is the live median and count, or the row's carried ones while the live
// sample is smaller than the carried count.
func withCarried(s *Snapshot, row string, median float64, n int) (float64, int) {
	if cm, cn, ok := carriedMedian(s, row); ok && n < cn {
		return cm, cn
	}
	return median, n
}

// StreamBase is a stream's landed cost and count at a tidy: the cost cell and per landed
// count from it (StreamCostSince, PerLandedSince).
type StreamBase struct {
	Cost   string `json:"cost,omitempty"` // the control card's exact landed cost (FieldCost), "" for none
	Landed int    `json:"landed"`
}

// StreamBases is every stream's base, from its control card and its landed cell.
func StreamBases(s *Snapshot) map[string]StreamBase {
	out := map[string]StreamBase{}
	for _, row := range s.Work.Rows() {
		b := StreamBase{Landed: s.Work.Count(row, Landed)}
		if ctl := s.StreamCtl(row); ctl != nil {
			b.Cost = ctl.F(FieldCost)
		}
		out[row] = b
	}
	return out
}

// StreamCostSince is a stream's landed cost since its tidy: the control card's exact
// cost less the base's, "" when that is nothing (the cell's "-"); the cost unchanged when
// the base names none or either does not read.
func StreamCostSince(cost string, base StreamBase) string {
	if base.Cost == "" || cost == "" {
		return cost
	}
	now, err := amountOf(cost)
	was, err2 := amountOf(base.Cost)
	if err != nil || err2 != nil || now == nil || was == nil {
		return cost
	}
	d := new(big.Rat).Sub(now, was)
	if d.Sign() <= 0 {
		return "" // nothing landed priced since: the cell's "-"
	}
	return d.FloatString(6)
}

// PerLandedSince is a stream's dollars per landed card since its tidy: the cost since
// over the cards landed since; "-" with none landed or nothing priced.
func PerLandedSince(cost string, landed int, base StreamBase) string {
	n := landed - base.Landed
	since := StreamCostSince(cost, base)
	if n <= 0 || since == "" {
		return "-"
	}
	r, err := amountOf(since)
	if err != nil || r == nil {
		return "-"
	}
	return cardcost.Cents(r.Quo(r, big.NewRat(int64(n), 1)))
}
