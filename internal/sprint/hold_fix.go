package sprint

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
	"github.com/mas-bandwidth/nova-tools/internal/member"
	"github.com/mas-bandwidth/nova-tools/internal/typedrec"
)

// A HOLD that names its own fix is applied by the machine at finish, not read by the seat
// (docs/SPEC-SPRINT.md section 8, the hold fix lines). Four lines, each the whole of a
// trimmed line and the first of its kind, are the whole grammar. Nothing else in the note
// is read.
//
//   - PATHS-PROPOSED: widens PATHS in place when the stream's land-protected mark covers
//     the card's repository (the same id, back to ready, the next attempt from the held
//     head). A stream with no mark, a proposal already inside PATHS, and a shared glob
//     stay the paths rule's (paths_proposed.go). A marked stream whose mark does not
//     cover the repository, or whose proposal climbs out of it, leaves the hold.
//   - NEEDS: appends the dependency when that card has landed. When it has not, the
//     primary stays in review with rule_need set (a primary in review has no move to
//     waiting) and the judgment says it is parked waiting. An unknown card, the card
//     itself, or a card that already needs this one leaves the hold.
//   - TIER: flash, pro or heavy, rewritten on line 1 and tier_now, when a friend of the
//     stream serves it. Any other word, or a tier no friend serves, leaves the hold.
//   - GATE-HOST: linux, recorded on the primary and passed to the next attempt's
//     packet so its Go gates run on a Linux bench. Another host leaves the hold.
//
// One line the machine cannot apply leaves the whole hold, the judgment prefixed
// holdFixRefused. A line the machine applies sends the card back to ready with fix set,
// except a NEEDS of a card that has not landed, which parks it. A note with no fix line
// finishes as it does today.
const (
	holdFixPaths    = "PATHS-PROPOSED:"
	holdFixNeeds    = "NEEDS:"
	holdFixTier     = "TIER:"
	holdFixGateHost = "GATE-HOST:"
	FieldGateHost   = "gate_host"

	// holdFixRefused prefixes the failed-work judgment when a fix line cannot be applied.
	holdFixRefused = "hold fix not applied: "

	holdFixReady  = "ready"
	holdFixPark   = "park"
	holdFixRefuse = "refuse"

	// NHoldFix is the happened note, to the coordinator, naming the lines a finish applied.
	NHoldFix = "hold fix applied"
)

// holdFix is what a finish does with the fix lines of one report.
type holdFix struct {
	act      string // holdFixReady, holdFixPark, holdFixRefuse, or "" when the note is not a hold fix
	why      string // the refusal, when act is holdFixRefuse
	said     string // the lines applied, joined
	brief    string
	tier     string
	gateHost string
	park     string // the card NEEDS names that has not landed
	needs    string // the primary's needs field when a landed card is appended
}

// briefTierRE is the tier word on line 1 (cardhdr's tierRE, which is unexported).
var briefTierRE = regexp.MustCompile(`\btier:\s*[A-Za-z0-9_-]+`)

