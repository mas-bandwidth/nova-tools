package sprint

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"time"

	"github.com/mas-bandwidth/nova-tools/pkg/cardhdr"
)

// A lane's wall time is capped by its card's tier (a-lane-is-capped-by-its-tier.w1;
// pkg/friend lane_cap.go): a friend's lane whose card reached its tier's cap is ended
// by her daemon, which finishes the card failed with a HOLD that names `capped at <cap>
// (tier <t>, overrun <d>)`. Such a finish is no failure yet: the first time a primary is
// capped it goes back to ready on the next tier up (capLadder), its failed count untouched,
// and the tick's deal cuts its next attempt there; a second cap, or a cap with no tier
// above it, is failed work as any other. Either way the cap and the overrun are on the work
// card and in the take's cost record on the primary (Consumer.Cap, Consumer.Overrun). The
// model is pkg/friend/tla/LaneEnd.tla, CapRedeal and RedealOnce.

// The fields of a capped take: on the work card the cap and the overrun its finish named,
// and on the primary the re-deal the cap spent (`<from> -> <to>`), once per card.
const (
	FieldLaneCap      = "lane_cap"
	FieldLaneOverrun  = "lane_overrun"
	FieldCapRedealt   = "cap_redealt"
	laneCapEnd        = "capped"
	laneCapRedealWord = "re-dealt once at the next tier up"
)

// capLadder is the tiers a capped card climbs, one step for its one re-deal: the route
// ladder and frontier above it, for a lane's tier is a friend's class and frontier is one.
var capLadder = []string{cardhdr.RouteFlash, cardhdr.RoutePro, cardhdr.RouteHeavy, cardhdr.RouteFrontier}

// LaneCap is a lane's cap as a failed finish's report names it: the cap, the tier it was
// the cap of, and the wall past it when the lane ended.
type LaneCap struct {
	Cap, Overrun time.Duration
	Tier         string
}

var laneCapRE = regexp.MustCompile(`\bcapped at ([0-9][0-9a-zµ.]*) \(tier ([a-z-]+), overrun ([0-9][0-9a-zµ.]*)\)`)

// ParseLaneCap reads the cap off a failed finish's report (the friend daemon's
// CappedWords); ok is false when it names none.
func ParseLaneCap(report string) (lc LaneCap, ok bool) {
	m := laneCapRE.FindStringSubmatch(report)
	if m == nil {
		return LaneCap{}, false
	}
	limit, err1 := time.ParseDuration(m[1])
	over, err2 := time.ParseDuration(m[3])
	if err1 != nil || err2 != nil || limit <= 0 {
		return LaneCap{}, false
	}
	if m[2] != "-" {
		lc.Tier = m[2]
	}
	lc.Cap, lc.Overrun = limit, over
	return lc, true
}

// capNextTier is the tier a capped primary pr is re-dealt on: the next of capLadder above
// the tier it is on; "" when the cap has been spent on it already, at the top, or when its
// tier is pinned (pinnedTier: a re-deal there would run on the same tier).
func capNextTier(pr *Card) string {
	if pr.F(FieldCapRedealt) != "" {
		return ""
	}
	m, bad := cardhdr.ReadModel(pr.F("brief"))
	if bad != "" || pinnedTier(pr, m) {
		return ""
	}
	i := slices.Index(capLadder, cardTier(pr, m))
	if i < 0 || i+1 >= len(capLadder) {
		return ""
	}
	return capLadder[i+1]
}

// capSets is what a capped finish writes on the work card: its cap and overrun.
func capSets(lc LaneCap, card map[string]string) {
	card[FieldLaneCap] = lc.Cap.String()
	card[FieldLaneOverrun] = lc.Overrun.String()
}

// capRedeal is the unit of a capped finish whose cap re-deals it: the work card done
// failed with the cap on it and its cost recorded on the primary (the end `capped`, the cap
// and the overrun on the record); the primary back to ready on the next tier, told why,
// FieldCapRedealt set so the cap re-deals it once, its failed count untouched. A friend's
// finish takes her next ready card in the same step (friendNext).
func capRedeal(s *Snapshot, c, pr *Card, r FinishReq, lc LaneCap, next string, prior []Unit) Unit {
	head := r.Head
	if head == "" {
		head = c.ID
	}
	cardSet := map[string]string{"ok": "no", "head": head, "finished": stamp(s.Now)}
	capSets(lc, cardSet)
	if r.Report != "" {
		cardSet["report"] = r.Report
	}
	if r.Branch != "" {
		cardSet["branch"] = r.Branch
	}
	dealt, taken := takeStamps(c)
	rec := costRecord(s, r.Usage, c.F(FieldRoute), c.F(FieldModel), false, dealt, taken)
	if r.Usage != "" {
		cardSet[FieldUsage] = rec
	}
	from := cardTierOf(pr)
	why := fmt.Sprintf("capped at %s on %s (overrun %s): %s, %s", lc.Cap, from, lc.Overrun, laneCapRedealWord, next)
	set := map[string]string{FieldTierNow: next, "why": why, FieldCapRedealt: from + " -> " + next}
	maps.Copy(set, finishStamps(pr, c, s.Now))
	cons := workConsumer(s, c, 0, laneCapEnd, rec)
	cons.Cap, cons.Overrun = lc.Cap.String(), lc.Overrun.String()
	addConsumer(pr, set, cons)
	u := Unit{Key: c.ID, Stream: pr.Row, Changes: []Change{
		change(Fleet, moveEntry(c, c.Row, DoneFailed, cardSet)),
		change(Work, moveEntry(pr, pr.Row, Ready, set)),
	}, Moved: fmt.Sprintf("%s working -> done failed; %s working -> ready (%s)", c.ID, pr.ID, why)}
	friendNext(s, c, &u, prior)
	return u
}
