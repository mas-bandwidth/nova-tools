package sprint

import (
	"fmt"
	"math/big"
	"slices"
	"time"

	"github.com/mas-bandwidth/nova-tools/internal/cardcost"
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

// TidyAgainAfter is how soon a tidy may follow the last: a second within it is refused.
const TidyAgainAfter = time.Minute

// TidyRow is one fleet row's done cells as a tidy found them: its ok and failed counts
// before (the counters the archive keeps), the cards it takes off, and the cards it
// leaves because the work still reads them.
type TidyRow struct {
	Row    string   `json:"row"`
	OK     int      `json:"ok"`
	Failed int      `json:"failed"`
	Moved  []string `json:"moved,omitempty"`
	Kept   []string `json:"kept,omitempty"`
}

// TidyKeeps says a finished work card stays on its done cell through a tidy, and why:
// the work card of a primary on the work table not landed (its review, its reads, its
// merge and its next attempt's base read every attempt of it), or a card finished within
// the friend-finish window (the idle rule, the overload window and the provider's return
// read the newest finishes). Every other finished card is history only, which its record
// keeps.
func TidyKeeps(s *Snapshot, wc *Card) (bool, string) {
	if pr := s.Work.Placed(wc.F(PrimaryField)); pr != nil && pr.Col != Landed {
		return true, "its primary " + pr.ID + " is " + pr.Col
	}
	if t := stampAt(wc, "finished"); !t.IsZero() && s.Now.Sub(t) < s.FriendFinishAfter() {
		return true, "finished within " + s.FriendFinishAfter().String()
	}
	return false, ""
}

// TidyDone is the tidy of the done cells of the fleet rows its kinds name (friends: the
// friends' rows, FriendRow; fleet: the machines'): each row with a finished card, in row
// order, and the plan that takes the history-only cards off (TidyKeeps), one unit each,
// their records kept.
func TidyDone(s *Snapshot, kinds []string) ([]TidyRow, Plan) {
	var rows []TidyRow
	var p Plan
	if s.Fleet == nil {
		return nil, p
	}
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
			if keep, _ := TidyKeeps(s, wc); keep {
				r.Kept = append(r.Kept, wc.ID)
				continue
			}
			r.Moved = append(r.Moved, wc.ID)
			p.Units = append(p.Units, Unit{Key: wc.ID, Changes: []Change{change(Fleet, removeEntry(wc, nil))},
				Moved: fmt.Sprintf("%s done %s -> off the table (stats tidy: its record kept)", wc.ID, doneWord(wc.Col))})
		}
		rows = append(rows, r)
	}
	return rows, p
}

// doneWord is a done column as the log says it.
func doneWord(col string) string {
	if col == DoneFailed {
		return "failed"
	}
	return "ok"
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
