package sprint

import (
	"cmp"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Promotion into dev (docs/SPEC-SPRINT.md section 7, "dev is behind"; the owner, 2026-10-04:
// "You should regularly, mechanically be reminded merges to dev are dirty, and should be
// done", and "We must merge into dev continually, at least in bursts. Otherwise we can drift
// from friend or other stream branch work and cause a big fuckup"). The lander merges each
// green batch onto the sprint branch; promoting that branch into dev is the coordinator's,
// and the store records it when the coordinator says so (Promoted, `nova-sprint promoted
// --sha <merge sha>`). The decision is one pure function over the snapshot, DevBehind: the
// cards landed since the last promotion pass PromoteCards, or the oldest of them is older
// than PromoteAge, whichever comes first.

// The work table's properties of the last promotion: when, and the merge sha.
const (
	PropPromotedAt  = "promoted_at"
	PropPromotedSha = "promoted_sha"
)

// PromoteCards is how many cards may land on the sprint branch before dev is behind.
const PromoteCards = 25

// PromoteAge is how long the oldest landing since the last promotion may wait before dev
// is behind.
const PromoteAge = 30 * time.Minute

// NDevBehind is the tick's judgment that dev is behind, one for the sprint.
const NDevBehind = "dev is behind"

// DevLag is dev behind: the cards landed since the last promotion, the branch they
// landed on (the base the most of them name, "the sprint branch" when none does), and
// the last promotion (zero and "" when none is recorded).
type DevLag struct {
	Count  int
	Branch string
	At     time.Time
	Sha    string
}

// Promotion is the last promotion the store records: when and the sha; ok false when none.
func Promotion(s *Snapshot) (at time.Time, sha string, ok bool) {
	v, has := s.Work.Prop(PropPromotedAt)
	if !has {
		return time.Time{}, "", false
	}
	t, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return time.Time{}, "", false
	}
	sha, _ = s.Work.Prop(PropPromotedSha)
	return t, sha, true
}

// LandedSince is the primaries (sentinels aside) landed after the last promotion, oldest
// first by their landing stamp, and the branch the most of them name as their base.
func LandedSince(s *Snapshot) (cards []*Card, branch string) {
	if s.Work == nil {
		return nil, ""
	}
	at, _, _ := Promotion(s)
	bases := map[string]int{}
	var oldest time.Time
	for _, c := range s.Work.Column(Landed) {
		if IsSentinel(c) {
			continue
		}
		t, err := time.Parse(time.RFC3339, c.F("landed"))
		if err != nil || !t.After(at) {
			continue
		}
		cards = append(cards, c)
		if b := c.F("base"); b != "" {
			bases[b]++
		}
		if oldest.IsZero() || t.Before(oldest) {
			oldest = t
		}
	}
	branch = "the sprint branch"
	best := 0
	for b, n := range bases {
		if n > best || n == best && b < branch {
			branch, best = b, n
		}
	}
	return cards, branch
}

// DevBehind is whether dev is behind, and the facts: PromoteCards or more cards landed since
// the last promotion, or the oldest of them landed PromoteAge or longer ago.
func DevBehind(s *Snapshot) (DevLag, bool) {
	cards, branch := LandedSince(s)
	if len(cards) == 0 {
		return DevLag{}, false
	}
	oldest := s.Now
	for _, c := range cards {
		if t, err := time.Parse(time.RFC3339, c.F("landed")); err == nil && t.Before(oldest) {
			oldest = t
		}
	}
	if len(cards) < PromoteCards && s.Now.Sub(oldest) < PromoteAge {
		return DevLag{}, false
	}
	d := DevLag{Count: len(cards), Branch: branch}
	d.At, d.Sha, _ = Promotion(s)
	return d, true
}

// What is the judgment's line: the count, the branch, the last promotion, and the promotion
// to run.
func (d DevLag) What() string {
	last := "no promotion recorded"
	if !d.At.IsZero() {
		last = "the last promotion at " + d.At.UTC().Format(time.RFC3339) + " (" + orDash(d.Sha) + ")"
	}
	return fmt.Sprintf("dev is behind: %d cards landed on %s since %s; promote: merge origin/dev into the sprint branch, open the PR to dev, run the functional tier, queue it; then: nova-sprint promoted --sha <merge sha> --branch <sprint branch> --tip <sha> --cards <ids> (nova-sprint promote prints it whole; --failed <why> when it did not merge)",
		d.Count, d.Branch, last)
}

// Decisions are the judgment's: the promotion recorded, or wait 30m.
func (d DevLag) Decisions() []string { return []string{"promoted", "wait 30m"} }

// devBehindCond is the tick's condition when dev is behind (NDevBehind).
func devBehindCond(s *Snapshot) []cond {
	d, ok := DevBehind(s)
	if !ok {
		return nil
	}
	return []cond{{typ: NDevBehind, streamLevel: true, what: d.What(), decisions: d.Decisions()}}
}

// PromotedReq is the coordinator's word that a branch was promoted into dev, or that the
// promotion failed (docs/SPEC-SPRINT.md section 7, delivery milestones).
type PromotedReq struct {
	// Sha is the merged result on the target, as the merge names it: under a squash or a
	// rebase it is not the promoted tip, and both are recorded.
	Sha     string
	Answers []string // the judgments it answers (answered): one naming no open judgment refuses the whole step
	Who     string
	// Returned is the landed cards dev or an audit returned with the promotion: each is
	// marked (readtier.go, FieldReturnedByDev) and the tick asks to raise its stream's
	// read tier.
	Returned []string `json:",omitempty"`
	// Branch is the branch promoted (the sprint branch), Tip its commit promoted, Target the
	// ref it was merged into (DefaultDevRef when empty), Repo the repository and Evidence the
	// gate's or the review's (the pull request, the merge-queue entry).
	Branch   string `json:",omitempty"`
	Tip      string `json:",omitempty"`
	Target   string `json:",omitempty"`
	Repo     string `json:",omitempty"`
	Evidence string `json:",omitempty"`
	// Cards is the landed cards the promoted range carried (the caller's fact: their land
	// commits are between the last promotion and the tip). Without them, the cards staged on
	// Branch are verified; with neither, none is.
	Cards []string `json:",omitempty"`
	// Failed is why the promotion failed: recorded on the work table, verifying nothing, and
	// standing until a promotion merges after it (FailedPromotion).
	Failed string `json:",omitempty"`
}

var shaWord = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// PromotedSha is the sha a promotion names, lower cased, or why it is not one.
func PromotedSha(given string) (string, string) {
	sha := strings.ToLower(strings.TrimSpace(given))
	if !shaWord.MatchString(sha) {
		return "", "--sha wants the merge commit's sha, 7 to 40 hex digits; found " + orDash(given)
	}
	return sha, ""
}

// Promoted records the promotion (nova-sprint promoted --sha <merge sha> --branch <sprint
// branch>): the coordinator's alone; the sha is 7 to 40 hex digits. It writes the work table's
// PropPromotedAt and PropPromotedSha, verifies in dev the cards the promotion carried
// (promotedCards: each card's verified record and its stream's), and closes the judgment "dev
// is behind"; the next tick counts landings from here. With Failed it records the failure
// alone (recordFailed).
func Promoted(s *Snapshot, r PromotedReq) Plan {
	var p Plan
	if w := notCoordinator(s, r.Who, "promoted"); w != "" {
		p.refuse("promoted", strings.Replace(w, "answers a judgment, which is", "is", 1))
		return p
	}
	target := cmp.Or(r.Target, DefaultDevRef)
	if r.Failed != "" {
		return recordFailed(s, r, target)
	}
	sha, why := PromotedSha(r.Sha)
	if why != "" {
		p.refuse("promoted", why)
		return p
	}
	now := stamp(s.Now)
	for _, kv := range [][2]string{{PropPromotedAt, now}, {PropPromotedSha, sha}} {
		was, had := s.Work.Prop(kv[0])
		p.Props = append(p.Props, PropWrite{Table: Work, Name: kv[0], Value: kv[1], Was: was, WasAbsent: !had})
	}
	for _, id := range r.Returned {
		c := s.Work.Placed(id)
		if c == nil {
			p.refuse(id, "--returned names landed cards; no "+id+" on the table")
			return p
		}
		if c.Col != Landed {
			p.refuse(id, "--returned names landed cards; "+id+" is "+placeWord(c))
			return p
		}
	}
	verified, key, why := promotedCards(s, r)
	if why != "" {
		p.refuse(key, why)
		return p
	}
	cards, branch := LandedSince(s)
	if r.Branch != "" {
		branch = r.Branch
	}
	from := fromOf(r.Branch, r.Tip)
	u := Unit{Key: "promoted", Moved: fmt.Sprintf("promoted %s into %s at %s (%s): %d cards landed since the last promotion, %d verified in %s", branch, target, now, sha, len(cards), len(verified), target)}
	if len(verified) == 0 && r.Branch == "" && len(r.Cards) == 0 {
		u.Moved += " (name --branch <sprint branch> or --cards: a promotion that does not say what it carried verifies nothing)"
	}
	sets := map[string]map[string]string{}
	for _, c := range verified {
		sets[c.ID] = known(map[string]string{FieldVerified: now, FieldVerifiedRepo: r.Repo, FieldVerifiedRef: target,
			FieldVerifiedCommit: sha, FieldVerifiedFrom: from, FieldVerifiedEvidence: r.Evidence})
	}
	for _, id := range r.Returned {
		// returned by dev or an audit: the card's record, read by the tick (readtier.go)
		c := s.Work.Placed(id)
		set := map[string]string{FieldReturnedByDev: now, FieldReturnedByDevTier: s.readTierOf(c), FieldReturnedByDevSha: sha}
		for k, v := range sets[id] {
			set[k] = v
		}
		sets[id] = set
	}
	var streams []string
	for _, c := range s.Work.Column(Landed) {
		if set, ok := sets[c.ID]; ok {
			u.Changes = append(u.Changes, change(Work, setEntry(c, set)))
			if set[FieldVerified] != "" && !slices.Contains(streams, c.Row) {
				streams = append(streams, c.Row)
			}
		}
	}
	slices.Sort(streams)
	for _, st := range streams {
		if set := streamRecord(s, st, sets, map[string]string{FieldVerifiedRef: target, FieldVerifiedCommit: sha}); set != nil {
			u.Changes = append(u.Changes, change(Merge, setEntry(s.StreamCtl(st), set)))
		}
	}
	if len(r.Returned) > 0 {
		u.Moved += fmt.Sprintf("; returned by dev: %s", strings.Join(r.Returned, ", "))
	}
	for _, o := range s.Open {
		if o.Note.Type == NDevBehind {
			u.Closes = append(u.Closes, o)
		}
	}
	p.Units = append(p.Units, u)
	answered(&p, s, r.Answers, r.Who)
	return p
}

// recordFailed records a failed promotion on the work table (PropPromoteFailedAt and the rest):
// no card is verified, the judgment "dev is behind" stays open, and the failure stands, in the
// views, until a promotion merges after it.
func recordFailed(s *Snapshot, r PromotedReq, target string) Plan {
	var p Plan
	if len(r.Returned) > 0 || len(r.Cards) > 0 || r.Sha != "" {
		p.refuse("promoted", "--failed records a promotion that did not merge: it takes no --sha, --cards or --returned")
		return p
	}
	why := strings.Join(strings.Fields(r.Failed), " ")
	now := stamp(s.Now)
	from := fromOf(r.Branch, r.Tip)
	for _, kv := range [][2]string{{PropPromoteFailedAt, now}, {PropPromoteFailedWhy, why}, {PropPromoteFailedFrom, orDash(from)}, {PropPromoteFailedEvidence, orDash(r.Evidence)}} {
		was, had := s.Work.Prop(kv[0])
		p.Props = append(p.Props, PropWrite{Table: Work, Name: kv[0], Value: kv[1], Was: was, WasAbsent: !had})
	}
	p.Units = append(p.Units, Unit{Key: "promoted", Moved: fmt.Sprintf("promotion of %s into %s FAILED at %s: %s", orDash(from), target, now, why)})
	answered(&p, s, r.Answers, r.Who)
	return p
}
