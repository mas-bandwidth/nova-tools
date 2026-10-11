package ci

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// shardWallsPath is the CL shard walls ledger: each CL package's wall on the
// last green run that ran it uncached (docs/TESTS.md, "The CL shards fit two
// minutes").
const shardWallsPath = "internal/ci/testdata/shard-walls.tsv"

// shardWallCap is the most a CL package may take, in seconds. A shard is
// cancelled at two minutes and the test target stops a package at 110 s, so a
// package past 60 leaves no room for the rest of its shard.
const shardWallCap = 60.0

// shardWallSource is where a row was measured: @run<id> for a GitHub Actions
// run, @<bench> (lower case) for a named machine.
var shardWallSource = regexp.MustCompile(`^@(run[0-9]+|[a-z][a-z0-9-]*)$`)

// shardWallRunner is the runner class that measured a row, from the run's job
// that logged the package (self-hosted, macos-latest, ubuntu-latest), or - where
// it is not known.
var shardWallRunner = regexp.MustCompile(`^([a-z][a-z0-9.-]*|-)$`)

// shardWallRow is one ledger row.
type shardWallRow struct {
	pkg     string
	seconds float64
	source  string
	runner  string
	line    int
}

// parseShardWalls reads the ledger's text into its rows, and a sentence for
// each line that is not a row: the wrong number of fields, a wall that is not
// a non-negative number, a source that names no run or bench, a runner class
// that is no label, a package twice.
func parseShardWalls(text string) (rows []shardWallRow, problems []string) {
	seen := map[string]int{}
	for i, line := range strings.Split(text, "\n") {
		n := i + 1
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 4 {
			problems = append(problems, fmt.Sprintf("%s:%d has %d tab-separated fields, want four: package, seconds, @run<id> or @<bench>, runner class or -", shardWallsPath, n, len(fields)))
			continue
		}
		secs, err := strconv.ParseFloat(fields[1], 64)
		if err != nil || secs < 0 {
			problems = append(problems, fmt.Sprintf("%s:%d: %q is not a wall in seconds", shardWallsPath, n, fields[1]))
			continue
		}
		if !shardWallSource.MatchString(fields[2]) {
			problems = append(problems, fmt.Sprintf("%s:%d: %q names no measurement, want @run<id> or @<bench>", shardWallsPath, n, fields[2]))
			continue
		}
		if !shardWallRunner.MatchString(fields[3]) {
			problems = append(problems, fmt.Sprintf("%s:%d: %q names no runner class, want the job's class (self-hosted, macos-latest) or -", shardWallsPath, n, fields[3]))
			continue
		}
		if first, ok := seen[fields[0]]; ok {
			problems = append(problems, fmt.Sprintf("%s:%d names %s again (first at line %d)", shardWallsPath, n, fields[0], first))
			continue
		}
		seen[fields[0]] = n
		rows = append(rows, shardWallRow{pkg: fields[0], seconds: secs, source: fields[2], runner: fields[3], line: n})
	}
	return rows, problems
}

// shardWallProblems holds the rows to the cap and to the tree: a row over
// shardWallCap, a package that has tests and no row, and a row for a package
// the tree does not hold are each one sentence. packages maps every live
// package directory to whether it holds a _test.go file.
func shardWallProblems(rows []shardWallRow, packages map[string]bool) []string {
	var problems []string
	named := map[string]bool{}
	for _, r := range rows {
		named[r.pkg] = true
		if _, ok := packages[r.pkg]; !ok {
			problems = append(problems, fmt.Sprintf("%s:%d names %s, which is not a live package of the tree: drop the row", shardWallsPath, r.line, r.pkg))
			continue
		}
		if r.seconds > shardWallCap {
			problems = append(problems, fmt.Sprintf("%s:%d: %s took %.1f s (%s), past the %.0f s a CL package may take in a two-minute shard: make its tests fit or move the slow ones behind the `slow` build tag (nightly-slow.yml runs them), then record the wall it has", shardWallsPath, r.line, r.pkg, r.seconds, r.source, shardWallCap))
		}
	}
	var missing []string
	for pkg, hasTests := range packages {
		if hasTests && !named[pkg] {
			missing = append(missing, pkg)
		}
	}
	sort.Strings(missing)
	for _, pkg := range missing {
		problems = append(problems, fmt.Sprintf("%s has tests and no row in %s: record its wall from the run that tested it", pkg, shardWallsPath))
	}
	return problems
}

