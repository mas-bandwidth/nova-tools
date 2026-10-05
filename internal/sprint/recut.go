package sprint

import (
	"cmp"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// tierRE matches the tier on line 1 of a brief.
// (docs/SPEC-SPRINT.md section 2, "A card replaced by its twin")
var tierRE = regexp.MustCompile(`\btier:\s*([A-Za-z0-9_-]+)`)

// SetBriefTier replaces or adds the tier on line 1 of a card's brief.
// (docs/SPEC-SPRINT.md section 2, "A card replaced by its twin")
func SetBriefTier(brief, tier string) string {
	first, rest, hasRest := strings.Cut(brief, "\n")
	if tierRE.MatchString(first) {
		first = tierRE.ReplaceAllString(first, "tier: "+tier)
	} else {
		first = strings.TrimRight(first, " ") + " tier: " + tier
	}
	if hasRest {
		return first + "\n" + rest
	}
	return first + "\n"
}

// recutTierID derives a twin ID from an old ID and a new tier.
// (docs/SPEC-SPRINT.md section 2, "A card replaced by its twin")
func recutTierID(old, tier string) string {
	for _, rt := range cardhdr.Routes {
		if strings.HasSuffix(old, "-"+rt) {
			return strings.TrimSuffix(old, "-"+rt) + "-" + tier
		}
		if strings.HasSuffix(old, "."+rt) {
			return strings.TrimSuffix(old, "."+rt) + "." + tier
		}
	}
	return old + "-" + tier
}

// RecutReq re-cuts an existing card for another tier or scope keeping id lineage
// via --replaces (docs/SPEC-SPRINT.md section 2, "A card replaced by its twin"; item 21).
type RecutReq struct {
	Old       string   // old card ID
	New       string   // optional twin ID
	Tier      string   // optional new tier
	Brief     string   // optional brief text
	BriefFile string   // optional brief file path
	Rules     string   // optional held rules file
	Needs     []string // optional explicit needs override
	Who       string   // actor
	Answers   []string // optional note IDs
}

// Recut re-cuts a card for another tier or scope keeping id lineage via --replaces,
// instead of drop plus add (docs/SPEC-SPRINT.md section 2, "A card replaced by its twin"; item 21):
// it makes the twin, relinks dependants, drops the old with the lineage recorded, on the twin store.
func Recut(s *Snapshot, r RecutReq) Plan {
	var p Plan
	p.on(s)
	refuseAll := func(why string) Plan {
		var q Plan
		q.on(s)
		q.refuse(cmp.Or(r.Old, "recut"), why)
		return q
	}
	if r.Old == "" {
		return refuseAll("recut wants one primary to re-cut")
	}
	c := s.Work.Card(r.Old)
	if c == nil {
		return refuseAll("no card " + r.Old + " on the table or off it")
	}
	if IsSentinel(c) {
		return refuseAll(r.Old + " is a sentinel, which cannot be re-cut")
	}
	if c.Placed() && c.Col == Landed {
		return refuseAll(r.Old + " landed: landed is final")
	}
	if r.Tier != "" && !cardhdr.IsRoute(r.Tier) {
		return refuseAll("--tier wants " + cardhdr.RouteList + ", found " + r.Tier)
	}
	if r.Tier == "" && r.BriefFile == "" && r.Brief == "" {
		return refuseAll("recut wants --tier <t> or --brief-file <f>")
	}

	briefText := r.Brief
	if briefText == "" {
		briefText = c.F("brief")
	}
	if r.Tier != "" {
		briefText = SetBriefTier(briefText, r.Tier)
	}

	nw := r.New
	if nw == "" && r.BriefFile != "" {
		base := strings.TrimSuffix(filepath.Base(r.BriefFile), ".md")
		if base != r.Old {
			nw = base
		}
	}
	if nw == "" {
		if r.Tier != "" {
			nw = recutTierID(r.Old, r.Tier)
		} else {
			nw = r.Old + "-recut"
		}
	}
	if nw == r.Old {
		return refuseAll(r.Old + " replaces itself")
	}

	needs := r.Needs
	if len(needs) == 0 {
		needs = without(Split(c.F("needs")), Split(c.F("waived")))
	}

	stream := c.Row
	if stream == "" {
		stream = c.F("stream")
	}
	if stream == "" {
		stream = "s1"
	}

	rules := r.Rules
	if rules == "" {
		rules = c.F(FieldRules)
	}

	who := r.Who
	if who == "" {
		who = c.F(FieldWho)
	}

	var before string
	if c.Placed() {
		before = c.ID
	}

	addReq := AddReq{
		Stream:   stream,
		IDs:      []string{nw},
		Brief:    briefText,
		Needs:    needs,
		Rules:    rules,
		Who:      who,
		Before:   before,
		Replaces: []string{c.ID},
		Answers:  r.Answers,
	}
	return Replace(s, addReq)
}
