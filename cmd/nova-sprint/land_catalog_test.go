package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mas-bandwidth/nova-tools/internal/diffcheck"
)

// catalogBase is a catalog with one existing row. A card that adds a package appends
// a row before the closing brace; two such cards conflict there, and on the map.
const catalogBase = "package docs\n\nvar DefaultCatalog = []Entry{\n\tE(\"internal/docs\", \"docs\", \"go test ./internal/docs\", \"go test ./internal/docs\"),\n}\n"

// catalogRow is a catalog row naming dir, as a card that adds that package writes it.
func catalogRow(dir string) string {
	return "\tE(\"" + dir + "\", \"a package\", \"go test ./" + dir + "\", \"go test ./" + dir + "\"),\n"
}

// withCatalogRow is the catalog with one more row, before the closing brace.
func withCatalogRow(dir string) string {
	return strings.Replace(catalogBase, "}\n", catalogRow(dir)+"}\n", 1)
}

// mapOf is a map page naming dirs, as the fake update run writes it.
func mapOf(dirs ...string) string {
	return "map\n" + strings.Join(dirs, "\n") + "\n"
}

// fakeMapRun is the agents map's update run as tools/agentsmap's is: the map is the
// catalog's rows' directories, one a line. Each run counts itself in the file count
// names, so a test reads how many runs one resolution made.
func fakeMapRun(count string) string {
	return "echo run >> " + strconv.Quote(count) + "\n" + `awk -F'"' '/E\(/ { print $2 }' internal/docs/catalog.go > AGENTS.md`
}

// catalogRig is the land rig whose agents-map family is a fake update run (the seam
// l.a.ledgers), on a base holding the catalog, its package and its map.
func catalogRig(t *testing.T, count string) *landRig {
	t.Helper()
	r := newLandRig(t)
	r.a.ledgers = []landLedger{{
		owns:  diffcheck.AgentsMap,
		roots: []string{"AGENTS.md"},
		tests: "TestCommittedMapMatchesTree",
		run:   []string{"sh", "-c", fakeMapRun(count)},
	}}
	r.git(r.worker, "switch", "-q", "--detach", "origin/main")
	r.files("the catalog", map[string]string{
		"internal/docs/catalog.go": catalogBase,
		"internal/docs/docs.go":    "package docs\n",
		"AGENTS.md":                mapOf("internal/docs"),
	})
	r.git(r.worker, "push", "-q", "origin", "HEAD:refs/heads/main")
	r.git(r.worker, "fetch", "-q", "origin")
	return r
}

// packageCard is a card's head that adds internal/dir with its catalog row and map.
func packageCard(r *landRig, id, dir string, catalog, agents string) string {
	r.t.Helper()
	return r.card(id, map[string]string{
		"internal/" + dir + "/" + dir + ".go": "package " + dir + "\n",
		"internal/docs/catalog.go":            catalog,
		"AGENTS.md":                           agents,
	})
}

