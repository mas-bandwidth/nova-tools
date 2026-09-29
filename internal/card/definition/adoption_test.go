package definition

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// B11: adoption. This profile is a new card format: no card in the repository
// carries `SCHEMA: v2`, and the cards that exist carry dispatch keys (LEGS, MODE,
// TURNS, DEADLINE, SOURCE, BASE, SPEC, ROUTE) that belong to a layer this one does
// not specify. The test runs parse over every file of the repository whose first
// line starts with RESULT (outside this package's own testdata) and records, in a
// golden file, which parse and the causes for those that do not. It changes no
// format and makes nothing parse; it is the standing report of how far the profile
// is from the cards that exist. When the golden differs, a card was added or
// changed, or the profile moved: read the difference, then run with -update.
func TestAdoptionOfTheCardsThatExist(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..", "..")
	own := filepath.Join(root, "internal", "card", "definition", "testdata")
	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch n := d.Name(); {
			case n == ".git", strings.HasPrefix(n, ".gocache"), n == "node_modules":
				return filepath.SkipDir
			}
			if p == own {
				return filepath.SkipDir
			}
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil || !info.Mode().IsRegular() || info.Size() > 1<<20 || strings.HasSuffix(p, ".go") {
			return nil
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil
		}
		first, _, _ := strings.Cut(string(b), "\n")
		if strings.HasPrefix(first, "RESULT") {
			files = append(files, p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(files)
	var out strings.Builder
	out.WriteString("# The files of the repository whose first line starts with RESULT, and what the v2 profile makes of each.\n")
	out.WriteString("# `parses` or `refused` with the sorted causes and the header keys named (unknown-key: LEGS MODE ...).\n")
	parsed := 0
	for _, f := range files {
		b, _ := os.ReadFile(f)
		rel := filepath.ToSlash(strings.TrimPrefix(f, root+string(filepath.Separator)))
		defs, refs := parse([]Source{{Name: rel, Data: b}})
		if refs == nil && len(defs) == 1 {
			parsed++
			out.WriteString(rel + "\tparses\n")
			continue
		}
		causes := map[string]bool{}
		unknown := map[string]bool{}
		missing := map[string]bool{}
		for _, r := range list(refs) {
			causes[string(r.Cause)] = true
			switch r.Cause {
			case CauseUnknownKey:
				unknown[r.Field] = true
			case CauseRequired:
				missing[r.Field] = true
			}
		}
		out.WriteString(rel + "\trefused\t" + joinSet(causes))
		if len(unknown) > 0 {
			out.WriteString("\tunknown-key: " + joinSet(unknown))
		}
		if len(missing) > 0 {
			out.WriteString("\trequired: " + joinSet(missing))
		}
		out.WriteString("\n")
	}
	out.WriteString("# " + strconv.Itoa(parsed) + " of " + strconv.Itoa(len(files)) + " parse\n")
	golden := filepath.Join("testdata", "adoption.golden")
	if *update {
		if err := os.WriteFile(golden, []byte(out.String()), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("%v (run with -update to write it)", err)
	}
	if string(want) != out.String() {
		t.Fatalf("the adoption report differs from testdata/adoption.golden (run with -update after reading the difference):\n--- got ---\n%s--- want ---\n%s", out.String(), want)
	}
	// The three swarm cards are dispatch-layer cards: they refuse on the dispatch keys
	// and on the profile keys they lack. They are not made to parse.
	for _, name := range []string{"queue-1282-bench-hygiene-home-guard", "tools11-c1-links-specpulse", "tools11-c2-links-toplevel"} {
		line := ""
		for _, l := range strings.Split(out.String(), "\n") {
			if strings.Contains(l, "cmd/nova-swarm/testdata/cards/"+name+".md") {
				line = l
			}
		}
		if !strings.Contains(line, "refused") || !strings.Contains(line, "LEGS") || !strings.Contains(line, "required:") {
			t.Errorf("%s: %q", name, line)
		}
	}
}

func joinSet(m map[string]bool) string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return strings.Join(ks, " ")
}