// livePackageDirs walks the module the way `go list ./...` does -- a directory
// holding a .go file is a package; testdata, vendor, and names starting with
// "." or "_" are skipped, and so is a directory with its own go.mod -- keeps the
// live ones (pkg/pkgselect/DEPRECATED read by loadLiveTree), and reports
// for each whether it holds a _test.go file.
func livePackageDirs(t *testing.T, root string) map[string]bool {
	t.Helper()
	lt := loadLiveTree(t, root)
	packages := map[string]bool{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (name == "testdata" || name == "vendor" || strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_")) {
				return filepath.SkipDir
			}
			if path != root {
				if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		rel, err := filepath.Rel(root, filepath.Dir(path))
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." || !lt.Package(rel) {
			return nil
		}
		packages[rel] = packages[rel] || strings.HasSuffix(d.Name(), "_test.go")
		return nil
	})
	require.NoError(t, err, "walking the module for its packages")
	return packages
}

// TestEveryCLPackageFitsItsShardWall holds the CL shard walls ledger: every
// live package with tests has a row, every row names a live package and a
// measurement, and no package takes more than 60 s, so a shard of the CL tier
// fits two minutes with the cap unchanged.
func TestEveryCLPackageFitsItsShardWall(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	text, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(shardWallsPath)))
	require.NoError(t, err, "the CL shard walls ledger")
	rows, problems := parseShardWalls(string(text))
	require.NotEmpty(t, rows, "%s has no rows", shardWallsPath)
	problems = append(problems, shardWallProblems(rows, livePackageDirs(t, root))...)
	for _, p := range problems {
		t.Errorf("%s", p)
	}
}

// TestTheShardWallRuleRefusesGrowthAndGaps is the rule's reversed witnesses on
// rows held in the test: a package past 60 s, a live package with tests and no
// row, a row for a package the tree lacks, and a malformed row each read red;
// a package without tests needs no row, and 60.0 s is still in.
func TestTheShardWallRuleRefusesGrowthAndGaps(t *testing.T) {
	t.Parallel()

	rows, problems := parseShardWalls("# header\ncmd/a\t60.0\t@run123\tself-hosted\ncmd/b\t61.2\t@local\t-\ncmd/gone\t1.0\t@run9\tmacos-latest\ncmd/c\tfast\t@run1\tself-hosted\ncmd/d\t1.0\tyesterday\tself-hosted\ncmd/a\t1.0\t@run1\tself-hosted\ncmd/e 1.0 @run1\ncmd/f\t1.0\t@run1\ncmd/g\t1.0\t@run1\tSelf Hosted\n")
	require.Equal(t, []string{
		shardWallsPath + `:5: "fast" is not a wall in seconds`,
		shardWallsPath + `:6: "yesterday" names no measurement, want @run<id> or @<bench>`,
		shardWallsPath + ":7 names cmd/a again (first at line 2)",
		shardWallsPath + ":8 has 1 tab-separated fields, want four: package, seconds, @run<id> or @<bench>, runner class or -",
		shardWallsPath + ":9 has 3 tab-separated fields, want four: package, seconds, @run<id> or @<bench>, runner class or -",
		shardWallsPath + `:10: "Self Hosted" names no runner class, want the job's class (self-hosted, macos-latest) or -`,
	}, problems)
	got := shardWallProblems(rows, map[string]bool{"cmd/a": true, "cmd/b": true, "cmd/untested": false, "cmd/new": true})
	require.Equal(t, []string{
		shardWallsPath + ":3: cmd/b took 61.2 s (@local), past the 60 s a CL package may take in a two-minute shard: make its tests fit or move the slow ones behind the `slow` build tag (nightly-slow.yml runs them), then record the wall it has",
		shardWallsPath + ":4 names cmd/gone, which is not a live package of the tree: drop the row",
		"cmd/new has tests and no row in " + shardWallsPath + ": record its wall from the run that tested it",
	}, got)
}
