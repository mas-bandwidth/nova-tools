package sprint

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Delivery milestones (docs/SPEC-SPRINT.md section 7, delivery milestones; tla/PromoteDelivery.tla).
// A card is delivered in three steps, each an explicit record on the card and on its stream's
// control card, never inferred from another: staged (the lander pushed its batch to the branch
// its stream lands on, the sprint branch or a stream branch: the work table's landed), verified
// in dev (a promotion merged a branch that holds it into dev, and the merged result on dev is
// recorded as the promotion names it, a squash or rebase included), and installed on a target (an
// install receipt names the dev commit a promotion recorded). Each record carries the
// repository, the target ref, the commit and the gate or review evidence. A push to a stream
// branch is staged and nothing more: only a promotion of the branch it was staged on, or one
// naming it among the cards it carried, verifies it in dev.

// The milestones, as the spec, the verbs and the views name them.
const (
	MilestoneStaged    = "staged"
	MilestoneVerified  = "verified in dev"
	MilestoneInstalled = "installed"
)

// A primary's milestone fields. Staged is written by the landing (merging -> landed), whose
// stamp is the card's landed; verified by promoted; installed by installed, one field a target.
const (
	FieldStagedRepo     = "staged_repo"
	FieldStagedRef      = "staged_ref"
	FieldStagedCommit   = "staged_commit"
	FieldStagedEvidence = "staged_evidence"

	FieldVerified         = "verified"
	FieldVerifiedRepo     = "verified_repo"
	FieldVerifiedRef      = "verified_ref"
	FieldVerifiedCommit   = "verified_commit"
	FieldVerifiedFrom     = "verified_from"
	FieldVerifiedEvidence = "verified_evidence"

	// FieldInstalledPrefix + <target> is the install receipt on that target: the dev commit,
	// the stamp and the receipt, one blank between each (the receipt last, its own blanks kept).
	FieldInstalledPrefix = "installed."
)

// A stream's record on its control card: the count of its cards at each milestone (set from
// the cards at each record, never added to, so a replay writes the same), and its last
// staging, verification and install (FieldStagedRef, FieldStagedCommit, FieldVerifiedCommit,
// FieldInstalledCommit).
const (
	FieldStreamStaged    = "delivery_staged"
	FieldStreamVerified  = "delivery_verified"
	FieldStreamInstalled = "delivery_installed"
	FieldInstalledCommit = "installed_commit"
)

// The work table's properties of the last failed promotion: when, why, the ref and tip it
// promoted, and its evidence. It stands until a promotion records a merge after it.
const (
	PropPromoteFailedAt       = "promote_failed_at"
	PropPromoteFailedWhy      = "promote_failed_why"
	PropPromoteFailedFrom     = "promote_failed_from"
	PropPromoteFailedEvidence = "promote_failed_evidence"
)

// DefaultDevRef is the ref a promotion targets when it names none.
const DefaultDevRef = "dev"

// Milestone is one delivery record: the repository, the target ref, the commit and the gate or
// review evidence. Empty fields are not known to the caller.
type Milestone struct {
	Repo     string `json:",omitempty"`
	Ref      string `json:",omitempty"`
	Commit   string `json:",omitempty"`
	Evidence string `json:",omitempty"`
}

// stagedSet is the staged record a landing writes on c: the caller's milestone, the ref the
// card's base when the caller named none.
func stagedSet(c *Card, m *Milestone) map[string]string {
	out := map[string]string{}
	ref := c.F("base")
	if m != nil {
		out[FieldStagedRepo], out[FieldStagedCommit], out[FieldStagedEvidence] = m.Repo, m.Commit, m.Evidence
		if m.Ref != "" {
			ref = m.Ref
		}
	}
	out[FieldStagedRef] = ref
	return known(out)
}

// known is the fields of set with a value: a milestone's unknown parts are left off the card.
func known(set map[string]string) map[string]string {
	for k, v := range set {
		if v == "" {
			delete(set, k)
		}
	}
	return set
}

// StagedRef is the ref c was staged on: its staged record, else its base.
func StagedRef(c *Card) string {
	if r := c.F(FieldStagedRef); r != "" {
		return r
	}
	return c.F("base")
}

// Install is one install receipt of a card.
type Install struct {
	Target  string
	Commit  string
	At      string
	Receipt string
}

