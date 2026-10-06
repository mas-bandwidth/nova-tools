package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The read tier follows what the change touches (readers.go, ChangeKind and
// readTierChoice; docs/SPEC-SPRINT.md section 6, the read tier by kind): a heavy card
// whose PATHS are Markdown and text only is read at pro, on a pro route, and its read
// card and cost record name the tier and why.
func TestAProseOnlyChangeIsReadAtPro(t *testing.T) {
	t.Parallel()
	w := newWorld(t, "reader-a", "reader-b")
	for _, m := range []string{"m1", "m2"} {
		w.must(FleetStep(w.s, FleetReq{Op: "up", Member: m}))
	}
	for _, r := range []struct{ name, tier string }{{"pro-a", "pro"}, {"heavy-a", "heavy"}} {
		w.s.Routes = append(w.s.Routes, Route{Name: r.name, Tier: r.tier, Provider: "p", Model: r.name, Tokens: 1000, Enabled: true})
	}
	w.must(Add(w.s, AddReq{Stream: "s1", IDs: []string{"s1-1"}, Brief: "tier: heavy\nPATHS: docs/SPEC-SPRINT.md,notes/*.txt"}))
	w.s.Work.Card("s1-1").Fields[FieldTierNow] = "heavy"
	toReview(w, "s1-1")
	w.must(Ask(w.s, AskReq{Sel: Sel{IDs: []string{"s1-1"}}}))
	reads := liveReadsAt(w.s, w.s.Work.Card("s1-1"), 1)
	require.Len(t, reads, 1)
	rc := reads[0]
	assert.Equal(t, "pro", rc.F(FieldTier), "a prose-only head at a heavy card is read at pro")
	assert.Equal(t, "pro", routeTier(w.s, rc.F(FieldRoute)))
	assert.Equal(t, "prose:read_tier_prose=pro", rc.F(FieldTierWhy))
	rec := readConsumer(w.s, rc, 0, "ok", "")
	assert.Equal(t, "pro", rec.Tier)
	assert.Equal(t, "prose:read_tier_prose=pro", rec.TierWhy)
	assert.Contains(t, rec.line(), "tier_why=prose:read_tier_prose=pro")
	assert.Equal(t, "prose:read_tier_prose=pro", parseConsumer(rec.Key, rec.line()).TierWhy)
}

// The rule by kind, each default and each override: prose at pro, code at the card's
// tier, tla/ at frontier (collapsed onto heavy for a machine's route, a friend's read
// before it); nova-config's sprint row moves each; a brief's READ-TIER pins it; a stream's
// or the sprint's read tier still raises it and never lowers it.
func TestTheReadTierFollowsTheCardKind(t *testing.T) {
	t.Parallel()
	card := func(brief string) *Card {
		return &Card{ID: "s1-1", Row: "s1", Fields: map[string]string{"kind": "primary", "brief": brief}}
	}
	for _, tc := range []struct {
		name, brief, sprintSet string
		rule                   ReadTierByKind
		want, collapsed, why   string
	}{
		{"prose at a heavy card", "tier: heavy\nPATHS: docs/A.md,README.md", "", ReadTierByKind{}, "pro", "pro", "prose:read_tier_prose=pro"},
		{"prose at a frontier card", "tier: frontier\nPATHS: docs/**/*.md", "", ReadTierByKind{}, "pro", "pro", "prose:read_tier_prose=pro"},
		{"prose at a flash card", "PATHS: docs/A.txt", "", ReadTierByKind{}, "pro", "pro", "prose:read_tier_prose=pro"},
		{"prose set to the card's tier", "tier: heavy\nPATHS: docs/A.md", "", ReadTierByKind{Prose: ReadTierCard}, "heavy", "heavy", "prose:read_tier_prose=card"},
		{"code at a heavy card", "tier: heavy\nPATHS: internal/x.go,docs/A.md", "", ReadTierByKind{}, "heavy", "heavy", "code:read_tier_code=card"},
		{"code at a flash card", "PATHS: internal/x.go", "", ReadTierByKind{}, "flash", "flash", "code:read_tier_code=card"},
		{"a directory is code", "tier: pro\nPATHS: docs/", "", ReadTierByKind{}, "pro", "pro", "code:read_tier_code=card"},
		{"no PATHS is code", "tier: pro", "", ReadTierByKind{}, "pro", "pro", "code:read_tier_code=card"},
		{"code set to pro", "tier: heavy\nPATHS: x.go", "", ReadTierByKind{Code: "pro"}, "pro", "pro", "code:read_tier_code=pro"},
		{"tla at a flash card", "PATHS: tla/Lease.tla,internal/x.go", "", ReadTierByKind{}, "frontier", "heavy", "tla:read_tier_tla=frontier"},
		{"tla set to the card's tier", "tier: pro\nPATHS: tla/Lease.tla", "", ReadTierByKind{TLA: ReadTierCard}, "pro", "pro", "tla:read_tier_tla=card"},
		{"a pin over the kind", "tier: heavy\nREAD-TIER: flash\nPATHS: internal/x.go", "", ReadTierByKind{}, "flash", "flash", "pin:READ-TIER=flash"},
		{"a pin not a tier is no pin", "tier: heavy\nREAD-TIER: cheap\nPATHS: docs/A.md", "", ReadTierByKind{}, "pro", "pro", "prose:read_tier_prose=pro"},
		{"the sprint's read tier raises prose", "tier: heavy\nPATHS: docs/A.md", "heavy", ReadTierByKind{}, "heavy", "heavy", "prose:read_tier_prose=pro,raised:read_tier=heavy"},
		{"the sprint's read tier lowers nothing", "tier: heavy\nPATHS: x.go", "pro", ReadTierByKind{}, "heavy", "heavy", "code:read_tier_code=card"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := &Snapshot{Work: NewTable(Work), Merge: NewTable(Merge), ReadTierByKind: tc.rule}
			if tc.sprintSet != "" {
				s.Work.SetProps(map[string]string{PropReadTier: tc.sprintSet})
			}
			got, why := s.readTierChoice(card(tc.brief))
			assert.Equal(t, tc.want, got)
			assert.Equal(t, tc.why, why)
			assert.Equal(t, tc.collapsed, s.readTierOf(card(tc.brief)))
		})
	}
}