// readHoldFix is the hold fix a failed finish's report names. The zero value finishes
// as today: no fix line, or a lone PATHS-PROPOSED the paths rule still owns.
func readHoldFix(s *Snapshot, pr *Card, report, head string) holdFix {
	lines, ok := parseHoldFix(report)
	if !ok {
		return holdFix{}
	}
	var reasons, applied []string
	var fresh []string
	var tier, park, needs string
	if lines.pathsSeen {
		switch {
		case !lines.pathsOK:
			reasons = append(reasons, "PATHS-PROPOSED names no path")
		default:
			claim, why, got := pathsHold(s, pr, lines.paths)
			switch {
			case !claim && !lines.needSeen && !lines.tierSeen:
				return holdFix{}
			case !claim || why != "":
				reasons = append(reasons, why)
			default:
				fresh = got
				applied = append(applied, "PATHS widened in place by "+strings.Join(got, ","))
			}
		}
	}
	if lines.needSeen {
		switch {
		case lines.need == "":
			reasons = append(reasons, "NEEDS names no card")
		default:
			why, landed := needHold(s, pr, lines.need)
			switch {
			case why != "":
				reasons = append(reasons, why)
			case landed:
				needs = withNeed(pr.F("needs"), lines.need)
				applied = append(applied, "needs "+lines.need)
			default:
				park = lines.need
				applied = append(applied, "parked waiting on "+lines.need)
			}
		}
	}
	if lines.tierSeen {
		if why := tierHold(s, pr, lines.tier); why != "" {
			reasons = append(reasons, why)
		} else {
			tier = lines.tier
			applied = append(applied, "tier "+lines.tier)
		}
	}
	if lines.gateSeen {
		if lines.gateHost != "linux" {
			reasons = append(reasons, "GATE-HOST "+lines.gateHost+" is not linux")
		} else {
			applied = append(applied, "gate host linux")
		}
	}
	if len(reasons) > 0 {
		return holdFix{act: holdFixRefuse, why: strings.Join(reasons, "; ")}
	}
	if len(applied) == 0 {
		return holdFix{}
	}
	fx := holdFix{
		said:     strings.Join(applied, "; "),
		brief:    applyHoldBrief(pr, head, fresh, tier),
		tier:     tier,
		gateHost: lines.gateHost,
		needs:    needs,
	}
	if park != "" {
		fx.act, fx.park = holdFixPark, park
		return fx
	}
	fx.act = holdFixReady
	return fx
}

// holdFixLines is the first of each fix line in a report.
type holdFixLines struct {
	paths              []string
	pathsSeen, pathsOK bool
	need               string
	needSeen           bool
	tier               string
	tierSeen           bool
	gateHost           string
	gateSeen           bool
}

// parseHoldFix reads the four lines. A line counts only when it starts with the key,
// so a mention inside a sentence is not a fix. ok is false when the report has none.
func parseHoldFix(report string) (holdFixLines, bool) {
	var l holdFixLines
	for _, line := range strings.Split(report, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case !l.pathsSeen && strings.HasPrefix(line, holdFixPaths):
			l.pathsSeen = true
			globs, ok := member.PathsProposed(line)
			l.paths, l.pathsOK = globs, ok && len(globs) > 0
		case !l.needSeen && strings.HasPrefix(line, holdFixNeeds):
			l.needSeen = true
			l.need = holdToken(strings.TrimPrefix(line, holdFixNeeds))
		case !l.tierSeen && strings.HasPrefix(line, holdFixTier):
			l.tierSeen = true
			l.tier = holdToken(strings.TrimPrefix(line, holdFixTier))
		case !l.gateSeen && strings.HasPrefix(line, holdFixGateHost):
			l.gateSeen = true
			l.gateHost = holdToken(strings.TrimPrefix(line, holdFixGateHost))
		}
	}
	return l, l.pathsSeen || l.needSeen || l.tierSeen || l.gateSeen
}

// holdToken is the first word of a fix line's value, quotes and a closing stop removed.
func holdToken(rest string) string {
	fields := strings.Fields(rest)
	if len(fields) == 0 {
		return ""
	}
	return strings.Trim(fields[0], "`*\"'.,;:")
}

// pathsHold says whether PATHS-PROPOSED is this finish's to apply. claim is false when
// the paths rule still owns it (no mark, already inside PATHS, or shared). why is why a
// claimed line cannot be applied. fresh is the globs the brief's PATHS do not hold.
func pathsHold(s *Snapshot, pr *Card, globs []string) (claim bool, why string, fresh []string) {
	marked := landMark(s, pr.Row) != ""
	if why = badGlobs(globs); why != "" {
		// an unmarked stream still belongs to the paths rule, which leaves a bad glob
		if !marked {
			return false, "PATHS-PROPOSED " + why, nil
		}
		return true, "PATHS-PROPOSED " + why, nil
	}
	own := headerGlobs(pr.F("brief"), cardhdr.KeyPaths)
	for _, g := range globs {
		if !slices.ContainsFunc(own, func(o string) bool { return o == g || globNames(o, g) }) {
			fresh = append(fresh, g)
		}
	}
	if len(fresh) == 0 {
		return false, "PATHS-PROPOSED is inside PATHS already", nil
	}
	if shared := sharedGlobs(s, pr, fresh); len(shared) > 0 {
		return false, "PATHS-PROPOSED is shared (" + strings.Join(shared, "; ") + ")", nil
	}
	if !marked {
		return false, "the stream is not marked land-protected", nil
	}
	if !landCovers(s, pr) {
		return true, "PATHS-PROPOSED is outside the stream's land-protected set", nil
	}
	return true, "", fresh
}

