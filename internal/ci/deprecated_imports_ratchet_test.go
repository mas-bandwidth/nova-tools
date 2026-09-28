package ci

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
)

// deprecatedImportsAllowlistPath is the shrink-only exception list for dependencies
// on dropped deprecated packages.
const deprecatedImportsAllowlistPath = "testdata/deprecated-imports-allowlist.txt"

// parseDeprecatedImportEdgeKey normalizes an allowlist row to `<importer> -> <imported>`.
func parseDeprecatedImportEdgeKey(row string) string {
	line, _, _ := strings.Cut(row, "#")
	line = strings.TrimSpace(line)
	fields := strings.Fields(line)
	if len(fields) >= 3 && fields[1] == "->" {
		return fields[0] + " -> " + fields[2]
	}
	if len(fields) >= 2 {
		return fields[0] + " -> " + fields[1]
	}
	if len(fields) == 1 {
		if parts := strings.Split(fields[0], "->"); len(parts) == 2 {
			return strings.TrimSpace(parts[0]) + " -> " + strings.TrimSpace(parts[1])
		}
		if parts := strings.Split(fields[0], ":"); len(parts) == 2 {
			return strings.TrimSpace(parts[0]) + " -> " + strings.TrimSpace(parts[1])
		}
	}
	return ""
}

func isDroppedDeprecated(lt *liveTree, p string) bool {
	p = strings.TrimPrefix(p, "github.com/mas-bandwidth/nova-tools/")
	p = strings.TrimPrefix(filepath.ToSlash(p), "./")
	if lt.keep[p] {
		return false
	}
	if p == "deprecated" || strings.HasPrefix(p, "deprecated/") {
		return true
	}
	for _, d := range lt.drop {
		if p == d || strings.HasPrefix(p, d+"/") {
			return true
		}
	}
	return false
}

func cleanPkgPath(p string) string {
	const mod = "github.com/mas-bandwidth/nova-tools/"
	p = strings.TrimPrefix(p, mod)
	p = strings.TrimPrefix(filepath.ToSlash(p), "./")
	if idx := strings.Index(p, " ["); idx != -1 {
		p = p[:idx]
	}
	p = strings.TrimSuffix(p, ".test")
	p = strings.TrimSuffix(p, "_test")
	return p
}

// TestLivingPackagesDoNotImportDroppedDeprecatedPackages walks every .go file in the
// repository (excluding deprecated/ and vendor/), reading import blocks via AST,
// and asserts that no living package (or keep foundation test dependency) imports
// dropped deprecated packages without an allowlist entry.
//
// A keep line in deprecated/PACKAGES permits importing that package; it is never
// permission for that package's test dependencies to import non-keep retired packages.
func TestLivingPackagesDoNotImportDroppedDeprecatedPackages(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	lt := loadLiveTree(t, root)

	allow := loadAllowlist(t, deprecatedImportsAllowlistPath, allowlist.Options{
		Ceiling: true,
		Key:     parseDeprecatedImportEdgeKey,
	})

	tree := repoTree(t)
	measured := measureLivingDeprecatedImports(t, tree.Files, lt)

	res := allowlist.Check(t, allow, measured)
	for _, row := range res.Stale {
		t.Errorf("%s lists %s, but no living or kept package depends on it any more; delete the stale entry (the ratchet only shrinks)",
			deprecatedImportsAllowlistPath, row.Key)
	}
	for _, unlisted := range res.Unlisted {
		t.Errorf("%s: unlisted dependency on dropped deprecated package: %s\n"+
			"A living package (or keep foundation test dependency) must not import dropped deprecated packages without an allowlist entry.\n"+
			"A keep line in deprecated/PACKAGES permits importing that package; it is never permission for that package's test dependencies to import non-keep retired packages.\n"+
			"Remedy: lift the target package into a shared module, decouple the test fixture, or remove the dependency; the allowlist only shrinks and refuses new rows.",
			deprecatedImportsAllowlistPath, unlisted)
	}
}

// deprecatedImportsAllowlistBase finds the commit the allowlist is compared against.
// When refs/remotes/origin/dev is present, it uses the merge base of HEAD and origin/dev.
// If HEAD is on origin/dev itself (e.g. after landing on dev), it compares against
// HEAD's first parent (HEAD~1). If refs/remotes/origin/dev is missing, it refuses
// loudly to fail closed, whether HEAD is a single commit or a merge commit.
func deprecatedImportsAllowlistBase(root string) (string, error) {
	if _, err := gitOut(root, "rev-parse", "--verify", "-q", "refs/remotes/origin/dev^{commit}"); err != nil {
		return "", fmt.Errorf("cannot verify allowlist growth: refs/remotes/origin/dev is missing; fetch origin/dev")
	}
	mb, mbErr := gitOut(root, "merge-base", "HEAD", "refs/remotes/origin/dev")
	head, headErr := gitOut(root, "rev-parse", "HEAD")
	if mbErr == nil && headErr == nil && strings.TrimSpace(mb) != strings.TrimSpace(head) {
		return strings.TrimSpace(mb), nil
	}
	return firstParent(root)
}