// InstallsOf is c's install receipts, by target.
func InstallsOf(c *Card) []Install {
	var out []Install
	if c == nil {
		return nil
	}
	for k, v := range c.Fields {
		target, ok := strings.CutPrefix(k, FieldInstalledPrefix)
		if !ok || target == "" {
			continue
		}
		parts := strings.SplitN(v, " ", 3)
		in := Install{Target: target, Commit: parts[0]}
		if len(parts) > 1 {
			in.At = parts[1]
		}
		if len(parts) > 2 {
			in.Receipt = parts[2]
		}
		out = append(out, in)
	}
	slices.SortFunc(out, func(a, b Install) int { return strings.Compare(a.Target, b.Target) })
	return out
}

// DeliveryCounts is the three milestone counts, apart.
type DeliveryCounts struct {
	Staged    int `json:"staged"`
	Verified  int `json:"verified"`
	Installed int `json:"installed"`
}

// DeliveryView is the sprint's (or a stream's) delivery: the counts, the installed cards by
// target, and the failed promotion that stands, when one does.
type DeliveryView struct {
	DeliveryCounts
	Targets map[string]int    `json:"targets,omitempty"`
	Failed  *PromotionFailure `json:"failed,omitempty"`
}

// Counts is the three counts alone.
func (d DeliveryView) Counts() DeliveryCounts { return d.DeliveryCounts }

// Delivery is the sprint's delivery over every stream.
func Delivery(s *Snapshot) DeliveryView {
	d := deliveryOf(s, s.Streams()...)
	if f, ok := FailedPromotion(s); ok {
		d.Failed = &f
	}
	return d
}

// StreamDelivery is one stream's delivery.
func StreamDelivery(s *Snapshot, stream string) DeliveryView { return deliveryOf(s, stream) }

func deliveryOf(s *Snapshot, streams ...string) DeliveryView {
	var d DeliveryView
	if s.Work == nil {
		return d
	}
	for _, stream := range streams {
		for _, c := range s.Work.Cell(stream, Landed) {
			if IsSentinel(c) {
				continue
			}
			d.Staged++
			if c.F(FieldVerified) != "" {
				d.Verified++
			}
			ins := InstallsOf(c)
			if len(ins) > 0 {
				d.Installed++
			}
			for _, in := range ins {
				if d.Targets == nil {
					d.Targets = map[string]int{}
				}
				d.Targets[in.Target]++
			}
		}
	}
	return d
}