// A card that adds a package owns its catalog row and the map, so both land though
// PATHS names only the package. Two such cards in one batch both land, the map family
// regenerates once, and the catalog is the union of both sides' rows, the tip's first,
// told on the card's timeline and the land log's line. An edit of an existing catalog
// row, and a map change from a card that adds no directory, land too: the catalog and
// every map are inside every card's PATHS (cardgen.AlwaysInPathsRule).
func TestLandExemptsTheCatalogRowAndMapOfANewPackage(t *testing.T) {
	t.Parallel()
	t.Run("a card adding a package with its catalog row and map lands", func(t *testing.T) {
		t.Parallel()
		r := catalogRig(t, filepath.Join(t.TempDir(), "runs"))
		brief := writeNeedsBrief(t, t.TempDir(), "s1-1", "Fix s1-1. tier: flash\nPATHS: internal/newpkg/newpkg.go", "")
		r.ok("add --stream s1 s1-1 --one --brief-file " + brief)
		head := packageCard(r, "s1-1", "newpkg", withCatalogRow("internal/newpkg"), mapOf("internal/docs", "internal/newpkg"))
		r.queued(map[string]string{"s1-1": head}, "s1-1")
		code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
		assert.Equal(t, 0, code, out+errs)
		assert.Contains(t, out, "LAND OK stream=s1 cards=1 base=main")
		assert.Equal(t, map[string]string{"s1-1": "landed/merged"}, r.places("s1-1"))
		assert.Contains(t, r.git(r.remote, "show", "main:internal/docs/catalog.go"), "internal/newpkg")
		assert.Contains(t, r.git(r.remote, "show", "main:AGENTS.md"), "internal/newpkg")
		assert.Equal(t, "package newpkg", r.git(r.remote, "show", "main:internal/newpkg/newpkg.go"))
		r.clean()
	})
	t.Run("two cards in one batch land and the map is regenerated once", func(t *testing.T) {
		t.Parallel()
		count := filepath.Join(t.TempDir(), "runs")
		r := catalogRig(t, count)
		dir := t.TempDir()
		b1 := writeNeedsBrief(t, dir, "s1-1", "Fix s1-1. tier: flash\nPATHS: internal/newpkg/newpkg.go", "")
		b2 := writeNeedsBrief(t, dir, "s1-2", "Fix s1-2. tier: flash\nPATHS: internal/otherpkg/otherpkg.go", "")
		r.ok("add --stream s1 --brief-file " + b1 + " --brief-file " + b2)
		heads := map[string]string{
			"s1-1": packageCard(r, "s1-1", "newpkg", withCatalogRow("internal/newpkg"), mapOf("internal/docs", "internal/newpkg")),
			"s1-2": packageCard(r, "s1-2", "otherpkg", withCatalogRow("internal/otherpkg"), mapOf("internal/docs", "internal/otherpkg")),
		}
		r.queued(heads, "s1-1", "s1-2")
		code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
		assert.Equal(t, 0, code, out+errs)
		assert.Contains(t, out, "LAND OK stream=s1 cards=2 base=main")
		assert.Equal(t, map[string]string{"s1-1": "landed/merged", "s1-2": "landed/merged"}, r.places("s1-1", "s1-2"))
		cat := r.git(r.remote, "show", "main:internal/docs/catalog.go")
		both := strings.Replace(catalogBase, "}\n", catalogRow("internal/newpkg")+catalogRow("internal/otherpkg")+"}\n", 1)
		assert.Equal(t, strings.TrimSuffix(both, "\n"), cat, "both sides' rows, the tip's first")
		assert.Contains(t, out, "NOTE ledger internal/docs/catalog.go: resolved as the union of both sides' added rows (+1 tip, +1 card)")
		assert.Contains(t, r.ok("card s1-2"), catalogUnionNote(), "the resolution is on the card's timeline")
		assert.NotContains(t, r.ok("card s1-1"), "resolved", "a card that merged plainly says nothing more")
		got, err := os.ReadFile(count)
		require.NoError(t, err)
		assert.Equal(t, 1, strings.Count(string(got), "run"), "the map is regenerated once")
		mapText := r.git(r.remote, "show", "main:AGENTS.md")
		assert.Contains(t, mapText, "internal/newpkg")
		assert.Contains(t, mapText, "internal/otherpkg")
		r.clean()
	})
	t.Run("an edit of an existing catalog row lands", func(t *testing.T) {
		t.Parallel()
		r := catalogRig(t, filepath.Join(t.TempDir(), "runs"))
		brief := writeNeedsBrief(t, t.TempDir(), "s1-1", "Fix s1-1. tier: flash\nPATHS: internal/newpkg/newpkg.go", "")
		r.ok("add --stream s1 s1-1 --one --brief-file " + brief)
		edited := strings.Replace(withCatalogRow("internal/newpkg"), `"docs"`, `"edited"`, 1)
		head := packageCard(r, "s1-1", "newpkg", edited, mapOf("internal/docs", "internal/newpkg"))
		r.queued(map[string]string{"s1-1": head}, "s1-1")
		code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
		assert.Equal(t, 0, code, out+errs)
		assert.NotContains(t, errs, "(E12)")
		assert.Equal(t, map[string]string{"s1-1": "landed/merged"}, r.places("s1-1"))
		assert.Contains(t, r.git(r.remote, "show", "main:internal/docs/catalog.go"), `"edited"`)
		r.clean()
	})
	t.Run("a map change without a new directory lands", func(t *testing.T) {
		t.Parallel()
		r := catalogRig(t, filepath.Join(t.TempDir(), "runs"))
		brief := writeNeedsBrief(t, t.TempDir(), "s1-1", "Fix s1-1. tier: flash\nPATHS: internal/docs/docs.go", "")
		r.ok("add --stream s1 s1-1 --one --brief-file " + brief)
		head := r.card("s1-1", map[string]string{
			"internal/docs/docs.go": "package docs // touched\n",
			"AGENTS.md":             mapOf("internal/docs", "touched"),
		})
		r.queued(map[string]string{"s1-1": head}, "s1-1")
		code, out, errs := r.do("land --repo-dir " + r.clone + " --base main")
		assert.Equal(t, 0, code, out+errs)
		assert.NotContains(t, errs, "(E12)")
		assert.Equal(t, map[string]string{"s1-1": "landed/merged"}, r.places("s1-1"))
		r.clean()
	})
}
