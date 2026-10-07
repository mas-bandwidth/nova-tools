package docs

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCommittedMapMatchesTree is the docs-guard: a mapped directory that
// grows a child, a catalog row that moves, or a hand-edit of a generated
// page all fail here until `make map` rewrites the pages.
func TestCommittedMapMatchesTree(t *testing.T) {
	t.Parallel()

	root := testRoot(t)
	issues := Check(root, DefaultCatalog)
	for _, s := range issues {
		t.Error(s)
	}
}

func TestRootMapStaysUnderTheByteCap(t *testing.T) {
	t.Parallel()

	root := testRoot(t)
	pages, _ := Render(root, DefaultCatalog)
	n := len(pages[RootAgents])
	assert.Less(t, n, MaxRootBytes, "%s is %d bytes, over the %d-byte cap; a harness reads this page at every session start — shorten the catalog rows or the standard", RootAgents, n, MaxRootBytes)
}

func TestRootMapIsAFourColumnTable(t *testing.T) {
	t.Parallel()

	root := testRoot(t)
	pages, _ := Render(root, DefaultCatalog)
	body := pages[RootAgents]
	require.Contains(t, body, "| dir | purpose | guard | command |", "%s is not a directory → purpose → guard → command map", RootAgents)
	assert.Contains(t, body, "docs/STANDARD.md", "%s does not point at the standard in docs/STANDARD.md", RootAgents)
	assert.Contains(t, body, "docs/CONTRIBUTING.md", "%s does not point at how review goes in docs/CONTRIBUTING.md", RootAgents)
}

func TestCatalogRoutesAndGuardsAreValid(t *testing.T) {
	t.Parallel()

	root := testRoot(t)
	specRe := regexp.MustCompile(`\b(SPEC(?:-[A-Z0-9]+)*\.md)\b`)

	for _, e := range DefaultCatalog {
		dirPath := filepath.Join(root, filepath.FromSlash(e.Path))
		st, err := os.Stat(dirPath)
		assert.True(t, err == nil && st.IsDir(), "catalog path %s does not exist on disk", e.Path)

		if strings.HasPrefix(e.Guard, "go test ./") {
			pkgPath := strings.TrimPrefix(e.Guard, "go test ./")
			pkgPath = strings.TrimSuffix(pkgPath, "/...")
			absPkg := filepath.Join(root, filepath.FromSlash(pkgPath))
			pst, statErr := os.Stat(absPkg)
			assert.True(t, statErr == nil && pst.IsDir(), "catalog entry %s has guard %q pointing to missing package %s", e.Path, e.Guard, pkgPath)
		}

		for _, m := range specRe.FindAllStringSubmatch(e.Purpose, -1) {
			specFile := m[1]
			specPath := filepath.Join(root, "docs", specFile)
			_, statErr := os.Stat(specPath)
			assert.NoError(t, statErr, "catalog entry %s purpose mentions spec %s which does not exist in docs/", e.Path, specFile)
		}
	}
}

