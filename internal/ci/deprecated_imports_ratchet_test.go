package ci

import (
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/mas-bandwidth/nova-tools/internal/ci/allowlist"
	"github.com/stretchr/testify/require"
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
// repository (excluding vendor/ and testdata/), reading import blocks via AST,
// and asserts that no living package (or keep foundation test dependency) imports
// dropped deprecated packages without an allowlist entry.
//
// A keep line in pkg/pkgselect/DEPRECATED permits importing that package; it is never
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
			"A keep line in pkg/pkgselect/DEPRECATED permits importing that package; it is never permission for that package's test dependencies to import non-keep retired packages.\n"+
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
	require.NoError(t, err)
	if seed {
		t.Logf("%s is not in the merge base %s: this change is the allowlist seed", deprecatedImportsAllowlistPath, parent[:9])
		return
	}
	for _, key := range added {
		t.Errorf("%s adds the row %q, which its merge base %s does not have; the allowlist only shrinks: remove the dependency and drop the row",
			deprecatedImportsAllowlistPath, key, parent[:9])
	}
}

// measureLivingDeprecatedImports scans files in living packages (excluding .git, vendor and
// testdata) for imports of dropped deprecated packages.
func measureLivingDeprecatedImports(t *testing.T, files []*treeFile, lt *liveTree) map[string]bool {
	t.Helper()
	measured := map[string]bool{}

	for _, f := range files {
		if !f.Go || f.AST == nil {
			continue
		}
		if f.HasDirNamed(".git") || f.HasDirNamed("vendor") || f.HasDirNamed("testdata") {
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
			require.NoError(t, err, "unquote import path %s in %s: %v", spec.Path.Value, relSlash, err)
			if isDroppedDeprecated(lt, importedPkg) {
				edge := pkgPath + " -> " + cleanPkgPath(importedPkg)
				measured[edge] = true
			}
		}
	}
	return measured
}
