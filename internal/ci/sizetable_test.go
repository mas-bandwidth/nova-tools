package ci

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// sizetable_test.go holds the one reading of a measured package-size table, so
// the Windows table and the darwin table are parsed by the same code and answer
// the same way.
//
// There are two of these tables now — testdata/ci/package-sizes-windows.tsv and
// testdata/ci/package-sizes-darwin.tsv — and a third (the Linux
// testdata/ci/package-sizes.tsv) in the older two-column shape. The two
// three-column tables are the same measurement on different machines: a package
// whose Windows size says nothing about its darwin size, recorded in the same
// format so the same rules apply to both. Writing the parser twice would have
// been two chances to disagree about what a censored row means, and what a
// censored row means is the whole lesson: integration-4's group was dropped by
// guessing an unknown size downward.
//
// This file was extracted from ci_windows_pr_test.go in the same commit that
// added the darwin table, with no change to what it accepts or what it says: the
// messages below are the Windows messages with the file name as a parameter, and
// every Windows test body is byte-identical across that extraction.

// measuredSize is one row's reading of a size column: the number, and whether
// the run that produced it was CENSORED (it hit its timeout, so the true size is
// at least this) or absent altogether. Every plan deals a censored or missing
// size across every slot the group opened, because an unknown size must never be
// guessed downward — that is the mistake that dropped integration-4's group.
type measuredSize struct {
	secs     float64
	measured bool
	censored bool
}

// readSizeTable parses a three-column measured-size table (import path, short,
// full) into its two columns, keyed by import path, reporting every malformed
// row against the name the table is known by. It is the same reading ci.yml's
// shard plans do in awk, so a row that would confuse them is a red run here
// first.
func readSizeTable(t *testing.T, root, rel string) (short, full map[string]measuredSize) {
	t.Helper()
	raw := readFile(t, filepath.Join(root, filepath.FromSlash(rel)))
	short, full = map[string]measuredSize{}, map[string]measuredSize{}
	read := func(n int, field string) (measuredSize, bool) {
		if field == "-" {
			return measuredSize{}, true
		}
		censored := strings.HasSuffix(field, "+")
		secs, err := strconv.ParseFloat(strings.TrimSuffix(field, "+"), 64)
		if err != nil {
			t.Errorf("%s:%d: %q is neither a number of seconds, a censored number ending in +, nor - for unmeasured: %v", rel, n+1, field, err)
			return measuredSize{}, false
		}
		return measuredSize{secs: secs, measured: true, censored: censored}, true
	}
	for n, line := range strings.Split(raw, "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 3 {
			t.Errorf("%s:%d: want exactly three tab-separated fields (import path, short, full), got %d: %q", rel, n+1, len(fields), line)
			continue
		}
		pkg := fields[0]
		if !strings.HasPrefix(pkg, modulePath) {
			t.Errorf("%s:%d: %q is not an import path in this module; the table is keyed the way `go list` prints, like its Linux sibling", rel, n+1, pkg)
			continue
		}
		if _, dup := short[pkg]; dup {
			t.Errorf("%s:%d: %s is measured twice; the plans read the first row and the second is a silent lie", rel, n+1, pkg)
		}
		// A measured package must still exist. A row for a package that has
		// left is a row the plans will never read and nobody will ever correct.
		dir := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(pkg, modulePath)))
		if _, err := os.Stat(dir); err != nil {
			t.Errorf("%s:%d: %s is in the table but not in the tree; delete the row", rel, n+1, pkg)
		}
		if v, ok := read(n, fields[1]); ok {
			short[pkg] = v
		}
		if v, ok := read(n, fields[2]); ok {
			full[pkg] = v
		}
	}
	return short, full
}