// landMark is the stream's land-protected mark, "" when it has none.
func landMark(s *Snapshot, stream string) string {
	ctl := s.StreamCtl(stream)
	if ctl == nil {
		return ""
	}
	return strings.TrimSpace(ctl.F(FieldLandProtected))
}

// landCovers says the stream's mark names the card's repository, or every repository.
func landCovers(s *Snapshot, pr *Card) bool {
	mark := landMark(s, pr.Row)
	if mark == "" {
		return false
	}
	repo := briefRepo(pr.F("brief"))
	for _, m := range strings.Split(mark, ",") {
		m = strings.TrimSpace(m)
		if m == LandProtectedAny || repo != "" && repoKey(m) == repoKey(repo) {
			return true
		}
	}
	return false
}

// briefRepo is the brief header's REPO line, "" when it names none.
func briefRepo(brief string) string {
	for i, l := range strings.Split(brief, "\n") {
		t := strings.TrimSpace(l)
		if i > 0 && t == "" {
			break
		}
		if k, v, ok := cardhdr.KeyValue(t); ok && k == "REPO" {
			return v
		}
	}
	return ""
}

// needHold says whether NEEDS can be applied. landed is true when the card has landed
// and the dependency may be stored. why is why the line cannot be applied.
func needHold(s *Snapshot, pr *Card, id string) (why string, landed bool) {
	if id == pr.ID {
		return "NEEDS names itself", false
	}
	if !ValidID(id) || s.Work.Placed(id) == nil {
		return "NEEDS names " + id + ", which is not a card on the table", false
	}
	if needsReach(s, id, pr.ID) {
		return "NEEDS names " + id + ", which needs " + pr.ID, false
	}
	return "", s.StateOf(id) == Landed
}

// needsReach says id's needs, walked, include target.
func needsReach(s *Snapshot, id, target string) bool {
	seen := map[string]bool{}
	var walk func(string) bool
	walk = func(cur string) bool {
		if cur == target {
			return true
		}
		if seen[cur] {
			return false
		}
		seen[cur] = true
		c := s.Work.Card(cur)
		if c == nil {
			return false
		}
		for _, n := range Split(c.F("needs")) {
			if walk(n) {
				return true
			}
		}
		return false
	}
	return walk(id)
}

// tierHold is why a TIER line cannot be applied, "" when a friend of the stream serves it.
func tierHold(s *Snapshot, pr *Card, tier string) string {
	switch tier {
	case cardhdr.RouteFlash, cardhdr.RoutePro, cardhdr.RouteHeavy:
	default:
		return "TIER " + tier + " is not flash, pro or heavy"
	}
	if !friendServes(s, pr, tier) {
		return "TIER " + tier + " is not served by the stream's friends"
	}
	return ""
}

// friendServes says some friend of the stream has the tier and may take this card.
func friendServes(s *Snapshot, pr *Card, tier string) bool {
	if s == nil || !s.FriendsTake(tier) {
		return false
	}
	kind := BriefKind(pr.F("brief"))
	for _, f := range s.Friends {
		if slices.Contains(friendTiers(f), tier) && FriendRestrictionWhy(f.Streams, f.Kinds, pr.Row, kind) == "" {
			return true
		}
	}
	return false
}

// withNeed is needs with id appended once.
func withNeed(needs, id string) string {
	cur := Split(needs)
	if slices.Contains(cur, id) {
		return strings.Join(cur, ",")
	}
	return strings.Join(append(cur, id), ",")
}

