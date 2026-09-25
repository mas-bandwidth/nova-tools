package benchretire

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// benchKeyShapes find a per-bench key written with a variable bench name in
// Go ("bench:"+b+":x", "bench:%s:x") and Lua ('bench:' .. b .. ':x').
var benchKeyShapes = []*regexp.Regexp{
	regexp.MustCompile(`"bench:"\s*\+\s*[A-Za-z_][A-Za-z0-9_.]*\s*\+\s*"(:[a-z_:]*[a-z_])"`),
	regexp.MustCompile(`"bench:%s(:[a-z_:]*[a-z_])`),
	regexp.MustCompile(`'bench:' \.\. [A-Za-z_][A-Za-z0-9_.]* \.\. '(:[a-z_:]*[a-z_])'`),
}

// TestRowsCoverEveryBenchKey: every per-bench key shape the tree names (non-test
// Go and Lua) is in Rows, so `bench retire` leaves no row of a retired bench.
// A shape that ends in ':' is a prefix built further (cards:<where>), and the
// card sets are in Rows by name.
func TestRowsCoverEveryBenchKey(t *testing.T) {
	t.Parallel()

	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("repo root %s: %v", root, err)
	}
	have := map[string]bool{}
	for _, s := range Rows {
		have[s] = true
	}
	missing := map[string]string{}
	seen := 0
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if n := d.Name(); n == ".git" || n == "testdata" || n == "vendor" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(p, "_test.go") || !(strings.HasSuffix(p, ".go") || strings.HasSuffix(p, ".lua")) {
			return nil
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		for _, re := range benchKeyShapes {
			for _, m := range re.FindAllStringSubmatch(string(raw), -1) {
				seen++
				if !have[m[1]] {
					missing[m[1]] = p
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if seen < 20 {
		t.Fatalf("found only %d bench key shapes; the patterns no longer match the tree", seen)
	}
	for s, p := range missing {
		t.Errorf("bench:<b>%s (named in %s) is not in benchretire.Rows: a retired bench would leave it", s, p)
	}
}