func deprecatedImportsAllowlistGrowth(root string) (added []string, parent string, seed bool, err error) {
	parent, err = deprecatedImportsAllowlistBase(root)
	if err != nil {
		return nil, "", false, err
	}
	const relPath = "internal/ci/" + deprecatedImportsAllowlistPath
	if _, err := gitOut(root, "cat-file", "-e", parent+":"+relPath); err != nil {
		return nil, parent, true, nil
	}
	baseText, err := gitOut(root, "show", parent+":"+relPath)
	if err != nil {
		return nil, parent, false, err
	}
	baseList, err := allowlist.Parse(relPath, baseText, allowlist.Options{
		Ceiling: true,
		Key:     parseDeprecatedImportEdgeKey,
	})
	if err != nil {
		return nil, parent, false, fmt.Errorf("%s at %s: %w", relPath, parent[:9], err)
	}
	headList, err := allowlist.Load(filepath.Join(root, filepath.FromSlash(relPath)), allowlist.Options{
		Ceiling: true,
		Key:     parseDeprecatedImportEdgeKey,
	})
	if err != nil {
		return nil, parent, false, fmt.Errorf("%s at HEAD: %w", relPath, err)
	}
	for _, row := range headList.Rows() {
		if !baseList.Has(row.Key) {
			added = append(added, row.Key)
		}
	}
	sort.Strings(added)
	return added, parent, false, nil
}

// TestDeprecatedImportsAllowlistOnlyShrinksAgainstMergeBase asserts that HEAD
// adds no row to deprecated-imports-allowlist.txt that its merge base lacks.
func TestDeprecatedImportsAllowlistOnlyShrinksAgainstMergeBase(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	added, parent, seed, err := deprecatedImportsAllowlistGrowth(root)
	if err != nil {
		t.Fatal(err)
	}
	if seed {
		t.Logf("%s is not in the merge base %s: this change is the allowlist seed", deprecatedImportsAllowlistPath, parent[:9])
		return
	}
	for _, key := range added {
		t.Errorf("%s adds the row %q, which its merge base %s does not have; the allowlist only shrinks: remove the dependency and drop the row",
			deprecatedImportsAllowlistPath, key, parent[:9])
	}
}

// TestDeprecatedImportsAllowlistGrowthIsReadOutOfGit exercises the growth detection
// over a synthetic git repository to prove that adding a row fails, deleting one passes,
// and a base lacking the allowlist is recognized as the seed.
func TestDeprecatedImportsAllowlistGrowthIsReadOutOfGit(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := gitOut(root, append([]string{
			"-c", "user.name=ci", "-c", "user.email=ci@example.invalid",
			"-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null"}, args...)...)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(out)
	}
	write := func(rel, text string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	growth := func() ([]string, bool) {
		t.Helper()
		added, _, seed, err := deprecatedImportsAllowlistGrowth(root)
		if err != nil {
			t.Fatal(err)
		}
		return added, seed
	}

	const relPath = "internal/ci/" + deprecatedImportsAllowlistPath

	git("init", "-q")
	write("README", "base\n")
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	baseCommit := git("rev-parse", "HEAD")
	git("update-ref", "refs/remotes/origin/dev", baseCommit)

	// Commit seed to create a branch commit ahead of origin/dev
	write(relPath, "cmd/a -> internal/nsprint/ws  # seed\n")
	git("add", "-A")
	git("commit", "-q", "-m", "seed")

	// PROBE 0: a parent with no allowlist is the seed.
	if added, seed := growth(); !seed || len(added) != 0 {
		t.Fatalf("parent with no allowlist: added %v seed %v; want the seed", added, seed)
	}

	// Advance origin/dev to the seed commit
	seedCommit := git("rev-parse", "HEAD")
	git("update-ref", "refs/remotes/origin/dev", seedCommit)

	// Commit an edit on top of seedCommit so HEAD is ahead of origin/dev
	write("README", "touched\n")
	git("add", "-A")
	git("commit", "-q", "-m", "touch")

	// PROBE 1: matching allowlist is clean.
	if added, seed := growth(); seed || len(added) != 0 {
		t.Fatalf("matching row: added %v seed %v; want nothing added", added, seed)
	}

	// PROBE 2: adding a row is detected as growth.
	write(relPath, "cmd/a -> internal/nsprint/ws  # seed\ncmd/b -> internal/nsprint/card  # new\n")
	if added, _ := growth(); len(added) != 1 || added[0] != "cmd/b -> internal/nsprint/card" {
		t.Fatalf("added row: added %q; want [cmd/b -> internal/nsprint/card]", added)
	}

	// PROBE 3: deleting a row is shrinking, so added is empty.
	write(relPath, "# all rows deleted\n")
	if added, _ := growth(); len(added) != 0 {
		t.Fatalf("deleted row: added %q; want none", added)
	}
}