// applyHoldBrief is the brief with the applied lines written and the held head carried.
func applyHoldBrief(pr *Card, head string, fresh []string, tier string) string {
	brief := pr.F("brief")
	carry := ""
	if typedrec.IsFullSha(head) {
		carry = member.CarryLine(member.Carry{Card: pr.ID, Attempt: pr.Int("attempt"), Head: head})
	}
	if len(fresh) > 0 {
		brief = WidenBrief(brief, fresh, carry)
	} else if carry != "" {
		brief = PathsWidened(brief, nil, carry)
	}
	if tier != "" {
		brief = briefWithTier(brief, tier)
	}
	return brief
}

// briefWithTier rewrites the tier word on line 1, or appends one when line 1 names none.
func briefWithTier(brief, tier string) string {
	lines := strings.Split(brief, "\n")
	if len(lines) == 0 {
		return "tier: " + tier
	}
	if briefTierRE.MatchString(lines[0]) {
		lines[0] = briefTierRE.ReplaceAllString(lines[0], "tier: "+tier)
	} else {
		lines[0] = strings.TrimRight(lines[0], " ") + " tier: " + tier
	}
	return strings.Join(lines, "\n")
}

// applyHoldFix is the unit of a finish whose fix lines apply and send the card back to
// ready: the work card ends failed, the primary keeps its id, and one happened note
// tells the coordinator what was applied. No failed-work judgment is written.
func applyHoldFix(s *Snapshot, c, pr *Card, r FinishReq, who, head string, fx holdFix) Unit {
	cardSet := map[string]string{"ok": "no", "head": head, "finished": stamp(s.Now)}
	if r.Report != "" {
		cardSet["report"] = r.Report
	}
	if r.Branch != "" {
		cardSet["branch"] = r.Branch
	}
	if r.Base != "" {
		cardSet["base"] = r.Base
	}
	dealt, taken := takeStamps(c)
	rec := costRecord(s, r.Usage, c.F(FieldRoute), c.F(FieldModel), false, dealt, taken)
	if r.Usage != "" {
		cardSet[FieldUsage] = rec
	}
	set := map[string]string{
		"head":  head,
		"brief": fx.brief,
		"fix": cutText(fmt.Sprintf("start from head %s (%s attempt %s): hold fix applied by the machine: %s",
			head, pr.ID, pr.F("attempt"), fx.said), MaxCardTextBytes),
		FieldBriefAttempt: pr.F("attempt"),
	}
	if fx.tier != "" {
		set[FieldTierNow] = fx.tier
	}
	if fx.gateHost != "" {
		set[FieldGateHost] = fx.gateHost
	}
	if fx.needs != "" {
		set["needs"] = fx.needs
	}
	if w := pr.F(FieldWho); w != "" {
		set[FieldWho] = w
	}
	// a hold fix is a next attempt: the rework policy sets the card's priority (fix for a
	// normal or low card), as the analogous in-place edits do (steps_edit.go briefInPlace).
	reworkPriority(s, pr, set)
	maps.Copy(set, finishStamps(pr, c, s.Now))
	decidedSets(r, false, pr, cardSet, set)
	addConsumer(pr, set, workConsumer(s, c, 0, "failed", rec))
	n := happened(NHoldFix, pr.Row, s.Now, pr.ID)
	n.Who, n.To, n.Attempt = who, s.Coordinator, pr.Int("attempt")
	n.What = cutText(NHoldFix+": "+fx.said, MaxCardTextBytes)
	return Unit{
		Key: c.ID, Stream: pr.Row,
		Changes: []Change{
			change(Fleet, moveEntry(c, c.Row, DoneFailed, cardSet)),
			change(Work, moveEntry(pr, pr.Row, Ready, set)),
		},
		Notes: []Note{n},
		Moved: fmt.Sprintf("%s working -> done failed; %s working -> ready at its fix (%s: %s)", c.ID, pr.ID, NHoldFix, fx.said),
	}
}