// streamRecord is the set that brings a stream's control card to its delivery after a plan
// whose card fields are given (by card id, the fields the plan sets on it): the three counts,
// and the last record of the milestones the plan writes (extra). Nil when nothing changes.
func streamRecord(s *Snapshot, stream string, sets map[string]map[string]string, extra map[string]string) map[string]string {
	ctl := s.StreamCtl(stream)
	if ctl == nil {
		return nil
	}
	var d DeliveryCounts
	for _, c := range s.Work.Cell(stream, Landed) {
		if IsSentinel(c) {
			continue
		}
		f := func(name string) string {
			if v, ok := sets[c.ID][name]; ok {
				return v
			}
			return c.F(name)
		}
		d.Staged++
		if f(FieldVerified) != "" {
			d.Verified++
		}
		installed := len(InstallsOf(c)) > 0
		for k := range sets[c.ID] {
			installed = installed || strings.HasPrefix(k, FieldInstalledPrefix)
		}
		if installed {
			d.Installed++
		}
	}
	for id, set := range sets {
		if c := s.Work.Placed(id); c != nil && c.Row == stream && c.Col != Landed {
			// landing in this plan: staged now
			d.Staged++
			if set[FieldVerified] != "" {
				d.Verified++
			}
		}
	}
	out := map[string]string{}
	for k, v := range map[string]string{FieldStreamStaged: itoa(d.Staged), FieldStreamVerified: itoa(d.Verified), FieldStreamInstalled: itoa(d.Installed)} {
		if ctl.F(k) != v {
			out[k] = v
		}
	}
	for k, v := range extra {
		if v != "" && ctl.F(k) != v {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// PromotionFailure is a failed promotion on the record.
type PromotionFailure struct {
	At       time.Time `json:"at"`
	Why      string    `json:"why"`
	From     string    `json:"from,omitempty"`
	Evidence string    `json:"evidence,omitempty"`
}

// FailedPromotion is the failed promotion that stands: recorded, and no promotion merged
// since. ok false when none stands.
func FailedPromotion(s *Snapshot) (PromotionFailure, bool) {
	v, has := s.Work.Prop(PropPromoteFailedAt)
	if !has || v == "" {
		return PromotionFailure{}, false
	}
	at, err := time.Parse(time.RFC3339, v)
	if err != nil {
		return PromotionFailure{}, false
	}
	if last, _, ok := Promotion(s); ok && !last.Before(at) {
		return PromotionFailure{}, false
	}
	f := PromotionFailure{At: at}
	f.Why, _ = s.Work.Prop(PropPromoteFailedWhy)
	f.From, _ = s.Work.Prop(PropPromoteFailedFrom)
	f.Evidence, _ = s.Work.Prop(PropPromoteFailedEvidence)
	if f.From == "-" {
		f.From = ""
	}
	if f.Evidence == "-" {
		f.Evidence = ""
	}
	return f, true
}

// fromOf is a promotion's source as the records name it: <branch>@<tip>, either part alone,
// or "" when neither is known.
func fromOf(branch, tip string) string {
	switch {
	case branch != "" && tip != "":
		return branch + "@" + tip
	case branch != "":
		return branch
	}
	return tip
}

// promotedCards is the landed primaries a promotion verifies in dev, oldest landing first, and
// a refusal: the cards it names (each must be a landed primary; the caller's fact, from the
// promoted range of the sprint branch, is that each is in it), else the landed primaries not
// yet verified that its tip carried on the branch it names (tipCarried). Neither named: none,
// since nothing says what reached dev.
func promotedCards(s *Snapshot, r PromotedReq) ([]*Card, string, string) {
	var out []*Card
	if len(r.Cards) > 0 {
		for _, id := range r.Cards {
			c := s.Work.Placed(id)
			switch {
			case c == nil:
				return nil, id, "--cards names landed cards; no " + id + " on the table"
			case c.Col != Landed || IsSentinel(c):
				return nil, id, "--cards names landed cards; " + id + " is " + placeWord(c)
			}
			if c.F(FieldVerified) == "" && !slices.Contains(out, c) {
				out = append(out, c)
			}
		}
		return out, "", ""
	}
	return tipCarried(s, r.Branch, r.Tip), "", ""
}

// tipCarried is the landed primaries not yet verified that the commit tip of branch carries,
// oldest landing first: the cards staged on branch whose landing is the one that staged tip
// (its staged_commit) or landed before it. The store holds no git, so the landing that pushed
// tip is the bound: a card the lander pushed after the promotion froze its tip (promote cuts
// promo/<date>-<n> there, then waits on the queue) landed after that landing and is not
// carried. No branch, no tip, or a tip no landing on branch staged: none, since nothing says
// which landings reached dev (tipUnknown says why). A landing in the same second as tip's from
// another batch is left for the next promotion, never counted early. tla/PromoteDelivery.tla's
// IgnoreTip witness is the fallback without that bound: VerifiedInDev fails.
func tipCarried(s *Snapshot, branch, tip string) []*Card {
	if branch == "" || tip == "" {
		return nil
	}
	same := sameCommit
	var cutoff string
	for _, c := range s.Work.Column(Landed) {
		if !IsSentinel(c) && StagedRef(c) == branch && same(c.F(FieldStagedCommit), tip) && c.F("landed") > cutoff {
			cutoff = c.F("landed")
		}
	}
	if cutoff == "" {
		return nil
	}
	var out []*Card
	for _, c := range s.Work.Column(Landed) {
		if IsSentinel(c) || c.F(FieldVerified) != "" || StagedRef(c) != branch {
			continue
		}
		if l := c.F("landed"); l > cutoff || (l == cutoff && !same(c.F(FieldStagedCommit), tip)) {
			continue
		}
		if t, err := time.Parse(time.RFC3339, c.F("landed")); err != nil || t.After(s.Now) {
			continue
		}
		out = append(out, c)
	}
	slices.SortStableFunc(out, func(a, b *Card) int { return strings.Compare(a.F("landed"), b.F("landed")) })
	return out
}

// sameCommit says a and b name one commit: both given, either a prefix of the other.
func sameCommit(a, b string) bool {
	return a != "" && b != "" && (strings.HasPrefix(a, b) || strings.HasPrefix(b, a))
}

// tipUnknown is why a promotion that names no --cards verifies nothing, or "" when its tip
// bounds the cards it carried.
func tipUnknown(s *Snapshot, r PromotedReq) string {
	switch {
	case len(r.Cards) > 0:
		return ""
	case r.Branch == "":
		return "name --branch <sprint branch> --tip <sha> or --cards: a promotion that does not say what it carried verifies nothing"
	case r.Tip == "":
		return "--branch wants --tip <the commit promoted> or --cards: a branch alone does not say which landings reached dev"
	}
	for _, c := range s.Work.Column(Landed) {
		if StagedRef(c) == r.Branch && sameCommit(c.F(FieldStagedCommit), r.Tip) {
			return ""
		}
	}
	return "no landing on " + r.Branch + " staged " + r.Tip + "; name --cards: the cards that commit carried"
}

// InstalledReq is the coordinator's install receipt: a target took a dev commit a promotion
// recorded (nova-sprint installed <target> --sha <dev commit> --receipt <text>).
type InstalledReq struct {
	Target  string
	Commit  string
	Receipt string
	Who     string
}

var targetWord = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// Installed records an install receipt on every card verified in dev at the commit named or
// before it, on the target named, and each touched stream's record. Refused: not the
// coordinator, a target that is not one word, a commit that is not 7 to 40 hex digits, no
// receipt, and a commit no promotion recorded (an install names what dev was given).
func Installed(s *Snapshot, r InstalledReq) Plan {
	var p Plan
	if w := notCoordinator(s, r.Who, "installed"); w != "" {
		p.refuse("installed", strings.Replace(w, "answers a judgment, which is", "is", 1))
		return p
	}
	if !targetWord.MatchString(r.Target) {
		p.refuse("installed", "the target is one word, letters, digits, '.', '_' or '-'; found "+orDash(r.Target))
		return p
	}
	sha, why := PromotedSha(r.Commit)
	if why != "" {
		p.refuse("installed", why)
		return p
	}
	receipt := strings.Join(strings.Fields(r.Receipt), " ")
	if receipt == "" {
		p.refuse("installed", "--receipt wants the install's receipt: what the target reported (the version, its checksum, the install log's last line)")
		return p
	}
	same := func(a, b string) bool { return a != "" && (strings.HasPrefix(a, b) || strings.HasPrefix(b, a)) }
	var cutoff string
	for _, c := range s.Work.Column(Landed) {
		if same(c.F(FieldVerifiedCommit), sha) && c.F(FieldVerified) > cutoff {
			cutoff = c.F(FieldVerified)
		}
	}
	if cutoff == "" {
		p.refuse("installed", "no card was verified in dev at "+sha+"; an install receipt names the merge sha a promotion recorded (nova-sprint promoted --sha <sha> --branch <sprint branch>)")
		return p
	}
	now := stamp(s.Now)
	field := FieldInstalledPrefix + r.Target
	sets := map[string]map[string]string{}
	byStream := map[string][]*Card{}
	for _, c := range s.Work.Column(Landed) {
		if IsSentinel(c) || c.F(FieldVerified) == "" || c.F(FieldVerified) > cutoff || c.F(field) != "" {
			continue
		}
		sets[c.ID] = map[string]string{field: sha + " " + now + " " + receipt}
		byStream[c.Row] = append(byStream[c.Row], c)
	}
	u := Unit{Key: "installed", Moved: fmt.Sprintf("installed %s on %s at %s (%s): %d cards", sha, r.Target, now, receipt, len(sets))}
	streams := make([]string, 0, len(byStream))
	for st := range byStream {
		streams = append(streams, st)
	}
	slices.Sort(streams)
	for _, st := range streams {
		for _, c := range byStream[st] {
			u.Changes = append(u.Changes, change(Work, setEntry(c, sets[c.ID])))
		}
		if set := streamRecord(s, st, sets, map[string]string{FieldInstalledCommit: sha}); set != nil {
			u.Changes = append(u.Changes, change(Merge, setEntry(s.StreamCtl(st), set)))
		}
	}
	p.Units = append(p.Units, u)
	return p
}