// TestDeprecatedImportsAllowlistGrowthRefusesStaleOrMissingBase verifies that
// missing refs/remotes/origin/dev refuses loudly (for both linear and merge commits),
// and does not fall back to firstParent.
func TestDeprecatedImportsAllowlistGrowthRefusesStaleOrMissingBase(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		out, err := gitOut(root, append([]string{
			"-c", "user.name=ci", "-c", "user.email=ci@example.invalid",
			"-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null"}, args...)...)
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(out)
	}
	write := func(rel, text string) {
		t.Helper()
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	const relPath = "internal/ci/" + deprecatedImportsAllowlistPath

	git("init", "-q")
	write("README", "base\n")
	write(relPath, "cmd/a -> internal/nsprint/ws  # base\n")
	git("add", "-A")
	git("commit", "-q", "-m", "base")
	baseCommit := git("rev-parse", "HEAD")

	// CASE 1: missing origin/dev on linear commit refuses loudly.
	_, _, _, err := deprecatedImportsAllowlistGrowth(root)
	if err == nil || !strings.Contains(err.Error(), "refs/remotes/origin/dev is missing") {
		t.Fatalf("missing origin/dev on linear commit: got %v, want 'refs/remotes/origin/dev is missing'", err)
	}

	// CASE 2: missing origin/dev on merge commit refuses loudly (fail closed).
	currentBranch := git("branch", "--show-current")
	git("checkout", "-qb", "side")
	write("SIDE", "side\n")
	git("add", "-A")
	git("commit", "-q", "-m", "side commit")
	git("checkout", "-q", currentBranch)
	git("merge", "-q", "--no-ff", "-m", "merge side", "side")
	_, _, _, err = deprecatedImportsAllowlistGrowth(root)
	if err == nil || !strings.Contains(err.Error(), "refs/remotes/origin/dev is missing") {
		t.Fatalf("missing origin/dev on merge commit: got %v, want 'refs/remotes/origin/dev is missing'", err)
	}

	// CASE 3: when origin/dev is restored on merge commit, growth check works against merge base.
	git("update-ref", "refs/remotes/origin/dev", baseCommit)
	write(relPath, "cmd/a -> internal/nsprint/ws  # base\ncmd/b -> internal/nsprint/card  # new\n")
	added, _, seed, err := deprecatedImportsAllowlistGrowth(root)
	if err != nil || seed || len(added) != 1 || added[0] != "cmd/b -> internal/nsprint/card" {
		t.Fatalf("restored origin/dev: added %v seed %v err %v; want [cmd/b -> internal/nsprint/card]", added, seed, err)
	}
}

// measureLivingDeprecatedImports scans files in living packages (excluding .git, vendor, testdata,
// and root deprecated/) for imports of dropped deprecated packages.
func measureLivingDeprecatedImports(t *testing.T, files []*treeFile, lt *liveTree) map[string]bool {
	t.Helper()
	measured := map[string]bool{}

	for _, f := range files {
		if !f.Go || f.AST == nil {
			continue
		}
		if f.HasDirNamed(".git") || f.HasDirNamed("vendor") || f.HasDirNamed("testdata") || f.InDir("deprecated") {
			continue
		}
		relSlash := f.Rel
		pkgPath := filepath.ToSlash(filepath.Dir(relSlash))
		if pkgPath == "." {
			pkgPath = ""
		}
		if isDroppedDeprecated(lt, pkgPath) {
			continue
		}

		for _, spec := range f.AST.Imports {
			if spec.Path == nil {
				continue
			}
			importedPkg, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatalf("unquote import path %s in %s: %v", spec.Path.Value, relSlash, err)
			}
			if isDroppedDeprecated(lt, importedPkg) {
				edge := pkgPath + " -> " + cleanPkgPath(importedPkg)
				measured[edge] = true
			}
		}
	}
	return measured
}

// TestDeprecatedImportsRatchetExaminesNestedDeprecatedDirectories verifies that
// the deprecated imports ratchet does not exclude nested directories named "deprecated"
// (like cmd/live/deprecated/), only the root deprecated/ directory.
func TestDeprecatedImportsRatchetExaminesNestedDeprecatedDirectories(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	for rel, body := range map[string]string{
		"deprecated/cmd/old/main.go":    "package old\n",
		"cmd/live/deprecated/nested.go": "package nested\n\nimport _ \"github.com/mas-bandwidth/nova-tools/deprecated/cmd/old\"\n",
	} {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	tree, err := loadRepoTree(root)
	if err != nil {
		t.Fatalf("loadRepoTree: %v", err)
	}

	lt := &liveTree{
		root: root,
		drop: []string{"deprecated"},
		keep: map[string]bool{},
	}

	measured := measureLivingDeprecatedImports(t, tree.Files, lt)
	wantEdge := "cmd/live/deprecated -> deprecated/cmd/old"
	if !measured[wantEdge] {
		t.Errorf("measured = %v, want edge %q; nested deprecated directories must be examined", measured, wantEdge)
	}
}
