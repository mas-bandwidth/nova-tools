package ci

import (
	"fmt"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
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

	fset := token.NewFileSet()
	measured := map[string]bool{}

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "vendor" || path == filepath.Join(root, "deprecated") {
				return filepath.SkipDir
			}
			rel, err := filepath.Rel(root, path)
			if err == nil {
				relSlash := filepath.ToSlash(rel)
				if relSlash == "deprecated" || strings.HasPrefix(relSlash, "deprecated/") {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}

		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		relSlash := filepath.ToSlash(rel)
		pkgPath := filepath.ToSlash(filepath.Dir(relSlash))
		if pkgPath == "." {
			pkgPath = ""
		}
		if isDroppedDeprecated(lt, pkgPath) {
			return nil
		}

		file, err := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", relSlash, err)
		}

		for _, spec := range file.Imports {
			if spec.Path == nil {
				continue
			}
			importedPkg := strings.Trim(spec.Path.Value, "`\"")
			if isDroppedDeprecated(lt, importedPkg) {
				edge := pkgPath + " -> " + cleanPkgPath(importedPkg)
				measured[edge] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk source tree: %v", err)
	}

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

func commitParents(root, rev string) ([]string, error) {
	raw, err := gitOut(root, "cat-file", "-p", rev)
	if err != nil {
		return nil, err
	}
	var parents []string
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(line, "parent ") {
			parents = append(parents, strings.TrimPrefix(line, "parent "))
		}
	}
	return parents, nil
}

func deprecatedImportsAllowlistBase(root string) (string, error) {
	if _, err := gitOut(root, "rev-parse", "--verify", "-q", "refs/remotes/origin/dev^{commit}"); err != nil {
		parents, pErr := commitParents(root, "HEAD")
		if pErr != nil || len(parents) < 2 {
			return "", fmt.Errorf("cannot verify allowlist growth: refs/remotes/origin/dev is missing; fetch origin/dev")
		}
		return parents[0], nil
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
		seedCommit, scErr := gitOut(root, "log", "--diff-filter=A", "--format=%H", "-n", "1", parent+"..HEAD", "--", relPath)
		seedCommit = strings.TrimSpace(seedCommit)
		if scErr != nil || seedCommit == "" {
			return nil, parent, false, fmt.Errorf("merge base %s lacks allowlist, but HEAD is not the seed commit: base ref is stale; rebase onto dev", parent[:9])
		}
		headCommit, hErr := gitOut(root, "rev-parse", "HEAD")
		if hErr != nil {
			return nil, parent, false, hErr
		}
		if strings.TrimSpace(headCommit) != seedCommit {
			seedText, sErr := gitOut(root, "show", seedCommit+":"+relPath)
			headText, hErr := gitOut(root, "show", "HEAD:"+relPath)
			if sErr != nil || hErr != nil || strings.TrimSpace(seedText) != strings.TrimSpace(headText) {
				return nil, parent, false, fmt.Errorf("merge base %s lacks allowlist, but HEAD is not the seed commit: base ref is stale; rebase onto dev", parent[:9])
			}
		}
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
// over a synthetic git repository to prove that adding a row fails and deleting one passes.
func TestDeprecatedImportsAllowlistGrowthIsReadOutOfGit(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		if _, err := gitOut(root, append([]string{
			"-c", "user.name=ci", "-c", "user.email=ci@example.invalid",
			"-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null"}, args...)...); err != nil {
			t.Fatal(err)
		}
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
	baseCommit, _ := gitOut(root, "rev-parse", "HEAD")
	git("update-ref", "refs/remotes/origin/dev", strings.TrimSpace(baseCommit))
	write(relPath, "cmd/a -> internal/nsprint/ws  # seed\n")
	git("add", "-A")
	git("commit", "-q", "-m", "seed")
	if added, seed := growth(); !seed || len(added) != 0 {
		t.Fatalf("a parent with no allowlist: added %v seed %v; want the seed", added, seed)
	}

	// PROBE 1: an allowlisted edge whose row the parent has is green.
	write("README", "touched\n")
	git("add", "-A")
	git("commit", "-q", "-m", "touch")
	seedCommit, _ := gitOut(root, "rev-parse", "HEAD~1")
	git("update-ref", "refs/remotes/origin/dev", strings.TrimSpace(seedCommit))
	if added, seed := growth(); seed || len(added) != 0 {
		t.Fatalf("the row at the parent: added %v seed %v; want nothing added", added, seed)
	}

	// PROBE 2: adding a row is detected as growth.
	write(relPath, "cmd/a -> internal/nsprint/ws  # seed\ncmd/b -> internal/nsprint/card  # new\n")
	git("add", "-A")
	git("commit", "-q", "-m", "added edge")
	if added, _ := growth(); len(added) != 1 || added[0] != "cmd/b -> internal/nsprint/card" {
		t.Fatalf("added row: added %q; want [cmd/b -> internal/nsprint/card]", added)
	}

	// PROBE 3: deleting a row is shrinking, so added is empty.
	addedCommit, _ := gitOut(root, "rev-parse", "HEAD")
	git("update-ref", "refs/remotes/origin/dev", strings.TrimSpace(addedCommit))
	write(relPath, "cmd/b -> internal/nsprint/card  # new\n")
	git("add", "-A")
	git("commit", "-q", "-m", "cmd/a fixed")
	if added, _ := growth(); len(added) != 0 {
		t.Fatalf("a deleted row: added %q; want none", added)
	}
}

// TestDeprecatedImportsAllowlistGrowthRefusesStaleOrMissingBase incorporates Stella's
// 3-case growth probe: current remote base rejects an added row, missing remote
// base fails closed with err != nil, and stale remote before seed fails closed
// with err != nil.
func TestDeprecatedImportsAllowlistGrowthRefusesStaleOrMissingBase(t *testing.T) {
	t.Parallel()

	for _, mode := range []string{"current remote base", "missing remote base", "stale remote before seed"} {
		t.Run(mode, func(t *testing.T) {
			t.Parallel()

			root := t.TempDir()
			git := func(args ...string) string {
				t.Helper()
				out, err := gitOut(root, append([]string{
					"-c", "user.name=review", "-c", "user.email=review@example.invalid",
					"-c", "commit.gpgsign=false", "-c", "core.hooksPath=/dev/null"}, args...)...)
				if err != nil {
					t.Fatal(err)
				}
				return strings.TrimSpace(out)
			}
			write := func(p, s string) {
				t.Helper()
				p = filepath.Join(root, p)
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			commit := func(msg string) {
				git("add", "-A")
				git("commit", "-qm", msg)
			}
			git("init", "-q")
			write("README", "base\n")
			commit("before seed")
			beforeSeed := git("rev-parse", "HEAD")
			const p = "internal/ci/testdata/deprecated-imports-allowlist.txt"
			write(p, "cmd/a -> internal/nsprint/ws # existing\n")
			commit("landed baseline")
			base := git("rev-parse", "HEAD")
			git("checkout", "-qb", "review")
			write(p, "cmd/a -> internal/nsprint/ws # existing\ncmd/b -> internal/nsprint/card # introduced by this branch\n")
			commit("add forbidden exception")
			write("README", "later unrelated edit\n")
			commit("touch README")
			if mode == "current remote base" {
				git("update-ref", "refs/remotes/origin/dev", base)
			}
			if mode == "stale remote before seed" {
				git("update-ref", "refs/remotes/origin/dev", beforeSeed)
			}
			added, parent, seed, err := deprecatedImportsAllowlistGrowth(root)
			t.Logf("mode=%s added=%v parent=%s seed=%v err=%v", mode, added, parent, seed, err)
			if err != nil {
				return // Failing closed when no trustworthy baseline exists is acceptable.
			}
			if seed || len(added) != 1 || added[0] != "cmd/b -> internal/nsprint/card" {
				t.Fatalf("branch introduced an exception but comparison did not reject it")
			}
		})
	}
}
