package sprint

import (
	"fmt"
	"regexp"
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
	return fmt.Sprintf("dev is behind: %d cards landed on %s since %s; promote: merge origin/dev into the sprint branch, open the PR to dev, run the functional tier, queue it; then: nova-sprint promoted --sha <merge sha>",
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

// PromotedReq is the coordinator's word that the sprint branch was promoted into dev: the
// merge sha.
type PromotedReq struct {
	Sha     string
	Answers []string // the judgments it answers (answered): one naming no open judgment refuses the whole step
	Who     string
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

// Promoted records the promotion (nova-sprint promoted --sha <merge sha>): the coordinator's
// alone; the sha is 7 to 40 hex digits. It writes the work table's PropPromotedAt and
// PropPromotedSha and closes the judgment "dev is behind"; the next tick counts landings
// from here.
func Promoted(s *Snapshot, r PromotedReq) Plan {
	var p Plan
	if w := notCoordinator(s, r.Who, "promoted"); w != "" {
		p.refuse("promoted", strings.Replace(w, "answers a judgment, which is", "is", 1))
		return p
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
	cards, branch := LandedSince(s)
	u := Unit{Key: "promoted", Moved: fmt.Sprintf("promoted %s into dev at %s (%s): %d cards landed since the last promotion", branch, now, sha, len(cards))}
	for _, o := range s.Open {
		if o.Note.Type == NDevBehind {
			u.Closes = append(u.Closes, o)
		}
	}
	p.Units = append(p.Units, u)
	answered(&p, s, r.Answers, r.Who)
	return p
}