// TestStaleMapFailsUntilRegenerate is the red-first blade: edit a mapped
// directory, the guard goes red, regenerate, the guard goes green.
func TestStaleMapFailsUntilRegenerate(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "Makefile"), []byte("# test tree\n"), 0o644))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "alpha"), 0o755))
	require.NoError(t, os.Mkdir(filepath.Join(dir, "docs"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, filepath.FromSlash(StandardDoc)), []byte("# The standard\n\nA rule.\n\n"+classRulesStart+"\n\nold names\n\n"+classRulesEnd+"\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, filepath.FromSlash(classRulesDoc)), []byte("## The class tests\n### `first` — first rule\n"), 0o644))
	cat := []Entry{
		E("docs", "the standard", "go test", "go test"),
		Page("alpha", "the mapped tree", "go test", "go test"),
	}
	pages, issues := Render(dir, cat)
	require.Empty(t, issues, "fresh tree: %s", strings.Join(issues, "; "))
	require.NoError(t, Write(dir, pages))
	issues = Check(dir, cat)
	require.Empty(t, issues, "committed map should be green: %s", strings.Join(issues, "; "))

	// Adding and then removing an indexed rule stales both generated copies.
	for _, spec := range []string{
		"## The class tests\n### `second` — second rule\n### `first` — first rule\n",
		"## The class tests\n### `second` — second rule\n",
	} {
		require.NoError(t, os.WriteFile(filepath.Join(dir, filepath.FromSlash(classRulesDoc)), []byte(spec), 0o644))
		issues := Check(dir, cat)
		for _, path := range []string{StandardDoc, RootAgents} {
			require.True(t, hasIssue(issues, path+" is stale; run: make map"), "changed rule must stale %s: %v", path, issues)
		}
		pages, issues := Render(dir, cat)
		require.Empty(t, issues, "render changed rules")
		require.NoError(t, Write(dir, pages))
		require.Empty(t, Check(dir, cat), "one generation must repair both rule lists")
	}

	standard, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(StandardDoc)))
	require.NoError(t, err)
	handList := strings.Replace(string(standard), "`second`.", "`hand-edited`.", 1)
	require.NoError(t, os.WriteFile(filepath.Join(dir, filepath.FromSlash(StandardDoc)), []byte(handList), 0o644))
	issues = Check(dir, cat)
	require.True(t, hasIssue(issues, StandardDoc+" is stale; run: make map"), "hand-edited list must be stale: %v", issues)
	require.NoError(t, os.WriteFile(filepath.Join(dir, filepath.FromSlash(StandardDoc)), standard, 0o644))

	require.NoError(t, os.Mkdir(filepath.Join(dir, "alpha", "beta"), 0o755))
	red := Check(dir, cat)
	require.True(t, hasIssue(red, "uncatalogued directory alpha/beta"), "new child without a catalog row should be uncatalogued, got:\n%s", strings.Join(red, "\n"))
	require.True(t, hasIssue(red, "stale; run: make map"), "new child without regenerating should stale the page, got:\n%s", strings.Join(red, "\n"))

	cat = append(cat, E("alpha/beta", "a child", "go test", "go test"))
	stale := Check(dir, cat)
	require.False(t, hasIssue(stale, "uncatalogued"), "catalogued child should not be uncatalogued, got:\n%s", strings.Join(stale, "\n"))
	require.True(t, hasIssue(stale, "stale; run: make map"), "catalogued child without regenerating should still be stale, got:\n%s", strings.Join(stale, "\n"))

	pages, issues = Render(dir, cat)
	require.Empty(t, issues, "after catalog row: %s", strings.Join(issues, "; "))
	require.NoError(t, Write(dir, pages))
	issues = Check(dir, cat)
	require.Empty(t, issues, "after make map the guard should be green, got:\n%s", strings.Join(issues, "\n"))

	require.NoError(t, os.WriteFile(filepath.Join(dir, "alpha", "beta", "AGENTS.md"), []byte("hand-written\n"), 0o644))
	hand := Check(dir, cat)
	require.True(t, hasIssue(hand, "not generated by internal/docs"), "a hand-written AGENTS.md should fail, got:\n%s", strings.Join(hand, "\n"))
}

func hasIssue(issues []string, substr string) bool {
	for _, s := range issues {
		if strings.Contains(s, substr) {
			return true
		}
	}
	return false
}

func testRoot(t *testing.T) string {
	t.Helper()
	root, err := RepoRoot()
	require.NoError(t, err)
	return root
}

// TestRootMapCarriesTheWholeStandard holds the owner's rule: everything someone
// needs to know while building or working on the tools is in the root page, so
// the page embeds docs/STANDARD.md whole.
func TestRootMapCarriesTheWholeStandard(t *testing.T) {
	t.Parallel()

	root := testRoot(t)
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(StandardDoc)))
	require.NoError(t, err, "%s: %v; the standard is the one source the root page embeds", StandardDoc, err)
	pages, _ := Render(root, DefaultCatalog)
	body := pages[RootAgents]
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		assert.Contains(t, body, line, "%s as rendered does not carry this line of %s: %q; the embed in agentsmap.go drops this line (embedStandard)", RootAgents, StandardDoc, line)
	}
}
