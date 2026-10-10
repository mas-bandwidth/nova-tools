package sprint

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/cardhdr"
)

// A card whose PATHS name TLA+ model work (a .tla module or an MC config under tla/) is
// tiered frontier at add (the owner, 2026-10-04: "When we do TLA+ modeling work, I would
// like that to go to frontier models."; docs/SPEC-SPRINT.md, the card decides its model):
// one that names no tier is admitted with tier: frontier on its line 1, one that names
// frontier is admitted as written, one that names a lower tier is refused with the reason,
// and one whose tla/ entries are only the TLC run records (tla/RUNS.tsv, tla/CASES.tsv)
// keeps its own tier, since running the checker is mechanical.
func TestACardThatWritesAModelIsTieredFrontier(t *testing.T) {
	t.Parallel()
	brief := func(line1, paths string) string {
		return line1 + "\nREPO: mas-bandwidth/nova-tools\nPATHS: " + paths + "\nTEST: ./internal/x TestX\n\nThe task."
	}
	w := newWorld(t)
	p := Add(w.s, AddReq{Stream: "s", Cards: []CardAdd{
		{ID: "m1", Brief: brief("m1: a lease model", "tla/Lease.tla,tla/MCLease.cfg,internal/x/*.go")},
		{ID: "m2", Brief: brief("m2: a friend model tier: frontier", "internal/friend/tla/MCLaneEnd.cfg")},
		{ID: "m3", Brief: brief("m3: every model", "tla/**")},
		{ID: "r1", Brief: brief("r1: a run record", "tla/RUNS.tsv,tla/CASES.tsv,internal/x/*.go")},
		{ID: "g1", Brief: brief("g1: go only tier: pro", "internal/x/*.go")},
	}})
	require.Empty(t, p.Refused)
	w.do(p)
	for id, want := range map[string]string{"m1": cardhdr.RouteFrontier, "m2": cardhdr.RouteFrontier, "m3": cardhdr.RouteFrontier, "r1": cardhdr.RouteFlash, "g1": cardhdr.RoutePro} {
		c := w.s.Primary(id)
		require.NotNil(t, c, id)
		now, ceiling := CardTiers(c)
		assert.Equal(t, want, now, "%s is dealt on %s", id, want)
		assert.Equal(t, want, ceiling, "%s is capped at %s", id, want)
	}
	// The tier writer stamps only a RESULT line (brief_tier.go): m1's line 1 is a title, so
	// its brief is unchanged and its own tier field carries frontier (steps_work.go).
	m1 := w.s.Primary("m1").F("brief")
	assert.Equal(t, brief("m1: a lease model", "tla/Lease.tla,tla/MCLease.cfg,internal/x/*.go"), m1, "a title is never stamped")
	assert.Equal(t, cardhdr.RouteFrontier, w.s.Primary("m1").F(FieldTier), "the card's tier field carries frontier")
	assert.Equal(t, cardhdr.RouteFrontier, TierWord(w.s.Primary("m2")), "m2 names frontier on line 1")
	assert.Equal(t, cardhdr.RouteFlash, TierWord(w.s.Primary("m1")), "the tier word reads line 1, which carries no tier")
	assert.Equal(t, brief("m2: a friend model tier: frontier", "internal/friend/tla/MCLaneEnd.cfg"), w.s.Primary("m2").F("brief"), "a frontier brief is admitted as written")
	assert.Equal(t, brief("r1: a run record", "tla/RUNS.tsv,tla/CASES.tsv,internal/x/*.go"), w.s.Primary("r1").F("brief"), "run records alone are no model")
	moved := map[string]string{}
	for _, u := range p.Units {
		moved[u.Key] = u.Moved
	}
	assert.Contains(t, moved["m1"], "tiered frontier: PATHS name TLA+ model work (tla/Lease.tla,tla/MCLease.cfg)")
	assert.NotContains(t, moved["m2"], "tiered frontier")

	for _, tier := range []string{cardhdr.RouteFlash, cardhdr.RoutePro, cardhdr.RouteHeavy} {
		p := Add(w.s, AddReq{Stream: "s", Cards: []CardAdd{{ID: "low-" + tier, Brief: brief("low: a model tier: "+tier, "tla/Lease.tla")}}})
		require.Len(t, p.Refused, 1, tier)
		why := p.Refused[0].Why
		assert.Contains(t, why, "PATHS name TLA+ model work (tla/Lease.tla)", tier)
		assert.Contains(t, why, "line 1 names tier "+tier, tier)
		assert.Contains(t, why, "write tier: frontier on line 1", tier)
		assert.Empty(t, p.Units, "nothing admitted on %s", tier)
	}
	assert.Empty(t, Check(w.s, nil))
}

// ModelPaths reads which PATHS entries can name a model: a .tla module anywhere, an MC
// config or the directory itself under a tla/ directory, and a glob that matches either;
// never a run record, a Go file or a doc.
func TestModelPathsNameModulesAndConfigsNotRunRecords(t *testing.T) {
	t.Parallel()
	for entry, want := range map[string]bool{
		"tla/Lease.tla":                       true,
		"tla/MCLease.cfg":                     true,
		"tla/MC*.cfg":                         true,
		"tla/*.tla":                           true,
		"tla/**":                              true,
		"tla/":                                true,
		"tla":                                 true,
		"internal/friend/tla/LaneEnd.tla":     true,
		"internal/friend/tla/*":               true,
		"**/*.tla":                            true,
		"security/**":                         false,
		"**":                                  false,
		"internal/x/*":                        false,
		"tla/RUNS.tsv":                        false,
		"tla/CASES.tsv":                       false,
		"tla/*.tsv":                           false,
		"tla/README.md":                       false,
		"internal/tlaplus/*.go":               false,
		"internal/x/config.cfg":               false,
		"docs/SPEC-SPRINT.md":                 false,
		"cmd/nova-tla/*.go":                   false,
		"internal/friend/tla/LaneEnd_test.go": false,
	} {
		assert.Equal(t, want, len(ModelPaths([]string{entry})) == 1, entry)
	}
}

// A model brief whose first line is a header carries no RESULT line for the tier writer to
// stamp (brief_tier.go), so the card's own tier field carries frontier and the add persists
// it (steps_work.go): the card keeps the frontier tier its model work requires instead of
// falling back to flash (the review of attempt 4). The header lines are read as written.
func TestAHeaderFirstModelCardKeepsTheFrontierTier(t *testing.T) {
	t.Parallel()
	brief := "PATHS: tla/Lease.tla,internal/x/*.go\n" +
		"REPO: mas-bandwidth/nova-tools\n" +
		"BASE: sprint/mechanical-2026-10-02\n" +
		"TEST: ./internal/x TestX\n" +
		"\nTHE TASK. Fix the lease model."
	w := newWorld(t)
	p := Add(w.s, AddReq{Stream: "s", Cards: []CardAdd{{ID: "m1", Brief: brief}}})
	require.Empty(t, p.Refused)
	w.do(p)
	c := w.s.Primary("m1")
	require.NotNil(t, c)
	assert.Equal(t, brief, c.F("brief"), "the header lines are read as written")
	assert.Equal(t, cardhdr.RouteFrontier, c.F(FieldTier), "the card's tier field carries frontier")
	now, ceiling := CardTiers(c)
	assert.Equal(t, cardhdr.RouteFrontier, now)
	assert.Equal(t, cardhdr.RouteFrontier, ceiling)
	moved := map[string]string{}
	for _, u := range p.Units {
		moved[u.Key] = u.Moved
	}
	assert.Contains(t, moved["m1"], "tiered frontier: PATHS name TLA+ model work (tla/Lease.tla)")
}
